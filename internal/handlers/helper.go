package handlers

import (
	"os"
	"strings"

	"cardapio-online/internal/db"
	"cardapio-online/internal/middleware"
	"cardapio-online/internal/models"

	"github.com/gin-gonic/gin"
)

// ResolveTenant tenta encontrar o Tenant com base no contexto do gin (ID, slug ou Host)
func ResolveTenant(c *gin.Context) (*models.Tenant, error) {
	var tenant models.Tenant
	tenantID := middleware.GetTenantID(c)

	// 1. Pelo ID no contexto (se logado ou autenticado via middleware)
	if tenantID > 0 {
		if err := db.DB.First(&tenant, tenantID).Error; err != nil {
			return nil, err
		}
		return &tenant, nil
	}

	// 2. Pelo parâmetro :slug na URL
	tenantSlug := c.Param("slug")
	if tenantSlug != "" {
		if err := db.DB.Where("slug = ? AND ativo = ?", tenantSlug, true).First(&tenant).Error; err != nil {
			return nil, err
		}
		return &tenant, nil
	}

	// 3. Pelo Host da requisição (Subdomínio Wildcard ou Domínio Customizado)
	hostDomain := strings.Split(c.Request.Host, ":")[0]
	mainDomain := os.Getenv("MAIN_DOMAIN")
	if mainDomain == "" {
		mainDomain = "deliverysistema.com.br"
	}

	// 3a. Verifica se é um subdomínio do domínio principal (ex: testejr.topautomacaojr.top)
	if hostDomain != mainDomain && strings.HasSuffix(hostDomain, "."+mainDomain) {
		subdomain := strings.TrimSuffix(hostDomain, "."+mainDomain)
		parts := strings.Split(subdomain, ".")
		slug := parts[len(parts)-1]
		if err := db.DB.Where("slug = ? AND ativo = ?", slug, true).First(&tenant).Error; err != nil {
			return nil, err
		}
		return &tenant, nil
	}

	// 3b. Caso contrário, busca por domínio próprio (ex: www.pizzariadojoao.com.br)
	if err := db.DB.Where("domain = ? AND ativo = ?", hostDomain, true).First(&tenant).Error; err != nil {
		return nil, err
	}

	return &tenant, nil
}
