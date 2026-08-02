package handlers

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"

	"cardapio-online/internal/db"
	"cardapio-online/internal/middleware"
	"cardapio-online/internal/models"

	"github.com/gin-gonic/gin"
)

type TenantHandler struct{}

func NewTenantHandler() *TenantHandler {
	return &TenantHandler{}
}

func hashPassword(password string) string {
	hash := sha256.Sum256([]byte(password))
	return hex.EncodeToString(hash[:])
}

type RegistrarInput struct {
	Nome     string `json:"nome" binding:"required"`
	Slug     string `json:"slug" binding:"required"`
	Email    string `json:"email" binding:"required,email"`
	Password string `json:"password" binding:"required,min=6"`
}

// Registrar cria um novo tenant no SaaS e o respectivo usuário administrador
func (h *TenantHandler) Registrar(c *gin.Context) {
	var input RegistrarInput
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Dados inválidos: " + err.Error()})
		return
	}

	slug := strings.ToLower(strings.TrimSpace(input.Slug))
	if slug == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "O slug do restaurante é obrigatório"})
		return
	}

	email := strings.ToLower(strings.TrimSpace(input.Email))

	// Valida se o slug já está em uso
	var slugCount int64
	db.DB.Model(&models.Tenant{}).Where("slug = ?", slug).Count(&slugCount)
	if slugCount > 0 {
		c.JSON(http.StatusConflict, gin.H{"error": "Este slug de URL já está em uso por outro restaurante"})
		return
	}

	// Valida se o e-mail já está em uso
	var emailCount int64
	db.DB.Model(&models.Usuario{}).Where("email = ?", email).Count(&emailCount)
	if emailCount > 0 {
		c.JSON(http.StatusConflict, gin.H{"error": "Este e-mail de usuário já está cadastrado"})
		return
	}

	// Transação para garantir criação do tenant e usuário atomicamente
	tx := db.DB.Begin()
	defer func() {
		if r := recover(); r != nil {
			tx.Rollback()
		}
	}()

	tenant := models.Tenant{
		Nome:               input.Nome,
		Slug:               slug,
		Ativo:              true,
		SenhaHash:          hashPassword(input.Password), // mantido para retrocompatibilidade
		PixAtivo:           false,
		CartaoCreditoAtivo: false,
		CartaoDebitoAtivo:  false,
		DinheiroAtivo:      true,
	}

	if err := tx.Create(&tenant).Error; err != nil {
		tx.Rollback()
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Erro ao registrar restaurante: " + err.Error()})
		return
	}

	usuario := models.Usuario{
		TenantID:  tenant.ID,
		Nome:      "Proprietário " + input.Nome,
		Email:     email,
		SenhaHash: hashPassword(input.Password),
		Role:      "owner",
		Ativo:     true,
	}

	if err := tx.Create(&usuario).Error; err != nil {
		tx.Rollback()
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Erro ao registrar usuário administrador: " + err.Error()})
		return
	}

	if err := tx.Commit().Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Erro ao confirmar cadastro: " + err.Error()})
		return
	}

	// Gera o arquivo de rotas dinâmicas do Traefik para o novo inquilino
	SaveTenantTraefikConfig(&tenant)

	c.JSON(http.StatusCreated, gin.H{
		"message": "Restaurante registrado com sucesso!",
		"tenant":  tenant,
		"token":   fmt.Sprintf("admin_user_%d_%d", tenant.ID, usuario.ID),
		"nome":    usuario.Nome,
		"email":   usuario.Email,
		"role":    usuario.Role,
	})
}

type LoginInput struct {
	Identifier string `json:"identifier" binding:"required"` // Aceita email ou slug do tenant
	Password   string `json:"password" binding:"required"`
}

// Login autentica o usuário e retorna o token de acesso administrativo correspondente
func (h *TenantHandler) Login(c *gin.Context) {
	var input LoginInput
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Dados inválidos: " + err.Error()})
		return
	}

	var usuario models.Usuario
	var tenant models.Tenant
	var err error

	identifier := strings.ToLower(strings.TrimSpace(input.Identifier))

	if strings.Contains(identifier, "@") {
		// Busca por E-mail do usuário
		err = db.DB.Where("email = ? AND ativo = ?", identifier, true).First(&usuario).Error
		if err == nil {
			err = db.DB.First(&tenant, usuario.TenantID).Error
		}
	} else {
		// Busca por Slug do restaurante (compatibilidade legada)
		err = db.DB.Where("slug = ? AND ativo = ?", identifier, true).First(&tenant).Error
		if err == nil {
			err = db.DB.Where("tenant_id = ? AND ativo = ?", tenant.ID, true).Order("role = 'owner' desc, id asc").First(&usuario).Error
		}
	}

	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Credenciais incorretas ou restaurante inativo"})
		return
	}

	if hashPassword(input.Password) != usuario.SenhaHash {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Credenciais incorretas ou restaurante inativo"})
		return
	}

	// Gera o token no formato admin_user_<tenant_id>_<user_id>
	token := fmt.Sprintf("admin_user_%d_%d", tenant.ID, usuario.ID)

	c.JSON(http.StatusOK, gin.H{
		"token": token,
		"nome":  usuario.Nome,
		"email": usuario.Email,
		"role":  usuario.Role,
		"slug":  tenant.Slug,
	})
}

