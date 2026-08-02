package handlers

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"cardapio-online/internal/auth"
	"cardapio-online/internal/middleware"
	"cardapio-online/internal/models"
	"cardapio-online/internal/validate"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// prefixoTXTVerificacao é o subdomínio onde o cliente coloca o registo TXT de prova.
const prefixoTXTVerificacao = "_cardapio-verify"

// GetConfig devolve as configurações do restaurante autenticado.
func (h *Handler) GetConfig(c *gin.Context) {
	var t models.Tenant
	if err := h.DB.First(&t, middleware.GetTenantID(c)).Error; err != nil {
		erroCliente(c, http.StatusNotFound, "Restaurante não encontrado")
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"nome":                 t.Nome,
		"nif":                  t.NIF,
		"slug":                 t.Slug,
		"domain":               t.Domain,
		"domain_status":        t.DomainStatus,
		"domain_verified_at":   t.DomainVerifiedAt,
		"cartao_credito_ativo": t.CartaoCreditoAtivo,
		"cartao_debito_ativo":  t.CartaoDebitoAtivo,
		"dinheiro_ativo":       t.DinheiroAtivo,
		"main_domain":          h.Cfg.MainDomain,
		"storefront_url":       fmt.Sprintf("https://%s.%s/menu", t.Slug, h.Cfg.MainDomain),
	})
}

type updateConfigInput struct {
	Nome               *string `json:"nome"`
	NIF                *string `json:"nif"`
	CartaoCreditoAtivo *bool   `json:"cartao_credito_ativo"`
	CartaoDebitoAtivo  *bool   `json:"cartao_debito_ativo"`
	DinheiroAtivo      *bool   `json:"dinheiro_ativo"`
}

// UpdateConfig actualiza os dados do restaurante.
//
// O domínio personalizado deixou de ser alterável aqui: passou a ter o seu próprio fluxo
// em dois passos (SolicitarDominio + VerificarDominio), porque aceitá-lo directamente
// permitia reclamar o domínio de terceiros.
//
// Todos os campos são ponteiros para distinguir "não enviado" de "enviado vazio/false":
// a versão anterior desactivava silenciosamente todos os métodos de pagamento quando o
// frontend enviava um payload parcial.
func (h *Handler) UpdateConfig(c *gin.Context) {
	var t models.Tenant
	if err := h.DB.First(&t, middleware.GetTenantID(c)).Error; err != nil {
		erroCliente(c, http.StatusNotFound, "Restaurante não encontrado")
		return
	}

	var in updateConfigInput
	if err := c.ShouldBindJSON(&in); err != nil {
		erroCliente(c, http.StatusBadRequest, "Dados inválidos")
		return
	}

	campos := map[string]any{}

	if in.Nome != nil {
		nome := limparLinha(*in.Nome, 150)
		if nome == "" {
			erroCliente(c, http.StatusBadRequest, "O nome do restaurante não pode ficar vazio")
			return
		}
		campos["nome"] = nome
	}
	if in.NIF != nil {
		nif := strings.TrimSpace(*in.NIF)
		if nif != "" && !validate.NIFPortugues(nif) {
			erroCliente(c, http.StatusBadRequest, "NIF inválido")
			return
		}
		campos["nif"] = nif
	}
	if in.CartaoCreditoAtivo != nil {
		campos["cartao_credito_ativo"] = *in.CartaoCreditoAtivo
	}
	if in.CartaoDebitoAtivo != nil {
		campos["cartao_debito_ativo"] = *in.CartaoDebitoAtivo
	}
	if in.DinheiroAtivo != nil {
		campos["dinheiro_ativo"] = *in.DinheiroAtivo
	}

	if len(campos) == 0 {
		c.JSON(http.StatusOK, gin.H{"message": "Nada a actualizar"})
		return
	}

	if err := h.DB.Model(&t).Updates(campos).Error; err != nil {
		h.erroInterno(c, "actualizar configuração do tenant", err)
		return
	}

	h.auditar(c, "config_actualizada", "tenant", fmt.Sprint(t.ID), chavesDe(campos))
	c.JSON(http.StatusOK, gin.H{"message": "Configurações guardadas"})
}

type dominioInput struct {
	Domain string `json:"domain" binding:"required"`
}

