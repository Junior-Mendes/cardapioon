package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"cardapio-online/internal/auth"
	"cardapio-online/internal/config"
	"cardapio-online/internal/db"
	"cardapio-online/internal/handlers"
	"cardapio-online/internal/mail"
	"cardapio-online/internal/middleware"
	"cardapio-online/internal/models"
	"cardapio-online/internal/traefik"

	"github.com/gin-gonic/gin"
	"github.com/joho/godotenv"
	"gorm.io/gorm"
)

func main() {
	if err := executar(); err != nil {
		slog.Error("falha fatal no arranque", "erro", err)
		os.Exit(1)
	}
}

func executar() error {
	_ = godotenv.Load()

	cfg, err := config.LoadConfig()
	if err != nil {
		// O logger estruturado ainda não está configurado; escrevemos directamente.
		return err
	}

	configurarLogger(cfg)

	slog.Info("a iniciar Cardápio Online",
		"modo", cfg.GinMode, "dominio", cfg.MainDomain, "porta", cfg.Port)

	gdb, err := db.Init(cfg)
	if err != nil {
		return err
	}
	sqlDB, err := gdb.DB()
	if err != nil {
		return err
	}
	defer sqlDB.Close()

	if err := os.MkdirAll(cfg.UploadDir, 0o755); err != nil {
		slog.Warn("não foi possível criar o directório de uploads", "dir", cfg.UploadDir, "erro", err)
	}

	tokens, err := auth.NewTokenService(cfg.JWTSecret, cfg.AccessTTL, cfg.RefreshTTL)
	if err != nil {
		return err
	}

	resolver := middleware.NewTenantResolver(gdb, cfg.MainDomain)

	h := handlers.New(&handlers.Deps{
		DB:     gdb,
		Cfg:    cfg,
		Tokens: tokens,
		Mailer: mail.New(mail.Config{
			Host: cfg.SMTPHost, Port: cfg.SMTPPort,
			User: cfg.SMTPUser, Password: cfg.SMTPPassword,
			From: cfg.MailFrom, FromName: cfg.MailFromName,
		}),
		Traefik:  traefik.NewWriter(cfg.TraefikDynamicDir, cfg.MainDomain, cfg.BackendURL),
		Resolver: resolver,
	})

	if err := h.SincronizarRotasTraefik(); err != nil {
		// Uma falha aqui deixa clientes sem rota, mas o servidor ainda serve o domínio
		// principal; registamos e continuamos.
		slog.Error("falha ao sincronizar rotas do Traefik", "erro", err)
	}

	iniciarLimpezaPeriodica(gdb)

	router := construirRouter(cfg, h, resolver, gdb)

	servidor := &http.Server{
		Addr:    ":" + cfg.Port,
		Handler: router,
		// Sem timeouts, uma ligação lenta ocupa um worker indefinidamente.
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	return servirComShutdownGracioso(servidor)
}

func configurarLogger(cfg *config.Config) {
	nivel := slog.LevelInfo
	if cfg.DevMode() {
		nivel = slog.LevelDebug
	}

	var handler slog.Handler
	if cfg.DevMode() {
		handler = slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: nivel})
	} else {
		// JSON em produção: pesquisável por tenant_id e request_id em qualquer
		// agregador de logs.
		handler = slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: nivel})
	}
	slog.SetDefault(slog.New(handler))
}

