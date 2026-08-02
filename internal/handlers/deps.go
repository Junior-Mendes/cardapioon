package handlers

import (
	"log/slog"
	"net/http"
	"time"

	"cardapio-online/internal/auth"
	"cardapio-online/internal/config"
	"cardapio-online/internal/mail"
	"cardapio-online/internal/middleware"
	"cardapio-online/internal/models"
	"cardapio-online/internal/traefik"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// Deps agrupa as dependências dos handlers.
//
// Substitui as structs vazias (TenantHandler{}, MenuHandler{}, ...) que liam a variável
// global db.DB: com dependências explícitas os testes podem injectar uma base de dados
// de teste e um Sender de email falso.
type Deps struct {
	DB       *gorm.DB
	Cfg      *config.Config
	Tokens   *auth.TokenService
	Mailer   mail.Sender
	Traefik  *traefik.Writer
	Resolver *middleware.TenantResolver
}

type Handler struct {
	*Deps
}

func New(d *Deps) *Handler { return &Handler{Deps: d} }

// erroInterno registra o erro real e devolve ao cliente uma mensagem genérica.
//
// A versão anterior devolvia err.Error() em cerca de quinze handlers, expondo nomes de
// tabelas, colunas e mensagens do MySQL a qualquer cliente.
func (h *Handler) erroInterno(c *gin.Context, contexto string, err error) {
	slog.Error(contexto,
		"erro", err,
		"caminho", c.Request.URL.Path,
		"metodo", c.Request.Method,
		"tenant_id", middleware.GetTenantID(c),
	)

	resposta := gin.H{"error": "Ocorreu um erro ao processar o pedido. Tente novamente."}
	if h.Cfg.DevMode() {
		// Em desenvolvimento o detalhe é útil; em produção nunca sai.
		resposta["debug"] = err.Error()
	}
	c.JSON(http.StatusInternalServerError, resposta)
}

func erroCliente(c *gin.Context, status int, msg string) {
	c.JSON(status, gin.H{"error": msg})
}

// auditar registra uma acção administrativa sensível.
func (h *Handler) auditar(c *gin.Context, acao, recurso, recursoID, detalhe string) {
	tenantID := middleware.GetTenantID(c)
	userID := middleware.GetUserID(c)

	registo := models.AuditLog{
		Acao:      acao,
		Recurso:   recurso,
		RecursoID: recursoID,
		Detalhe:   detalhe,
		IP:        c.ClientIP(),
		CreatedAt: time.Now(),
	}
	if tenantID > 0 {
		registo.TenantID = &tenantID
	}
	if userID > 0 {
		registo.UsuarioID = &userID
	}

	// A auditoria não deve fazer falhar a operação que a originou.
	if err := h.DB.Create(&registo).Error; err != nil {
		slog.Error("falha ao gravar registo de auditoria", "acao", acao, "erro", err)
	}
}

// tenantDoContexto devolve o tenant carregado por ResolvePublic, ou vai buscá-lo.
func (h *Handler) tenantDoContexto(c *gin.Context) (*models.Tenant, error) {
	if v, existe := c.Get("tenant"); existe {
		if t, ok := v.(*models.Tenant); ok {
			return t, nil
		}
	}
	id := middleware.GetTenantID(c)
	if id == 0 {
		return nil, gorm.ErrRecordNotFound
	}
	var t models.Tenant
	if err := h.DB.First(&t, id).Error; err != nil {
		return nil, err
	}
	return &t, nil
}

// sincronizarRotaTenant regrava o ficheiro de rota do Traefik para um tenant.
//
// O domínio personalizado só é incluído depois de verificado: o Writer recebe apenas o
// que pode ser encaminhado.
func (h *Handler) sincronizarRotaTenant(t *models.Tenant) {
	rota := traefik.TenantRoute{Slug: t.Slug}
	if t.DomainAtivo() {
		rota.CustomDomain = t.DomainValue()
	}
	if err := h.Traefik.WriteTenant(rota); err != nil {
		slog.Error("falha ao gravar rota do Traefik", "slug", t.Slug, "erro", err)
	}
}
