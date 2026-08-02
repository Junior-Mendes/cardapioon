package handlers

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"cardapio-online/internal/auth"
	"cardapio-online/internal/middleware"
	"cardapio-online/internal/models"
	"cardapio-online/internal/validate"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

const (
	validadeResetSenha = 30 * time.Minute
	// Mensagem única para credenciais inválidas: distinguir "email não existe" de
	// "senha errada" permitiria enumerar as contas da plataforma.
	msgCredenciais = "Email ou senha incorrectos"
)

type registarInput struct {
	Nome     string `json:"nome" binding:"required,min=2,max=150"`
	Slug     string `json:"slug" binding:"required"`
	Email    string `json:"email" binding:"required,email,max=150"`
	Password string `json:"password" binding:"required"`
}

// Registar cria um tenant e o respectivo utilizador owner.
func (h *Handler) Registar(c *gin.Context) {
	var in registarInput
	if err := c.ShouldBindJSON(&in); err != nil {
		erroCliente(c, http.StatusBadRequest, "Dados de registo inválidos")
		return
	}

	slug, err := validate.Slug(in.Slug)
	if err != nil {
		erroCliente(c, http.StatusBadRequest, err.Error())
		return
	}
	if err := auth.ValidarForcaSenha(in.Password); err != nil {
		erroCliente(c, http.StatusBadRequest, err.Error())
		return
	}

	email := strings.ToLower(strings.TrimSpace(in.Email))
	nome := strings.TrimSpace(in.Nome)

	hash, err := auth.HashPassword(in.Password)
	if err != nil {
		h.erroInterno(c, "gerar hash de senha no registo", err)
		return
	}

	agora := time.Now()
	tokenVerificacao := auth.NewOpaqueToken(32)

	var tenant models.Tenant
	var usuario models.Usuario

	err = h.DB.Transaction(func(tx *gorm.DB) error {
		// A unicidade é garantida pelos índices UNIQUE em slug e email; a verificação
		// prévia serve apenas para dar uma mensagem melhor. A condição de corrida entre
		// duas inscrições simultâneas é resolvida pelo erro do índice, abaixo.
		var n int64
		if err := tx.Model(&models.Tenant{}).Where("slug = ?", slug).Count(&n).Error; err != nil {
			return err
		}
		if n > 0 {
			return errSlugEmUso
		}
		if err := tx.Model(&models.Usuario{}).Where("email = ?", email).Count(&n).Error; err != nil {
			return err
		}
		if n > 0 {
			return errEmailEmUso
		}

		tenant = models.Tenant{
			Nome: nome, Slug: slug, Ativo: true,
			SenhaHash:    hash,
			DomainStatus: models.DomainNenhum,
			// Dinheiro fica activo por omissão para que o restaurante possa receber
			// encomendas antes de configurar pagamento online.
			DinheiroAtivo: true,
		}
		if err := tx.Create(&tenant).Error; err != nil {
			return err
		}

		usuario = models.Usuario{
			TenantID:          tenant.ID,
			Nome:              "Proprietário " + nome,
			Email:             email,
			SenhaHash:         hash,
			PasswordChangedAt: &agora,
			Role:              models.RoleOwner,
			Ativo:             true,
		}
		return tx.Create(&usuario).Error
	})

	switch {
	case errors.Is(err, errSlugEmUso):
		erroCliente(c, http.StatusConflict, "Este endereço já está em uso por outro restaurante")
		return
	case errors.Is(err, errEmailEmUso):
		erroCliente(c, http.StatusConflict, "Este email já está registado")
		return
	case err != nil:
		h.erroInterno(c, "registar tenant", err)
		return
	}

	h.sincronizarRotaTenant(&tenant)

	// O email de verificação é enviado de forma assíncrona: um SMTP lento não deve
	// atrasar a resposta do registo.
	go h.enviarEmailVerificacao(usuario, tokenVerificacao)

	h.responderComSessao(c, http.StatusCreated, &tenant, &usuario, gin.H{
		"message": "Restaurante registado com sucesso",
	})
}