// GetConfig retorna as configurações do tenant autenticado
func (h *TenantHandler) GetConfig(c *gin.Context) {
	tenantID := middleware.GetTenantID(c)
	var tenant models.Tenant
	if err := db.DB.First(&tenant, tenantID).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Restaurante não localizado"})
		return
	}

	mainDomain := os.Getenv("MAIN_DOMAIN")
	if mainDomain == "" {
		mainDomain = "deliverysistema.com.br"
	}

	c.JSON(http.StatusOK, gin.H{
		"nome":                 tenant.Nome,
		"slug":                 tenant.Slug,
		"domain":               tenant.Domain,
		"pix_ativo":            tenant.PixAtivo,
		"pix_chave":            tenant.PixChave,
		"cartao_credito_ativo": tenant.CartaoCreditoAtivo,
		"cartao_debito_ativo":  tenant.CartaoDebitoAtivo,
		"dinheiro_ativo":      tenant.DinheiroAtivo,
		"main_domain":          mainDomain,
	})
}

type UpdateConfigInput struct {
	Nome               string  `json:"nome"`
	Domain             *string `json:"domain"`
	PixAtivo           bool    `json:"pix_ativo"`
	PixChave           string  `json:"pix_chave"`
	CartaoCreditoAtivo bool    `json:"cartao_credito_ativo"`
	CartaoDebitoAtivo  bool    `json:"cartao_debito_ativo"`
	DinheiroAtivo      bool    `json:"dinheiro_ativo"`
}

// UpdateConfig atualiza as configurações do tenant
func (h *TenantHandler) UpdateConfig(c *gin.Context) {
	tenantID := middleware.GetTenantID(c)
	var tenant models.Tenant
	if err := db.DB.First(&tenant, tenantID).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Restaurante não localizado"})
		return
	}

	var input UpdateConfigInput
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Dados inválidos: " + err.Error()})
		return
	}

	// Atualiza os dados
	if input.Nome != "" {
		tenant.Nome = input.Nome
	}

	// Trata o domínio personalizado
	if input.Domain != nil {
		domainTrim := strings.ToLower(strings.TrimSpace(*input.Domain))
		if domainTrim == "" {
			tenant.Domain = nil
		} else {
			// Verifica se já não há outro tenant usando o mesmo domínio
			var count int64
			db.DB.Model(&models.Tenant{}).Where("domain = ? AND id != ?", domainTrim, tenant.ID).Count(&count)
			if count > 0 {
				c.JSON(http.StatusConflict, gin.H{"error": "Este domínio personalizado já está configurado por outro restaurante"})
				return
			}
			tenant.Domain = &domainTrim
		}
	}

	tenant.PixAtivo = input.PixAtivo
	tenant.PixChave = input.PixChave
	tenant.CartaoCreditoAtivo = input.CartaoCreditoAtivo
	tenant.CartaoDebitoAtivo = input.CartaoDebitoAtivo
	tenant.DinheiroAtivo = input.DinheiroAtivo

	if err := db.DB.Save(&tenant).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Erro ao atualizar configurações: " + err.Error()})
		return
	}

	// Atualiza o arquivo de rotas dinâmicas do Traefik com o novo domínio
	SaveTenantTraefikConfig(&tenant)

	c.JSON(http.StatusOK, gin.H{
		"message": "Configurações salvas com sucesso!",
		"config":  input,
	})
}

