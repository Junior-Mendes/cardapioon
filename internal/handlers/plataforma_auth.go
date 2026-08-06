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

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// Autenticação do painel da plataforma.
//
// É um fluxo paralelo ao dos lojistas, não uma extensão dele: tabela própria, audiência de
// token própria e tabela de sessões própria. O código é semelhante ao de auth.go de
// propósito — a alternativa era generalizar o fluxo existente com um parâmetro de "tipo de
// conta", e um ramo condicional dentro do caminho de autenticação dos lojistas é
// exactamente onde um erro futuro passaria a dar acesso cruzado.

// PlataformaLogin autentica um administrador da plataforma.
func (h *Handler) PlataformaLogin(c *gin.Context) {
	var in loginInput
	if err := c.ShouldBindJSON(&in); err != nil {
		erroCliente(c, http.StatusBadRequest, "Dados de acesso inválidos")
		return
	}

	email := strings.ToLower(strings.TrimSpace(in.Identifier))

	var admin models.PlataformaAdmin
	err := h.DB.Where("email = ? AND ativo = ?", email, true).First(&admin).Error
	if err != nil {
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			h.erroInterno(c, "procurar administrador da plataforma", err)
			return
		}
		// Trabalho equivalente ao de uma verificação real, para que o tempo de resposta não
		// revele quais os emails com acesso à plataforma.
		auth.DummyVerify()
		erroCliente(c, http.StatusUnauthorized, msgCredenciais)
		return
	}

	precisaRehash, err := auth.VerifyPassword(admin.SenhaHash, in.Password)
	if err != nil {
		if errors.Is(err, auth.ErrSenhaIncorreta) {
			// Registado porque uma senha errada aqui é um sinal com peso diferente do de um
			// lojista: esta conta vê todos os restaurantes da plataforma.
			slog.Warn("tentativa de acesso ao painel da plataforma com senha incorrecta",
				"email", email, "ip", c.ClientIP())
			erroCliente(c, http.StatusUnauthorized, msgCredenciais)
			return
		}
		h.erroInterno(c, "verificar senha do administrador da plataforma", err)
		return
	}

	agora := time.Now()
	actualizacoes := map[string]any{"last_login_at": agora}
	if precisaRehash {
		if novoHash, err := auth.HashPassword(in.Password); err == nil {
			actualizacoes["senha_hash"] = novoHash
			actualizacoes["password_changed_at"] = agora
		}
	}
	if err := h.DB.Model(&admin).Updates(actualizacoes).Error; err != nil {
		// Não impedir o login por causa disto.
		slog.Error("falha ao actualizar dados de login da plataforma", "admin_id", admin.ID, "erro", err)
	}

	slog.Info("acesso ao painel da plataforma", "admin_id", admin.ID, "ip", c.ClientIP())
	h.responderComSessaoPlataforma(c, http.StatusOK, &admin)
}

// responderComSessaoPlataforma emite o par de tokens do painel da plataforma.
func (h *Handler) responderComSessaoPlataforma(c *gin.Context, status int, a *models.PlataformaAdmin) {
	accessToken, expiraEm, err := h.Tokens.IssuePlataformaToken(a.ID)
	if err != nil {
		h.erroInterno(c, "emitir token da plataforma", err)
		return
	}

	refreshToken, hashRefresh := auth.NewRefreshToken()
	registo := models.PlataformaRefreshToken{
		AdminID:   a.ID,
		TokenHash: hashRefresh,
		ExpiresAt: time.Now().Add(h.Tokens.RefreshTTL()),
		UserAgent: truncar(c.Request.UserAgent(), 255),
		IP:        c.ClientIP(),
		CreatedAt: time.Now(),
	}
	if err := h.DB.Create(&registo).Error; err != nil {
		h.erroInterno(c, "gravar refresh token da plataforma", err)
		return
	}

	c.JSON(status, gin.H{
		"access_token":  accessToken,
		"refresh_token": refreshToken,
		"token_type":    "Bearer",
		"expires_at":    expiraEm.UTC().Format(time.RFC3339),
		"admin": gin.H{
			"id":    a.ID,
			"nome":  a.Nome,
			"email": a.Email,
		},
	})
}