var (
	errSlugEmUso  = errors.New("slug em uso")
	errEmailEmUso = errors.New("email em uso")
)

type loginInput struct {
	// Identifier aceita email; o slug do restaurante já não é aceite como identificador
	// porque não identifica um utilizador e permitia entrar na conta do primeiro owner.
	Identifier string `json:"identifier" binding:"required"`
	Password   string `json:"password" binding:"required"`
}

// Login autentica e devolve access + refresh token.
func (h *Handler) Login(c *gin.Context) {
	var in loginInput
	if err := c.ShouldBindJSON(&in); err != nil {
		erroCliente(c, http.StatusBadRequest, "Dados de acesso inválidos")
		return
	}

	email := strings.ToLower(strings.TrimSpace(in.Identifier))

	var usuario models.Usuario
	err := h.DB.Where("email = ? AND ativo = ?", email, true).First(&usuario).Error
	if err != nil {
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			h.erroInterno(c, "procurar utilizador no login", err)
			return
		}
		// Trabalho equivalente ao de uma verificação real, para que o tempo de resposta
		// não revele se o email existe.
		auth.DummyVerify()
		erroCliente(c, http.StatusUnauthorized, msgCredenciais)
		return
	}

	precisaRehash, err := auth.VerifyPassword(usuario.SenhaHash, in.Password)
	if err != nil {
		if errors.Is(err, auth.ErrSenhaIncorreta) {
			erroCliente(c, http.StatusUnauthorized, msgCredenciais)
			return
		}
		h.erroInterno(c, "verificar senha", err)
		return
	}

	var tenant models.Tenant
	if err := h.DB.First(&tenant, usuario.TenantID).Error; err != nil {
		h.erroInterno(c, "carregar tenant do utilizador", err)
		return
	}
	if !tenant.Ativo {
		erroCliente(c, http.StatusForbidden, "Esta conta está suspensa. Contacte o suporte.")
		return
	}

	agora := time.Now()
	actualizacoes := map[string]any{"last_login_at": agora}

	// Migração transparente do SHA-256 legado para bcrypt, agora que temos a senha em
	// claro e já a validámos.
	if precisaRehash {
		if novoHash, err := auth.HashPassword(in.Password); err == nil {
			actualizacoes["senha_hash"] = novoHash
			actualizacoes["password_changed_at"] = agora
			slog.Info("hash de senha migrado para bcrypt", "usuario_id", usuario.ID)
		}
	}
	if err := h.DB.Model(&usuario).Updates(actualizacoes).Error; err != nil {
		// Não impedir o login por causa disto.
		slog.Error("falha ao actualizar dados de login", "usuario_id", usuario.ID, "erro", err)
	}

	h.responderComSessao(c, http.StatusOK, &tenant, &usuario, nil)
}

// responderComSessao emite os tokens e devolve o payload de sessão.
func (h *Handler) responderComSessao(c *gin.Context, status int, t *models.Tenant, u *models.Usuario, extra gin.H) {
	accessToken, expiraEm, err := h.Tokens.IssueAccessToken(t.ID, u.ID, u.Role)
	if err != nil {
		h.erroInterno(c, "emitir access token", err)
		return
	}

	refreshToken, hashRefresh := auth.NewRefreshToken()
	registo := models.RefreshToken{
		UsuarioID: u.ID,
		TenantID:  t.ID,
		TokenHash: hashRefresh,
		ExpiresAt: time.Now().Add(h.Tokens.RefreshTTL()),
		UserAgent: truncar(c.Request.UserAgent(), 255),
		IP:        c.ClientIP(),
		CreatedAt: time.Now(),
	}
	if err := h.DB.Create(&registo).Error; err != nil {
		h.erroInterno(c, "gravar refresh token", err)
		return
	}

	resposta := gin.H{
		"access_token":  accessToken,
		"refresh_token": refreshToken,
		"token_type":    "Bearer",
		"expires_at":    expiraEm.UTC().Format(time.RFC3339),
		"usuario": gin.H{
			"id":    u.ID,
			"nome":  u.Nome,
			"email": u.Email,
			"role":  u.Role,
		},
		"restaurante": gin.H{
			"id":   t.ID,
			"nome": t.Nome,
			"slug": t.Slug,
		},
	}
	for k, v := range extra {
		resposta[k] = v
	}
	c.JSON(status, resposta)
}