func construirRouter(
	cfg *config.Config,
	h *handlers.Handler,
	resolver *middleware.TenantResolver,
	gdb *gorm.DB,
) *gin.Engine {
	gin.SetMode(cfg.GinMode)

	r := gin.New()
	// Limite de memória para formulários multipart. Acima disto o Gin escreve em disco
	// temporário em vez de manter tudo em RAM.
	r.MaxMultipartMemory = 4 << 20
	r.Use(gin.Recovery())
	r.Use(middleware.RequestLogger())

	// Sem esta configuração o Gin confia no X-Forwarded-For de qualquer origem, o que
	// permite falsificar o IP do cliente e contornar o rate limiting.
	if len(cfg.TrustedProxies) > 0 {
		if err := r.SetTrustedProxies(cfg.TrustedProxies); err != nil {
			slog.Error("TRUSTED_PROXIES inválido", "erro", err)
		}
	} else {
		_ = r.SetTrustedProxies(nil)
	}

	r.Use(middleware.CORS(middleware.CORSConfig{
		MainDomain:        cfg.MainDomain,
		PermitirLocalhost: cfg.DevMode(),
		DB:                gdb,
	}))
	r.Use(middleware.SecurityHeaders(cfg.DevMode()))
	r.Use(middleware.CacheControl())

	// Limites separados por sensibilidade: autenticação é o alvo de força-bruta, o
	// storefront tem tráfego legítimo muito superior.
	limiteAuth := middleware.NewRateLimiter(10, 5)
	limiteRegisto := middleware.NewRateLimiter(5, 3)
	limitePublico := middleware.NewRateLimiter(300, 60)
	limiteEncomenda := middleware.NewRateLimiter(20, 10)
	limiteDNS := middleware.NewRateLimiter(10, 5)
	// Upload é caro (descodificar e recodificar imagem): limite mais apertado.
	limiteUpload := middleware.NewRateLimiter(30, 10)

	r.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})
	r.GET("/ready", func(c *gin.Context) {
		if err := gdb.Exec("SELECT 1").Error; err != nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"status": "base de dados indisponível"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})

	r.Static("/static", "./static")
	r.StaticFile("/menu", "./static/menu.html")
	r.StaticFile("/pedido", "./static/order.html")
	r.StaticFile("/admin", "./static/admin.html")
	r.StaticFile("/redefinir-senha", "./static/reset.html")
	r.StaticFile("/privacidade", "./static/privacidade.html")

	// Raiz: landing page no domínio do SaaS, storefront nos domínios de clientes.
	r.GET("/", func(c *gin.Context) {
		if resolver.IsMainDomain(c.Request.Host) {
			c.File("./static/index.html")
			return
		}
		c.Redirect(http.StatusFound, "/menu")
	})

	api := r.Group("/api")
	{
		autenticacao := api.Group("/tenant")
		{
			autenticacao.POST("/registrar", limiteRegisto.Limit(), h.Registar)
			autenticacao.POST("/login", limiteAuth.LimitBy(chavePorIPeCorpo), h.Login)
			autenticacao.POST("/refresh", limiteAuth.Limit(), h.Refresh)
			autenticacao.POST("/logout", h.Logout)
			autenticacao.POST("/esqueci-senha", limiteRegisto.Limit(), h.EsqueciSenha)
			autenticacao.POST("/redefinir-senha", limiteRegisto.Limit(), h.RedefinirSenha)
			autenticacao.GET("/detect", limitePublico.Limit(), h.DetectTenant)
		}

		// Rastreio público por token opaco.
		api.GET("/encomendas/:token", limitePublico.Limit(), h.GetOrderPublico)

		// Storefront. O tenant é resolvido pelo Host ou pelo :slug, nunca por token.
		publico := api.Group("", limitePublico.Limit(), resolver.ResolvePublic())
		{
			publico.GET("/public-menu", h.GetPublicMenu)
			publico.POST("/pedidos", limiteEncomenda.Limit(), h.CreateOrder)
		}
		publicoComSlug := api.Group("/:slug", limitePublico.Limit(), resolver.ResolvePublic())
		{
			publicoComSlug.GET("/public-menu", h.GetPublicMenu)
			publicoComSlug.POST("/pedidos", limiteEncomenda.Limit(), h.CreateOrder)
		}
	}

	// Rotas administrativas: o tenant vem exclusivamente das claims do JWT.
	admin := r.Group("/api/admin", middleware.RequireAuth(h.Tokens))
	{
		admin.GET("/config", h.GetConfig)
		admin.POST("/conta/alterar-senha", h.AlterarSenha)

		// Configuração do restaurante e domínio: apenas owner e admin.
		gestao := admin.Group("", middleware.RequireRole(middleware.RoleAdmin))
		{
			gestao.PUT("/config", h.UpdateConfig)
			gestao.POST("/config/dominio", h.SolicitarDominio)
			gestao.POST("/config/dominio/verificar", h.VerificarDominio)
			gestao.GET("/config/check-dns", limiteDNS.Limit(), h.CheckDNS)
		}

		// Upload de imagens: quem pode editar o menu pode carregar imagens.
		admin.POST("/upload", middleware.RequireRole(middleware.RoleGerente),
			limiteUpload.Limit(), h.UploadImagem)

		// Cardápio: gerente e acima.
		cardapio := admin.Group("/cardapio", middleware.RequireRole(middleware.RoleGerente))
		{
			cardapio.GET("", h.GetMenu)
			cardapio.POST("", h.CreateMenuItem)
			cardapio.PUT("/:id", h.UpdateMenuItem)
			cardapio.DELETE("/:id", h.DeleteMenuItem)
		}

		// Encomendas: qualquer funcionário precisa de operar o balcão.
		encomendas := admin.Group("/pedidos", middleware.RequireRole(middleware.RoleFuncionario))
		{
			encomendas.GET("", h.GetAdminOrders)
			encomendas.PUT("/:id/status", h.UpdateOrderStatus)
		}

		// Utilizadores: apenas owner e admin.
		usuarios := admin.Group("/usuarios", middleware.RequireRole(middleware.RoleAdmin))
		{
			usuarios.GET("", h.ListUsuarios)
			usuarios.POST("", h.CreateUsuario)
			usuarios.DELETE("/:id", h.DeleteUsuario)
		}
	}

	return r
}