// PlataformaRefresh troca um refresh token da plataforma por um novo par.
//
// Mesma rotação obrigatória do fluxo dos lojistas: a reapresentação de um token já
// substituído revoga todas as sessões do administrador, porque indica roubo de sessão.
func (h *Handler) PlataformaRefresh(c *gin.Context) {
	var in refreshInput
	if err := c.ShouldBindJSON(&in); err != nil {
		erroCliente(c, http.StatusBadRequest, "Pedido inválido")
		return
	}

	hash := auth.HashToken(in.RefreshToken)
	agora := time.Now()

	var registo models.PlataformaRefreshToken
	if err := h.DB.Where("token_hash = ?", hash).First(&registo).Error; err != nil {
		erroCliente(c, http.StatusUnauthorized, "Sessão inválida. Faça login novamente.")
		return
	}

	if registo.ReplacedBy != nil {
		slog.Warn("reutilização de refresh token da plataforma detectada; a revogar sessões",
			"admin_id", registo.AdminID, "ip", c.ClientIP())
		h.DB.Model(&models.PlataformaRefreshToken{}).
			Where("admin_id = ? AND revoked_at IS NULL", registo.AdminID).
			Update("revoked_at", agora)
		erroCliente(c, http.StatusUnauthorized, "Sessão inválida. Faça login novamente.")
		return
	}
	if !registo.Valido(agora) {
		erroCliente(c, http.StatusUnauthorized, "Sessão expirada. Faça login novamente.")
		return
	}

	var admin models.PlataformaAdmin
	if err := h.DB.Where("id = ? AND ativo = ?", registo.AdminID, true).First(&admin).Error; err != nil {
		erroCliente(c, http.StatusUnauthorized, "Sessão inválida. Faça login novamente.")
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
		return tx.Create(&models.PlataformaRefreshToken{
			AdminID:   admin.ID,
			TokenHash: novoHash,
			ExpiresAt: agora.Add(h.Tokens.RefreshTTL()),
			UserAgent: truncar(c.Request.UserAgent(), 255),
			IP:        c.ClientIP(),
			CreatedAt: agora,
		}).Error
	})
	if err != nil {
		h.erroInterno(c, "rodar refresh token da plataforma", err)
		return
	}

	accessToken, expiraEm, err := h.Tokens.IssuePlataformaToken(admin.ID)
	if err != nil {
		h.erroInterno(c, "emitir token da plataforma na renovação", err)
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"access_token":  accessToken,
		"refresh_token": novoToken,
		"token_type":    "Bearer",
		"expires_at":    expiraEm.UTC().Format(time.RFC3339),
	})
}

// PlataformaLogout revoga o refresh token apresentado.
func (h *Handler) PlataformaLogout(c *gin.Context) {
	var in refreshInput
	if err := c.ShouldBindJSON(&in); err == nil && in.RefreshToken != "" {
		h.DB.Model(&models.PlataformaRefreshToken{}).
			Where("token_hash = ? AND revoked_at IS NULL", auth.HashToken(in.RefreshToken)).
			Update("revoked_at", time.Now())
	}
	c.JSON(http.StatusOK, gin.H{"message": "Sessão terminada"})
}

// PlataformaEu devolve o perfil do administrador autenticado.
//
// Serve também para o painel confirmar que a sessão guardada no browser ainda é válida
// antes de mostrar o interface.
func (h *Handler) PlataformaEu(c *gin.Context) {
	var admin models.PlataformaAdmin
	if err := h.DB.Where("id = ? AND ativo = ?", middleware.GetPlataformaAdminID(c), true).
		First(&admin).Error; err != nil {
		erroCliente(c, http.StatusUnauthorized, "Sessão inválida")
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"id": admin.ID, "nome": admin.Nome, "email": admin.Email,
		"last_login_at": admin.LastLoginAt,
	})
}

// PlataformaAlterarSenha permite ao administrador mudar a própria senha.
func (h *Handler) PlataformaAlterarSenha(c *gin.Context) {
	var in alterarSenhaInput
	if err := c.ShouldBindJSON(&in); err != nil {
		erroCliente(c, http.StatusBadRequest, "Pedido inválido")
		return
	}
	if err := auth.ValidarForcaSenha(in.NovaSenha); err != nil {
		erroCliente(c, http.StatusBadRequest, err.Error())
		return
	}

	var admin models.PlataformaAdmin
	if err := h.DB.First(&admin, middleware.GetPlataformaAdminID(c)).Error; err != nil {
		erroCliente(c, http.StatusUnauthorized, "Sessão inválida")
		return
	}

	if _, err := auth.VerifyPassword(admin.SenhaHash, in.SenhaAtual); err != nil {
		erroCliente(c, http.StatusUnauthorized, "A senha actual está incorrecta")
		return
	}

	novoHash, err := auth.HashPassword(in.NovaSenha)
	if err != nil {
		h.erroInterno(c, "gerar hash na alteração de senha da plataforma", err)
		return
	}

	agora := time.Now()
	if err := h.DB.Model(&admin).Updates(map[string]any{
		"senha_hash":          novoHash,
		"password_changed_at": agora,
	}).Error; err != nil {
		h.erroInterno(c, "gravar nova senha da plataforma", err)
		return
	}

	// Mudar a senha termina as outras sessões: se a conta estava comprometida, quem a tinha
	// perde o acesso a toda a plataforma.
	if err := h.DB.Model(&models.PlataformaRefreshToken{}).
		Where("admin_id = ? AND revoked_at IS NULL", admin.ID).
		Update("revoked_at", agora).Error; err != nil {
		slog.Error("falha ao revogar sessões da plataforma", "admin_id", admin.ID, "erro", err)
	}

	h.auditarPlataforma(c, "plataforma_senha_alterada", "plataforma_admin", fmt.Sprint(admin.ID), "", nil)
	c.JSON(http.StatusOK, gin.H{
		"message": "Senha alterada. As outras sessões abertas foram terminadas.",
	})
}