type refreshInput struct {
	RefreshToken string `json:"refresh_token" binding:"required"`
}

// Refresh troca um refresh token por um novo par de tokens.
//
// A rotação é obrigatória: o token usado é marcado como substituído. Se um token já
// substituído voltar a ser apresentado, toda a família de sessões desse utilizador é
// revogada, porque isso indica que o token foi roubado.
func (h *Handler) Refresh(c *gin.Context) {
	var in refreshInput
	if err := c.ShouldBindJSON(&in); err != nil {
		erroCliente(c, http.StatusBadRequest, "Pedido inválido")
		return
	}

	hash := auth.HashToken(in.RefreshToken)
	agora := time.Now()

	var registo models.RefreshToken
	if err := h.DB.Where("token_hash = ?", hash).First(&registo).Error; err != nil {
		erroCliente(c, http.StatusUnauthorized, "Sessão inválida. Faça login novamente.")
		return
	}

	if registo.ReplacedBy != nil {
		// Reutilização de um token já rodado.
		slog.Warn("reutilização de refresh token detectada; a revogar sessões",
			"usuario_id", registo.UsuarioID, "ip", c.ClientIP())
		h.DB.Model(&models.RefreshToken{}).
			Where("usuario_id = ? AND revoked_at IS NULL", registo.UsuarioID).
			Update("revoked_at", agora)
		erroCliente(c, http.StatusUnauthorized, "Sessão inválida. Faça login novamente.")
		return
	}
	if !registo.Valido(agora) {
		erroCliente(c, http.StatusUnauthorized, "Sessão expirada. Faça login novamente.")
		return
	}

	var usuario models.Usuario
	if err := h.DB.Where("id = ? AND ativo = ?", registo.UsuarioID, true).First(&usuario).Error; err != nil {
		erroCliente(c, http.StatusUnauthorized, "Sessão inválida. Faça login novamente.")
		return
	}
	var tenant models.Tenant
	if err := h.DB.First(&tenant, usuario.TenantID).Error; err != nil || !tenant.Ativo {
		erroCliente(c, http.StatusForbidden, "Esta conta está suspensa.")
		return
	}

	novoToken, novoHash := auth.NewRefreshToken()

	err := h.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&registo).Updates(map[string]any{
			"revoked_at":  agora,
			"replaced_by": novoHash,
		}).Error; err != nil {
			return err
		}
		return tx.Create(&models.RefreshToken{
			UsuarioID: usuario.ID,
			TenantID:  tenant.ID,
			TokenHash: novoHash,
			ExpiresAt: agora.Add(h.Tokens.RefreshTTL()),
			UserAgent: truncar(c.Request.UserAgent(), 255),
			IP:        c.ClientIP(),
			CreatedAt: agora,
		}).Error
	})
	if err != nil {
		h.erroInterno(c, "rodar refresh token", err)
		return
	}

	accessToken, expiraEm, err := h.Tokens.IssueAccessToken(tenant.ID, usuario.ID, usuario.Role)
	if err != nil {
		h.erroInterno(c, "emitir access token na renovação", err)
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"access_token":  accessToken,
		"refresh_token": novoToken,
		"token_type":    "Bearer",
		"expires_at":    expiraEm.UTC().Format(time.RFC3339),
	})
}

// Logout revoga o refresh token apresentado.
func (h *Handler) Logout(c *gin.Context) {
	var in refreshInput
	if err := c.ShouldBindJSON(&in); err == nil && in.RefreshToken != "" {
		h.DB.Model(&models.RefreshToken{}).
			Where("token_hash = ? AND revoked_at IS NULL", auth.HashToken(in.RefreshToken)).
			Update("revoked_at", time.Now())
	}
	c.JSON(http.StatusOK, gin.H{"message": "Sessão terminada"})
}