// SolicitarDominio registra a intenção de usar um domínio próprio e devolve o registo TXT
// que o cliente tem de criar.
//
// A rota no Traefik NÃO é criada nesta fase. Só depois de VerificarDominio confirmar o
// TXT é que o domínio passa a ser encaminhado e a pedir certificado.
func (h *Handler) SolicitarDominio(c *gin.Context) {
	var t models.Tenant
	if err := h.DB.First(&t, middleware.GetTenantID(c)).Error; err != nil {
		erroCliente(c, http.StatusNotFound, "Restaurante não encontrado")
		return
	}

	var in dominioInput
	if err := c.ShouldBindJSON(&in); err != nil {
		erroCliente(c, http.StatusBadRequest, "Domínio inválido")
		return
	}

	dominio, err := validate.Domain(in.Domain)
	if err != nil {
		erroCliente(c, http.StatusBadRequest, err.Error())
		return
	}

	// String vazia remove o domínio configurado.
	if dominio == "" {
		if err := h.DB.Model(&t).Updates(map[string]any{
			"domain":              nil,
			"domain_status":       models.DomainNenhum,
			"domain_verify_token": "",
			"domain_verified_at":  nil,
		}).Error; err != nil {
			h.erroInterno(c, "remover domínio personalizado", err)
			return
		}
		t.Domain = nil
		t.DomainStatus = models.DomainNenhum
		h.sincronizarRotaTenant(&t)
		h.auditar(c, "dominio_removido", "tenant", fmt.Sprint(t.ID), "")
		c.JSON(http.StatusOK, gin.H{"message": "Domínio personalizado removido", "domain_status": models.DomainNenhum})
		return
	}

	if err := validate.DomainNaoPodeSerDoSaaS(dominio, h.Cfg.MainDomain); err != nil {
		erroCliente(c, http.StatusBadRequest, err.Error())
		return
	}

	// Um domínio já verificado por outro tenant não pode ser reclamado.
	var conflitos int64
	if err := h.DB.Model(&models.Tenant{}).
		Where("domain = ? AND id <> ?", dominio, t.ID).
		Count(&conflitos).Error; err != nil {
		h.erroInterno(c, "verificar conflito de domínio", err)
		return
	}
	if conflitos > 0 {
		erroCliente(c, http.StatusConflict, "Este domínio já está associado a outro restaurante")
		return
	}

	token := auth.NewOpaqueToken(24)
	if err := h.DB.Model(&t).Updates(map[string]any{
		"domain":              dominio,
		"domain_status":       models.DomainPendente,
		"domain_verify_token": token,
		"domain_verified_at":  nil,
	}).Error; err != nil {
		h.erroInterno(c, "gravar domínio pendente", err)
		return
	}

	// Se havia um domínio verificado antes, a rota é reescrita sem ele: o encaminhamento
	// pára até a nova verificação passar.
	t.Domain = &dominio
	t.DomainStatus = models.DomainPendente
	h.sincronizarRotaTenant(&t)

	h.auditar(c, "dominio_solicitado", "tenant", fmt.Sprint(t.ID), dominio)

	c.JSON(http.StatusAccepted, gin.H{
		"message":       "Domínio registado. Crie o registo TXT indicado e depois clique em verificar.",
		"domain":        dominio,
		"domain_status": models.DomainPendente,
		"registo_txt": gin.H{
			"tipo":  "TXT",
			"nome":  fmt.Sprintf("%s.%s", prefixoTXTVerificacao, dominio),
			"valor": token,
		},
		"registo_encaminhamento": gin.H{
			"tipo":  "CNAME",
			"nome":  dominio,
			"valor": h.Cfg.MainDomain,
			"nota":  "Se o seu registrador não permitir CNAME na raiz do domínio, use um registo A para o IP do servidor.",
		},
	})
}

