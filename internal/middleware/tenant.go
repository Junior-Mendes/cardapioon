package middleware

import (
	"fmt"
	"net/http"
	"os"
	"strings"

	"cardapio-online/internal/db"
	"cardapio-online/internal/models"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// TenantContext intercepta requisições e identifica o tenant atual
func TenantContext() gin.HandlerFunc {
	return func(c *gin.Context) {
		var tenantID uint
		var tenantSlug string

		// 0. Identifica por Host/DNS (Domínio Personalizado ou Subdomínio Wildcard)
		hostDomain := strings.Split(c.Request.Host, ":")[0]
		mainDomain := os.Getenv("MAIN_DOMAIN")
		if mainDomain == "" {
			mainDomain = "deliverysistema.com.br"
		}

		if hostDomain != "" && hostDomain != "localhost" && hostDomain != "127.0.0.1" {
			// Se o host termina com o domínio principal (ex: pizzaria.deliverysistema.com.br)
			if hostDomain != mainDomain && strings.HasSuffix(hostDomain, "."+mainDomain) {
				subdomain := strings.TrimSuffix(hostDomain, "."+mainDomain)
				parts := strings.Split(subdomain, ".")
				tenantSlug = parts[len(parts)-1]
			} else {
				// Domínio próprio do cliente (ex: pizzariadojoao.com)
				var tenant models.Tenant
				if err := db.DB.Where("domain = ? AND ativo = ?", hostDomain, true).First(&tenant).Error; err == nil {
					tenantID = tenant.ID
					tenantSlug = tenant.Slug
				}
			}
		}

		// 1. Identifica por parâmetro de query (útil para o cardápio público: ?tenant=slug)
		if tenantID == 0 {
			tenantSlug = c.Query("tenant")
		}

		// 2. Se vazio, tenta identificar por parâmetro de rota (ex: /api/:slug/public-menu)
		if tenantID == 0 && tenantSlug == "" {
			tenantSlug = c.Param("slug")
		}

		// 3. Se vazio, tenta identificar por cabeçalho personalizado
		if tenantID == 0 && tenantSlug == "" {
			tenantSlug = c.GetHeader("X-Tenant-Slug")
		}

		// 4. Se identificou por slug, busca o tenant no banco
		if tenantID == 0 && tenantSlug != "" {
			var tenant models.Tenant
			if err := db.DB.Where("slug = ? AND ativo = ?", tenantSlug, true).First(&tenant).Error; err == nil {
				tenantID = tenant.ID
			}
		}

		// 5. Se ainda não achou o tenant_id ou se já achou e quer recuperar o usuário, tenta extrair do token de autorização
		token := c.GetHeader("Authorization")
		if token != "" {
			// Formato do Token Admin: admin_user_<tenant_id>_<user_id> ou admin_tenant_<tenant_id>
			if strings.HasPrefix(token, "admin_user_") {
				var tID, uID uint
				if _, err := fmt.Sscanf(token, "admin_user_%d_%d", &tID, &uID); err == nil {
					if tenantID == 0 {
						tenantID = tID
					}
					c.Set("user_id", uID)
				}
			} else if strings.HasPrefix(token, "admin_tenant_") {
				var id uint
				if _, err := fmt.Sscanf(token, "admin_tenant_%d", &id); err == nil {
					if tenantID == 0 {
						tenantID = id
					}
				}
			}
		}

		// Rotas públicas que não requerem escopo de tenant específico
		path := c.Request.URL.Path
		if tenantID == 0 && (path == "/health" || strings.HasPrefix(path, "/api/tenant/registrar") || strings.HasPrefix(path, "/api/tenant/login") || strings.HasPrefix(path, "/api/tenant/detect")) {
			c.Next()
			return
		}

		// Se o tenant não pôde ser identificado
		if tenantID == 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Restaurante não identificado ou inativo"})
			c.Abort()
			return
		}

		// Grava as informações identificadas no contexto do Gin
		c.Set("tenant_id", tenantID)
		if tenantSlug != "" {
			c.Set("tenant_slug", tenantSlug)
		} else {
			// Se resolveu por domínio, busca o slug para salvar no contexto
			var tenant models.Tenant
			if err := db.DB.First(&tenant, tenantID).Error; err == nil {
				c.Set("tenant_slug", tenant.Slug)
			}
		}
		c.Next()
	}
}

// GetTenantID recupera o tenant_id do contexto
func GetTenantID(c *gin.Context) uint {
	id, exists := c.Get("tenant_id")
	if !exists {
		return 0
	}
	val, ok := id.(uint)
	if !ok {
		return 0
	}
	return val
}

// TenantScope retorna o escopo do GORM para filtrar os dados por tenant_id
func TenantScope(c *gin.Context) func(db *gorm.DB) *gorm.DB {
	return func(db *gorm.DB) *gorm.DB {
		tenantID := GetTenantID(c)
		if tenantID == 0 {
			// Se o tenant_id não for informado, retorna um filtro falso para bloquear vazamento de dados
			return db.Where("1 = 0")
		}
		return db.Where("tenant_id = ?", tenantID)
	}
}