type esqueciSenhaInput struct {
	Email string `json:"email" binding:"required,email"`
}

// EsqueciSenha inicia o fluxo de recuperação.
//
// Responde sempre com sucesso, exista ou não a conta: caso contrário a resposta revelaria
// quais os emails registados na plataforma.
func (h *Handler) EsqueciSenha(c *gin.Context) {
	var in esqueciSenhaInput
	if err := c.ShouldBindJSON(&in); err != nil {
		erroCliente(c, http.StatusBadRequest, "Email inválido")
		return
	}

	resposta := gin.H{"message": "Se existir uma conta com este email, receberá instruções em breve."}
	email := strings.ToLower(strings.TrimSpace(in.Email))

	var usuario models.Usuario
	if err := h.DB.Where("email = ? AND ativo = ?", email, true).First(&usuario).Error; err != nil {
		c.JSON(http.StatusOK, resposta)
		return
	}

	token := auth.NewOpaqueToken(32)
	registo := models.PasswordReset{
		UsuarioID: usuario.ID,
		TokenHash: auth.HashToken(token),
		ExpiresAt: time.Now().Add(validadeResetSenha),
		CreatedIP: c.ClientIP(),
		CreatedAt: time.Now(),
	}
	if err := h.DB.Create(&registo).Error; err != nil {
		h.erroInterno(c, "criar pedido de reset de senha", err)
		return
	}

	go h.enviarEmailReset(usuario, token)

	c.JSON(http.StatusOK, resposta)
}

type redefinirSenhaInput struct {
	Token    string `json:"token" binding:"required"`
	Password string `json:"password" binding:"required"`
}

// RedefinirSenha consome um token de reset e grava a nova senha.
func (h *Handler) RedefinirSenha(c *gin.Context) {
	var in redefinirSenhaInput
	if err := c.ShouldBindJSON(&in); err != nil {
		erroCliente(c, http.StatusBadRequest, "Pedido inválido")
		return
	}
	if err := auth.ValidarForcaSenha(in.Password); err != nil {
		erroCliente(c, http.StatusBadRequest, err.Error())
		return
	}

	agora := time.Now()
	var registo models.PasswordReset
	if err := h.DB.Where("token_hash = ?", auth.HashToken(in.Token)).First(&registo).Error; err != nil {
		erroCliente(c, http.StatusBadRequest, "Link de recuperação inválido ou já utilizado")
		return
	}
	if !registo.Valido(agora) {
		erroCliente(c, http.StatusBadRequest, "Link de recuperação expirado. Peça um novo.")
		return
	}

	novoHash, err := auth.HashPassword(in.Password)
	if err != nil {
		h.erroInterno(c, "gerar hash na redefinição de senha", err)
		return
	}

	err = h.DB.Transaction(func(tx *gorm.DB) error {
		// Marcar o token como usado com a condição used_at IS NULL torna o consumo
		// atómico: dois pedidos simultâneos com o mesmo token só um vence.
		res := tx.Model(&models.PasswordReset{}).
			Where("id = ? AND used_at IS NULL", registo.ID).
			Update("used_at", agora)
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return errTokenJaUsado
		}

		if err := tx.Model(&models.Usuario{}).Where("id = ?", registo.UsuarioID).
			Updates(map[string]any{
				"senha_hash":          novoHash,
				"password_changed_at": agora,
			}).Error; err != nil {
			return err
		}

		// Redefinir a senha termina todas as sessões abertas: se a conta estava
		// comprometida, o atacante perde o acesso.
		return tx.Model(&models.RefreshToken{}).
			Where("usuario_id = ? AND revoked_at IS NULL", registo.UsuarioID).
			Update("revoked_at", agora).Error
	})

	if errors.Is(err, errTokenJaUsado) {
		erroCliente(c, http.StatusBadRequest, "Link de recuperação inválido ou já utilizado")
		return
	}
	if err != nil {
		h.erroInterno(c, "redefinir senha", err)
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Senha alterada com sucesso. Já pode entrar."})
}