// DetectTenant identifica o restaurante pelo domínio Host (incluindo subdomínios wildcard)
func (h *TenantHandler) DetectTenant(c *gin.Context) {
	hostDomain := strings.Split(c.Request.Host, ":")[0]
	mainDomain := os.Getenv("MAIN_DOMAIN")
	if mainDomain == "" {
		mainDomain = "deliverysistema.com.br"
	}

	var tenant models.Tenant
	var resolved bool

	if hostDomain != "" && hostDomain != "localhost" && hostDomain != "127.0.0.1" {
		// 1. Verifica se é um subdomínio do domínio principal (ex: testejr.topautomacaojr.top)
		if hostDomain != mainDomain && strings.HasSuffix(hostDomain, "."+mainDomain) {
			subdomain := strings.TrimSuffix(hostDomain, "."+mainDomain)
			parts := strings.Split(subdomain, ".")
			slug := parts[len(parts)-1]
			if err := db.DB.Where("slug = ? AND ativo = ?", slug, true).First(&tenant).Error; err == nil {
				resolved = true
			}
		} else {
			// 2. Busca por domínio próprio (ex: www.pizzariadojoao.com.br)
			if err := db.DB.Where("domain = ? AND ativo = ?", hostDomain, true).First(&tenant).Error; err == nil {
				resolved = true
			}
		}
	}

	if !resolved {
		c.JSON(http.StatusOK, gin.H{
			"status":  "main_domain",
			"message": "Nenhum restaurante resolvido para este domínio",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"status": "resolved",
		"tenant": gin.H{
			"id":     tenant.ID,
			"nome":   tenant.Nome,
			"slug":   tenant.Slug,
			"domain": tenant.Domain,
		},
	})
}

// ListUsuarios lista todos os usuários vinculados ao tenant
func (h *TenantHandler) ListUsuarios(c *gin.Context) {
	tenantID := middleware.GetTenantID(c)
	var usuarios []models.Usuario
	if err := db.DB.Where("tenant_id = ?", tenantID).Order("id asc").Find(&usuarios).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Erro ao buscar usuários: " + err.Error()})
		return
	}
	c.JSON(http.StatusOK, usuarios)
}

type CreateUsuarioInput struct {
	Nome     string `json:"nome" binding:"required"`
	Email    string `json:"email" binding:"required,email"`
	Password string `json:"password" binding:"required,min=6"`
	Role     string `json:"role" binding:"required"`
}

// CreateUsuario cria um novo usuário no escopo do tenant atual
func (h *TenantHandler) CreateUsuario(c *gin.Context) {
	tenantID := middleware.GetTenantID(c)
	var input CreateUsuarioInput
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Dados inválidos: " + err.Error()})
		return
	}

	email := strings.ToLower(strings.TrimSpace(input.Email))
	var count int64
	db.DB.Model(&models.Usuario{}).Where("email = ?", email).Count(&count)
	if count > 0 {
		c.JSON(http.StatusConflict, gin.H{"error": "Este e-mail já está em uso por outro usuário"})
		return
	}

	usuario := models.Usuario{
		TenantID:  tenantID,
		Nome:      input.Nome,
		Email:     email,
		SenhaHash: hashPassword(input.Password),
		Role:      input.Role,
		Ativo:     true,
	}

	if err := db.DB.Create(&usuario).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Erro ao cadastrar usuário: " + err.Error()})
		return
	}

	c.JSON(http.StatusCreated, usuario)
}

// DeleteUsuario exclui um usuário (exceto a si próprio ou o proprietário principal)
func (h *TenantHandler) DeleteUsuario(c *gin.Context) {
	tenantID := middleware.GetTenantID(c)
	var currentUserID uint

	userIDVal, exists := c.Get("user_id")
	if exists {
		if val, ok := userIDVal.(uint); ok {
			currentUserID = val
		}
	}

	targetUserIDStr := c.Param("id")
	targetUserID, err := strconv.ParseUint(targetUserIDStr, 10, 32)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "ID de usuário inválido"})
		return
	}

	if uint(targetUserID) == currentUserID {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Não é permitido excluir o próprio usuário conectado"})
		return
	}

	var usuario models.Usuario
	if err := db.DB.Where("id = ? AND tenant_id = ?", targetUserID, tenantID).First(&usuario).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Usuário não encontrado"})
		return
	}

	if usuario.Role == "owner" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "O usuário proprietário original ('owner') não pode ser excluído"})
		return
	}

	if err := db.DB.Delete(&usuario).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Erro ao remover usuário: " + err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Usuário removido com sucesso!"})
}