// auditarPlataforma registra uma acção do dono do SaaS no mesmo registo de auditoria dos
// lojistas.
//
// usuario_id fica sempre nulo — a coluna refere-se a `usuarios`, e um administrador da
// plataforma não está lá. A identificação vai no detalhe, e a acção é prefixada com
// "plataforma_" para que seja distinguível numa consulta. tenantID, quando presente,
// aponta o restaurante afectado, de modo a que a acção apareça no histórico dele.
func (h *Handler) auditarPlataforma(c *gin.Context, acao, recurso, recursoID, detalhe string, tenantID *uint) {
	adminID := middleware.GetPlataformaAdminID(c)

	prefixo := fmt.Sprintf("plataforma_admin=%d", adminID)
	if detalhe != "" {
		detalhe = prefixo + " " + detalhe
	} else {
		detalhe = prefixo
	}

	registo := models.AuditLog{
		TenantID:  tenantID,
		Acao:      acao,
		Recurso:   recurso,
		RecursoID: recursoID,
		Detalhe:   detalhe,
		IP:        c.ClientIP(),
		CreatedAt: time.Now(),
	}

	if err := h.DB.Create(&registo).Error; err != nil {
		slog.Error("falha ao gravar auditoria da plataforma", "acao", acao, "erro", err)
	}
}

// GarantirAdminPlataforma cria a primeira conta do painel a partir do ambiente.
//
// Só actua quando a tabela está vazia: assim as variáveis podem ficar no .env sem que um
// reinício reponha a senha de uma conta cuja senha já foi mudada no painel. Sem estas
// variáveis a tabela fica vazia e o painel, embora montado, não tem como ser aberto — que
// é o comportamento correcto para quem não o quer usar.
func (h *Handler) GarantirAdminPlataforma() error {
	email := strings.ToLower(strings.TrimSpace(h.Cfg.PlataformaAdminEmail))
	senha := h.Cfg.PlataformaAdminPassword

	var existentes int64
	if err := h.DB.Model(&models.PlataformaAdmin{}).Count(&existentes).Error; err != nil {
		return fmt.Errorf("contar administradores da plataforma: %w", err)
	}

	if existentes > 0 {
		if email != "" {
			slog.Info("painel da plataforma já tem conta criada; PLATAFORMA_ADMIN_EMAIL ignorado")
		}
		return nil
	}
	if email == "" || senha == "" {
		slog.Warn("painel da plataforma sem nenhuma conta: defina PLATAFORMA_ADMIN_EMAIL e " +
			"PLATAFORMA_ADMIN_PASSWORD para criar a primeira")
		return nil
	}

	// Falhar aqui é melhor do que criar uma conta com acesso a toda a plataforma e uma
	// senha fraca.
	if err := auth.ValidarForcaSenha(senha); err != nil {
		return fmt.Errorf("PLATAFORMA_ADMIN_PASSWORD: %w", err)
	}

	hash, err := auth.HashPassword(senha)
	if err != nil {
		return fmt.Errorf("gerar hash da senha do administrador da plataforma: %w", err)
	}

	agora := time.Now()
	admin := models.PlataformaAdmin{
		Nome:              "Administrador da Plataforma",
		Email:             email,
		SenhaHash:         hash,
		Ativo:             true,
		PasswordChangedAt: &agora,
	}
	if err := h.DB.Create(&admin).Error; err != nil {
		return fmt.Errorf("criar administrador da plataforma: %w", err)
	}

	slog.Info("primeira conta do painel da plataforma criada", "email", email,
		"nota", "mude a senha no painel e remova PLATAFORMA_ADMIN_PASSWORD do ambiente")
	return nil
}