// VerificarDominio confirma o registo TXT e activa o encaminhamento.
func (h *Handler) VerificarDominio(c *gin.Context) {
	var t models.Tenant
	if err := h.DB.First(&t, middleware.GetTenantID(c)).Error; err != nil {
		erroCliente(c, http.StatusNotFound, "Restaurante não encontrado")
		return
	}

	dominio := t.DomainValue()
	if dominio == "" || t.DomainVerifyToken == "" {
		erroCliente(c, http.StatusBadRequest, "Não há domínio pendente de verificação")
		return
	}

	ctx, cancelar := context.WithTimeout(c.Request.Context(), 8*time.Second)
	defer cancelar()

	nome := fmt.Sprintf("%s.%s", prefixoTXTVerificacao, dominio)
	registos, err := net.DefaultResolver.LookupTXT(ctx, nome)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"verificado": false,
			"motivo": fmt.Sprintf(
				"Não foi possível ler o registo TXT em %s. A propagação de DNS pode levar até algumas horas.", nome),
			"registo_txt": gin.H{"tipo": "TXT", "nome": nome, "valor": t.DomainVerifyToken},
		})
		return
	}

	encontrado := false
	for _, r := range registos {
		if strings.TrimSpace(r) == t.DomainVerifyToken {
			encontrado = true
			break
		}
	}
	if !encontrado {
		c.JSON(http.StatusOK, gin.H{
			"verificado":  false,
			"motivo":      "O registo TXT existe mas o valor não corresponde. Confirme que copiou o token completo.",
			"registo_txt": gin.H{"tipo": "TXT", "nome": nome, "valor": t.DomainVerifyToken},
		})
		return
	}

	agora := time.Now()
	if err := h.DB.Model(&t).Updates(map[string]any{
		"domain_status":      models.DomainVerificado,
		"domain_verified_at": agora,
	}).Error; err != nil {
		h.erroInterno(c, "gravar domínio verificado", err)
		return
	}

	t.DomainStatus = models.DomainVerificado
	t.DomainVerifiedAt = &agora
	// Só agora a rota do Traefik inclui o domínio do cliente.
	h.sincronizarRotaTenant(&t)

	h.auditar(c, "dominio_verificado", "tenant", fmt.Sprint(t.ID), dominio)

	c.JSON(http.StatusOK, gin.H{
		"verificado":    true,
		"message":       "Domínio verificado. O certificado SSL é emitido no primeiro acesso.",
		"domain":        dominio,
		"domain_status": models.DomainVerificado,
	})
}

// CheckDNS informa se o domínio já aponta para a plataforma.
//
// É apenas um auxiliar de diagnóstico para o painel; não concede qualquer permissão.
func (h *Handler) CheckDNS(c *gin.Context) {
	dominio, err := validate.Domain(c.Query("domain"))
	if err != nil || dominio == "" {
		erroCliente(c, http.StatusBadRequest, "Domínio inválido")
		return
	}

	ctx, cancelar := context.WithTimeout(c.Request.Context(), 8*time.Second)
	defer cancelar()

	aponta := false
	if ips, err := net.DefaultResolver.LookupIPAddr(ctx, dominio); err == nil {
		if principais, err := net.DefaultResolver.LookupIPAddr(ctx, h.Cfg.MainDomain); err == nil {
			for _, ip := range ips {
				for _, p := range principais {
					if ip.IP.Equal(p.IP) {
						aponta = true
					}
				}
			}
		}
	}
	if !aponta {
		if cname, err := net.DefaultResolver.LookupCNAME(ctx, dominio); err == nil {
			cname = strings.TrimSuffix(strings.ToLower(cname), ".")
			aponta = cname == h.Cfg.MainDomain || strings.HasSuffix(cname, "."+h.Cfg.MainDomain)
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"domain":      dominio,
		"configured":  aponta,
		"main_domain": h.Cfg.MainDomain,
	})
}

// DetectTenant informa o storefront qual o restaurante do domínio actual.
func (h *Handler) DetectTenant(c *gin.Context) {
	t, err := h.Resolver.Lookup(c)
	if err != nil || t == nil {
		c.JSON(http.StatusOK, gin.H{"status": "main_domain"})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"status": "resolved",
		"tenant": gin.H{"nome": t.Nome, "slug": t.Slug, "domain": t.Domain},
	})
}

// --- Gestão de utilizadores ---

// ListUsuarios lista os utilizadores do tenant.
func (h *Handler) ListUsuarios(c *gin.Context) {
	var usuarios []models.Usuario
	if err := h.DB.Scopes(middleware.TenantScope(c)).Order("id asc").Find(&usuarios).Error; err != nil {
		h.erroInterno(c, "listar utilizadores", err)
		return
	}
	c.JSON(http.StatusOK, usuarios)
}

type criarUsuarioInput struct {
	Nome     string `json:"nome" binding:"required,min=2,max=150"`
	Email    string `json:"email" binding:"required,email,max=150"`
	Password string `json:"password" binding:"required"`
	Role     string `json:"role" binding:"required"`
}