// CheckDNS valida o apontamento CNAME ou A do domínio informado
func (h *TenantHandler) CheckDNS(c *gin.Context) {
	domain := c.Query("domain")
	domain = strings.ToLower(strings.TrimSpace(domain))
	if domain == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "O domínio é obrigatório"})
		return
	}

	mainDomain := os.Getenv("MAIN_DOMAIN")
	if mainDomain == "" {
		mainDomain = "deliverysistema.com.br"
	}

	success := false
	// 1. Tenta resolver o IP do domínio informado
	ips, err := net.LookupIP(domain)
	if err == nil {
		// Resolve o IP do domínio principal
		mainIPs, errMain := net.LookupIP(mainDomain)
		if errMain == nil {
			for _, ip := range ips {
				for _, mIP := range mainIPs {
					if ip.Equal(mIP) {
						success = true
						break
					}
				}
				if success {
					break
				}
			}
		}
	}

	// 2. Se IP não bater, tenta verificar se CNAME aponta para o domínio principal
	if !success {
		cname, errCname := net.LookupCNAME(domain)
		if errCname == nil {
			cname = strings.TrimSuffix(strings.ToLower(cname), ".")
			if strings.Contains(cname, mainDomain) {
				success = true
			}
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"domain":      domain,
		"configured":  success,
		"main_domain": mainDomain,
	})
}

// SaveTenantTraefikConfig grava a rota dinâmica do Traefik para o tenant
func SaveTenantTraefikConfig(tenant *models.Tenant) {
	dirPath := "/traefik_dynamic"
	mainDomain := os.Getenv("MAIN_DOMAIN")
	if mainDomain == "" {
		mainDomain = "deliverysistema.com.br"
	}

	rule := fmt.Sprintf("Host(`%s.%s`) || Host(`www.%s.%s`)", tenant.Slug, mainDomain, tenant.Slug, mainDomain)
	if tenant.Domain != nil && *tenant.Domain != "" {
		rule += fmt.Sprintf(" || Host(`%s`)", *tenant.Domain)
	}

	config := fmt.Sprintf(`http:
  routers:
    router-%[1]s:
      rule: "%[2]s"
      entryPoints:
        - websecure
      tls:
        certResolver: myresolver
      service: service-%[1]s

  services:
    service-%[1]s:
      loadBalancer:
        servers:
          - url: "http://cardapio_online_api:8081"
`, tenant.Slug, rule)

	filePath := fmt.Sprintf("%s/%s.yml", dirPath, tenant.Slug)
	if err := os.WriteFile(filePath, []byte(config), 0644); err != nil {
		fmt.Printf("Erro ao gravar arquivo de configuração do Traefik para tenant %s: %v\n", tenant.Slug, err)
	}
}

// DeleteTenantTraefikConfig remove a rota dinâmica do Traefik do tenant
func DeleteTenantTraefikConfig(slug string) {
	dirPath := "/traefik_dynamic"
	filePath := fmt.Sprintf("%s/%s.yml", dirPath, slug)
	if err := os.Remove(filePath); err != nil && !os.IsNotExist(err) {
		fmt.Printf("Erro ao remover arquivo de configuração do Traefik para tenant %s: %v\n", slug, err)
	}
}

// SyncTraefikConfigs gera os arquivos dinâmicos do Traefik com base no BD ativo
func SyncTraefikConfigs() {
	var tenants []models.Tenant
	if err := db.DB.Where("ativo = ?", true).Find(&tenants).Error; err != nil {
		fmt.Printf("Erro ao buscar inquilinos para sincronização do Traefik: %v\n", err)
		return
	}

	dirPath := "/traefik_dynamic"
	if _, err := os.Stat(dirPath); os.IsNotExist(err) {
		os.MkdirAll(dirPath, 0755)
	}

	mainDomain := os.Getenv("MAIN_DOMAIN")
	if mainDomain == "" {
		mainDomain = "deliverysistema.com.br"
	}

	// 1. Escreve a rota default para o domínio principal
	defaultConfig := fmt.Sprintf(`http:
  routers:
    router-main:
      rule: "Host(` + "`" + `%[1]s` + "`" + `) || Host(` + "`" + `www.%[1]s` + "`" + `)"
      entryPoints:
        - websecure
      tls:
        certResolver: myresolver
      service: service-main

  services:
    service-main:
      loadBalancer:
        servers:
          - url: "http://cardapio_online_api:8081"
`, mainDomain)

	defaultFilePath := fmt.Sprintf("%s/default.yml", dirPath)
	if err := os.WriteFile(defaultFilePath, []byte(defaultConfig), 0644); err != nil {
		fmt.Printf("Erro ao escrever default.yml do Traefik: %v\n", err)
	}

	// 2. Escreve as configurações dinâmicas dos inquilinos
	for _, tenant := range tenants {
		SaveTenantTraefikConfig(&tenant)
	}
}