var errTokenJaUsado = errors.New("token já usado")

type alterarSenhaInput struct {
	SenhaAtual string `json:"senha_atual" binding:"required"`
	NovaSenha  string `json:"nova_senha" binding:"required"`
}

// AlterarSenha permite a um utilizador autenticado mudar a própria senha.
func (h *Handler) AlterarSenha(c *gin.Context) {
	var in alterarSenhaInput
	if err := c.ShouldBindJSON(&in); err != nil {
		erroCliente(c, http.StatusBadRequest, "Pedido inválido")
		return
	}
	if err := auth.ValidarForcaSenha(in.NovaSenha); err != nil {
		erroCliente(c, http.StatusBadRequest, err.Error())
		return
	}

	userID := middleware.GetUserID(c)
	var usuario models.Usuario
	if err := h.DB.First(&usuario, userID).Error; err != nil {
		erroCliente(c, http.StatusUnauthorized, "Sessão inválida")
		return
	}

	if _, err := auth.VerifyPassword(usuario.SenhaHash, in.SenhaAtual); err != nil {
		erroCliente(c, http.StatusUnauthorized, "A senha actual está incorrecta")
		return
	}

	novoHash, err := auth.HashPassword(in.NovaSenha)
	if err != nil {
		h.erroInterno(c, "gerar hash na alteração de senha", err)
		return
	}

	agora := time.Now()
	if err := h.DB.Model(&usuario).Updates(map[string]any{
		"senha_hash":          novoHash,
		"password_changed_at": agora,
	}).Error; err != nil {
		h.erroInterno(c, "gravar nova senha", err)
		return
	}

	h.auditar(c, "senha_alterada", "usuario", fmt.Sprint(usuario.ID), "")
	c.JSON(http.StatusOK, gin.H{"message": "Senha alterada com sucesso"})
}

// --- Emails ---

func (h *Handler) enviarEmailReset(u models.Usuario, token string) {
	link := fmt.Sprintf("%s/redefinir-senha?token=%s", h.Cfg.BaseURL, token)

	texto := fmt.Sprintf(`Olá %s,

Recebemos um pedido para redefinir a senha da sua conta.

Abra o link seguinte para escolher uma nova senha (válido durante 30 minutos):
%s

Se não foi você a fazer este pedido, ignore esta mensagem: a sua senha actual continua válida.

Cardápio Online`, u.Nome, link)

	html := fmt.Sprintf(`<p>Olá %s,</p>
<p>Recebemos um pedido para redefinir a senha da sua conta.</p>
<p><a href="%s">Escolher uma nova senha</a> (válido durante 30 minutos)</p>
<p>Se não foi você a fazer este pedido, ignore esta mensagem: a sua senha actual continua válida.</p>
<p>Cardápio Online</p>`, htmlEscape(u.Nome), link)

	if err := h.Mailer.Send(u.Email, "Redefinir a senha da sua conta", texto, html); err != nil {
		slog.Error("falha ao enviar email de reset", "usuario_id", u.ID, "erro", err)
	}
}

func (h *Handler) enviarEmailVerificacao(u models.Usuario, token string) {
	link := fmt.Sprintf("%s/verificar-email?token=%s", h.Cfg.BaseURL, token)

	texto := fmt.Sprintf(`Bem-vindo, %s!

Confirme o seu email para activar todas as funcionalidades da conta:
%s

Cardápio Online`, u.Nome, link)

	html := fmt.Sprintf(`<p>Bem-vindo, %s!</p>
<p><a href="%s">Confirmar o meu email</a></p>
<p>Cardápio Online</p>`, htmlEscape(u.Nome), link)

	if err := h.Mailer.Send(u.Email, "Confirme o seu email", texto, html); err != nil {
		slog.Error("falha ao enviar email de verificação", "usuario_id", u.ID, "erro", err)
	}
}

func truncar(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