// CreateUsuario cria um utilizador dentro do tenant.
func (h *Handler) CreateUsuario(c *gin.Context) {
	var in criarUsuarioInput
	if err := c.ShouldBindJSON(&in); err != nil {
		erroCliente(c, http.StatusBadRequest, "Dados inválidos")
		return
	}

	if !models.RolesValidos[in.Role] {
		erroCliente(c, http.StatusBadRequest, "Perfil de utilizador inválido")
		return
	}
	// Só um owner pode criar outro owner: caso contrário um admin poderia escalar
	// privilégios criando uma conta owner para si.
	if in.Role == models.RoleOwner && middleware.GetRole(c) != models.RoleOwner {
		erroCliente(c, http.StatusForbidden, "Apenas o proprietário pode criar outro proprietário")
		return
	}
	if err := auth.ValidarForcaSenha(in.Password); err != nil {
		erroCliente(c, http.StatusBadRequest, err.Error())
		return
	}

	email := strings.ToLower(strings.TrimSpace(in.Email))
	hash, err := auth.HashPassword(in.Password)
	if err != nil {
		h.erroInterno(c, "gerar hash de senha", err)
		return
	}

	agora := time.Now()
	usuario := models.Usuario{
		TenantID:          middleware.GetTenantID(c),
		Nome:              limparLinha(in.Nome, 150),
		Email:             email,
		SenhaHash:         hash,
		PasswordChangedAt: &agora,
		Role:              in.Role,
		Ativo:             true,
	}

	if err := h.DB.Create(&usuario).Error; err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "duplicate") {
			erroCliente(c, http.StatusConflict, "Este email já está em uso")
			return
		}
		h.erroInterno(c, "criar utilizador", err)
		return
	}

	h.auditar(c, "usuario_criado", "usuario", fmt.Sprint(usuario.ID), in.Role)
	c.JSON(http.StatusCreated, usuario)
}

// DeleteUsuario remove um utilizador do tenant.
func (h *Handler) DeleteUsuario(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil {
		erroCliente(c, http.StatusBadRequest, "Identificador inválido")
		return
	}

	if uint(id) == middleware.GetUserID(c) {
		erroCliente(c, http.StatusBadRequest, "Não pode remover a sua própria conta")
		return
	}

	// O escopo de tenant é aplicado na leitura: sem isto, um lojista poderia apagar
	// utilizadores de outro restaurante indicando o ID deles.
	var usuario models.Usuario
	if err := h.DB.Scopes(middleware.TenantScope(c)).Where("id = ?", id).First(&usuario).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			erroCliente(c, http.StatusNotFound, "Utilizador não encontrado")
			return
		}
		h.erroInterno(c, "procurar utilizador", err)
		return
	}

	if usuario.Role == models.RoleOwner {
		var owners int64
		if err := h.DB.Model(&models.Usuario{}).
			Where("tenant_id = ? AND role = ? AND ativo = ?", usuario.TenantID, models.RoleOwner, true).
			Count(&owners).Error; err != nil {
			h.erroInterno(c, "contar proprietários", err)
			return
		}
		// Permitir remover um owner desde que sobre pelo menos um: bloquear sempre, como
		// antes, deixava contas com um owner inacessível para sempre.
		if owners <= 1 {
			erroCliente(c, http.StatusBadRequest,
				"Não é possível remover o único proprietário da conta")
			return
		}
	}

	if err := h.DB.Delete(&usuario).Error; err != nil {
		h.erroInterno(c, "remover utilizador", err)
		return
	}

	h.auditar(c, "usuario_removido", "usuario", fmt.Sprint(usuario.ID), usuario.Email)
	c.JSON(http.StatusOK, gin.H{"message": "Utilizador removido"})
}

// SincronizarRotasTraefik regenera todos os ficheiros de rota a partir da base de dados e
// remove os que já não correspondam a um tenant activo. Corre no arranque.
func (h *Handler) SincronizarRotasTraefik() error {
	if err := h.Traefik.WriteDefault(); err != nil {
		return fmt.Errorf("gravar rota do domínio principal: %w", err)
	}

	var tenants []models.Tenant
	if err := h.DB.Where("ativo = ?", true).Find(&tenants).Error; err != nil {
		return fmt.Errorf("carregar tenants activos: %w", err)
	}

	slugs := make([]string, 0, len(tenants))
	for i := range tenants {
		h.sincronizarRotaTenant(&tenants[i])
		slugs = append(slugs, tenants[i].Slug)
	}

	if err := h.Traefik.Prune(slugs); err != nil {
		// Uma rota órfã que não se consegue remover não deve impedir o arranque.
		slog.Error("falha ao remover rotas órfãs do Traefik", "erro", err)
	}

	slog.Info("rotas do Traefik sincronizadas", "tenants", len(slugs))
	return nil
}

func chavesDe(m map[string]any) string {
	chaves := make([]string, 0, len(m))
	for k := range m {
		chaves = append(chaves, k)
	}
	return strings.Join(chaves, ",")
}