// chavePorIPeCorpo limita o login por IP e por conta em simultâneo, para que a rotação de
// IPs não permita força-bruta a uma conta específica.
func chavePorIPeCorpo(c *gin.Context) string {
	return c.ClientIP() + "|" + c.GetHeader("X-Login-Hint")
}

// iniciarLimpezaPeriodica remove tokens expirados.
//
// Sem isto as tabelas refresh_tokens e password_resets crescem indefinidamente.
func iniciarLimpezaPeriodica(gdb *gorm.DB) {
	go func() {
		t := time.NewTicker(6 * time.Hour)
		defer t.Stop()
		for {
			agora := time.Now()
			if err := gdb.Where("expires_at < ?", agora.Add(-24*time.Hour)).
				Delete(&models.RefreshToken{}).Error; err != nil {
				slog.Error("falha ao limpar refresh tokens expirados", "erro", err)
			}
			if err := gdb.Where("expires_at < ?", agora.Add(-24*time.Hour)).
				Delete(&models.PasswordReset{}).Error; err != nil {
				slog.Error("falha ao limpar pedidos de reset expirados", "erro", err)
			}
			<-t.C
		}
	}()
}

// servirComShutdownGracioso aceita tráfego até receber SIGTERM/SIGINT e depois espera que
// os pedidos em curso terminem.
//
// Sem isto, um deploy corta encomendas a meio da transacção.
func servirComShutdownGracioso(s *http.Server) error {
	erros := make(chan error, 1)

	go func() {
		slog.Info("servidor a escutar", "endereco", s.Addr)
		if err := s.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			erros <- err
		}
	}()

	paragem := make(chan os.Signal, 1)
	signal.Notify(paragem, os.Interrupt, syscall.SIGTERM)

	select {
	case err := <-erros:
		return err
	case sig := <-paragem:
		slog.Info("sinal de paragem recebido; a encerrar", "sinal", sig.String())
	}

	ctx, cancelar := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancelar()

	if err := s.Shutdown(ctx); err != nil {
		return err
	}
	slog.Info("servidor encerrado")
	return nil
}
