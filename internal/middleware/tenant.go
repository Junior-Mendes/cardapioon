package middleware

import (
	"errors"
	"net/http"
	"strings"

	"cardapio-online/internal/models"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// TenantResolver resolve o tenant das rotas públicas (storefront).
//
// Separado de RequireAuth de propósito: o Host identifica *que restaurante mostrar* ao
// consumidor, e nunca *quem está autenticado*. Misturar os dois era a causa do
// escalonamento horizontal de privilégios da versão anterior.
type TenantResolver struct {
	db         *gorm.DB
	mainDomain string
}

func NewTenantResolver(db *gorm.DB, mainDomain string) *TenantResolver {
	return &TenantResolver{db: db, mainDomain: mainDomain}
}

// ResolvePublic identifica o tenant de uma rota pública e aborta com 404 se não existir.
func (r *TenantResolver) ResolvePublic() gin.HandlerFunc {
	return func(c *gin.Context) {
		tenant, err := r.Lookup(c)
		if err != nil || tenant == nil {
			abortJSON(c, http.StatusNotFound, "Restaurante não encontrado")
			return
		}
		c.Set(CtxTenantID, tenant.ID)
		c.Set(CtxSlug, tenant.Slug)
		c.Set("tenant", tenant)
		c.Next()
	}
}

// Lookup encontra o tenant pela ordem: parâmetro :slug → Host → query ?tenant.
//
// O cabeçalho X-Tenant-Slug foi removido: era aceite em rotas administrativas e permitia
// a qualquer cliente escolher o tenant sobre o qual operava.
func (r *TenantResolver) Lookup(c *gin.Context) (*models.Tenant, error) {
	if slug := c.Param("slug"); slug != "" {
		return r.porSlug(slug)
	}

	host := HostSemPorta(c.Request.Host)
	if host != "" && !hostLocal(host) {
		if slug, ok := r.SubdomainSlug(host); ok {
			return r.porSlug(slug)
		}
		// Domínio próprio: só resolve se a propriedade tiver sido verificada.
		var t models.Tenant
		err := r.db.Where(
			"domain = ? AND ativo = ? AND domain_status = ?",
			host, true, models.DomainVerificado,
		).First(&t).Error
		if err == nil {
			return &t, nil
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, err
		}
	}

	if slug := c.Query("tenant"); slug != "" {
		return r.porSlug(slug)
	}
	return nil, gorm.ErrRecordNotFound
}

func (r *TenantResolver) porSlug(slug string) (*models.Tenant, error) {
	slug = strings.ToLower(strings.TrimSpace(slug))
	if slug == "" {
		return nil, gorm.ErrRecordNotFound
	}
	var t models.Tenant
	if err := r.db.Where("slug = ? AND ativo = ?", slug, true).First(&t).Error; err != nil {
		return nil, err
	}
	return &t, nil
}

// SubdomainSlug extrai o slug de um host que seja subdomínio do domínio principal.
//
// Aceita "loja.exemplo.pt" e "www.loja.exemplo.pt", devolvendo "loja" em ambos os casos.
func (r *TenantResolver) SubdomainSlug(host string) (string, bool) {
	if r.mainDomain == "" || host == r.mainDomain {
		return "", false
	}
	sufixo := "." + r.mainDomain
	if !strings.HasSuffix(host, sufixo) {
		return "", false
	}

	sub := strings.TrimSuffix(host, sufixo)
	if sub == "" || sub == "www" {
		return "", false
	}

	partes := strings.Split(sub, ".")
	// O rótulo mais à direita é o slug: em "www.loja" o slug é "loja".
	return partes[len(partes)-1], true
}

// IsMainDomain indica se o host é o domínio raiz do SaaS.
func (r *TenantResolver) IsMainDomain(host string) bool {
	host = HostSemPorta(host)
	return host == r.mainDomain || host == "www."+r.mainDomain || hostLocal(host)
}

// TenantScope filtra uma query pelo tenant do contexto.
//
// Se o tenant não estiver definido devolve um predicado sempre falso, para que um erro
// de montagem de middleware resulte em zero linhas em vez de nas linhas de todos.
func TenantScope(c *gin.Context) func(*gorm.DB) *gorm.DB {
	return func(db *gorm.DB) *gorm.DB {
		id := GetTenantID(c)
		if id == 0 {
			return db.Where("1 = 0")
		}
		return db.Where("tenant_id = ?", id)
	}
}

// HostSemPorta remove a porta de um cabeçalho Host, lidando com IPv6 entre parênteses.
func HostSemPorta(host string) string {
	host = strings.TrimSpace(strings.ToLower(host))
	if host == "" {
		return ""
	}
	if strings.HasPrefix(host, "[") {
		if i := strings.Index(host, "]"); i > 0 {
			return host[1:i]
		}
		return host
	}
	if i := strings.LastIndex(host, ":"); i > 0 {
		return host[:i]
	}
	return host
}

func hostLocal(host string) bool {
	switch host {
	case "localhost", "127.0.0.1", "::1", "0.0.0.0":
		return true
	}
	return false
}
