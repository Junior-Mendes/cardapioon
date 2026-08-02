package main

import (
	"log"
	"net/http"
	"os"
	"strings"

	"cardapio-online/internal/config"
	"cardapio-online/internal/db"
	"cardapio-online/internal/handlers"
	"cardapio-online/internal/middleware"

	"github.com/gin-gonic/gin"
	"github.com/joho/godotenv"
)

func main() {
	// Carrega arquivo .env local se existir
	if err := godotenv.Load(); err != nil {
		log.Println("Aviso: arquivo .env não encontrado. Usando variáveis de ambiente do sistema.")
	}

	// 1. Carrega Configurações
	cfg := config.LoadConfig()

	// 2. Inicializa Conexão com o MySQL e roda Migrações (AutoMigrate + Seeders)
	database := db.InitDB(cfg)
	sqlDB, err := database.DB()
	if err == nil {
		defer sqlDB.Close()
	}

	// Garante que a pasta de uploads de imagens exista
	if err := os.MkdirAll("./static/uploads", 0755); err != nil {
		log.Printf("Aviso: falha ao criar diretório de uploads: %v", err)
	}

	// 3. Inicializa Handlers
	tenantHandler := handlers.NewTenantHandler()
	menuHandler := handlers.NewMenuHandler()
	orderHandler := handlers.NewOrderHandler()

	// Sincroniza arquivos de rotas do Traefik baseado nos inquilinos ativos
	handlers.SyncTraefikConfigs()

	// 4. Configura Roteador Gin
	r := gin.Default()

	// Middleware de CORS e controle de cache
	r.Use(func(c *gin.Context) {
		c.Writer.Header().Set("Access-Control-Allow-Origin", "*")
		c.Writer.Header().Set("Access-Control-Allow-Credentials", "true")
		c.Writer.Header().Set("Access-Control-Allow-Headers", "Content-Type, Content-Length, Accept-Encoding, X-CSRF-Token, Authorization, accept, origin, Cache-Control, X-Requested-With, X-Tenant-Slug")
		c.Writer.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS, GET, PUT, DELETE")
		
		// Desativa cache no navegador para evitar travamento do frontend
		c.Writer.Header().Set("Cache-Control", "no-store, no-cache, must-revalidate, max-age=0")
		c.Writer.Header().Set("Pragma", "no-cache")
		c.Writer.Header().Set("Expires", "0")

		if c.Request.Method == "OPTIONS" {
			c.AbortWithStatus(204)
			return
		}
		c.Next()
	})

	// Rota de Health Check
	r.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "healthy"})
	})

	// Servir Arquivos Estáticos
	r.Static("/static", "./static")
	// Rota Raiz (Landing Page no Domínio Principal, Redirecionamento para /menu nos subdomínios)
	r.GET("/", func(c *gin.Context) {
		hostDomain := strings.Split(c.Request.Host, ":")[0]
		mainDomain := os.Getenv("MAIN_DOMAIN")
		if mainDomain == "" {
			mainDomain = "deliverysistema.com.br"
		}

		isRootDomain := hostDomain == mainDomain || 
			hostDomain == "www."+mainDomain || 
			hostDomain == "localhost" || 
			hostDomain == "127.0.0.1"

		if !isRootDomain {
			// Se for subdomínio ou domínio próprio do restaurante, vai direto pro cardápio
			c.Redirect(http.StatusMovedPermanently, "/menu")
			return
		}

		// Se for o domínio raiz do SaaS, serve a landing page oficial
		c.File("./static/index.html")
	})
	r.StaticFile("/menu", "./static/menu.html")
	r.StaticFile("/pedido", "./static/order.html")
	r.StaticFile("/admin", "./static/admin.html")

	// --- ROTAS DA API ---

	// Rotas Públicas (Sem necessidade de autenticação)
	api := r.Group("/api")
	{
		// Rotas de Tenant (Autenticação SaaS)
		api.POST("/tenant/registrar", tenantHandler.Registrar)
		api.POST("/tenant/login", tenantHandler.Login)
		api.GET("/tenant/detect", tenantHandler.DetectTenant)

		// Rastreamento público de pedidos
		api.GET("/pedidos/:id", orderHandler.GetOrder)

		// Consultas e Checkout Públicas escopo do cardápio do restaurante (:slug ou por Domínio)
		api.GET("/:slug/public-menu", menuHandler.GetPublicMenu)
		api.POST("/:slug/pedidos", orderHandler.CreateOrder)
		api.GET("/public-menu", menuHandler.GetPublicMenu)
		api.POST("/pedidos", orderHandler.CreateOrder)
	}

	// Rotas Privadas (Lojista - Requer header Authorization: admin_user_<tenant_id>_<user_id>)
	admin := r.Group("/api/admin")
	admin.Use(middleware.TenantContext())
	{
		// Configurações do Restaurante (Nome, Domínio, Pagamento)
		admin.GET("/config", tenantHandler.GetConfig)
		admin.PUT("/config", tenantHandler.UpdateConfig)
		admin.GET("/config/check-dns", tenantHandler.CheckDNS)

		// Gestão de Cardápio
		admin.GET("/cardapio", menuHandler.GetMenu)
		admin.POST("/cardapio", menuHandler.CreateMenuItem)
		admin.PUT("/cardapio/:id", menuHandler.UpdateMenuItem)
		admin.DELETE("/cardapio/:id", menuHandler.DeleteMenuItem)

		// Gestão de Pedidos do Lojista
		admin.GET("/pedidos", orderHandler.GetAdminOrders)
		admin.PUT("/pedidos/:id/status", orderHandler.UpdateOrderStatus)

		// Gestão de Usuários
		admin.GET("/usuarios", tenantHandler.ListUsuarios)
		admin.POST("/usuarios", tenantHandler.CreateUsuario)
		admin.DELETE("/usuarios/:id", tenantHandler.DeleteUsuario)
	}

	// Porta padrão 8081 para evitar conflito com a 8080 do CRM
	port := ":" + cfg.Port
	log.Printf("Servidor de cardápios online iniciado na porta %s...", port)
	if err := r.Run(port); err != nil {
		log.Fatalf("Erro ao iniciar o servidor: %v", err)
	}
}
