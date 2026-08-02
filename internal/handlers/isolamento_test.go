package handlers_test

// Testes de isolamento de tenant.
//
// Este é o ficheiro mais importante da Fase 0. As duas falhas mais graves da versão
// anterior eram atravessáveis exactamente por aqui:
//
//   - C1: o token era a string "admin_user_<tenant>_<user>", que qualquer pessoa podia
//     escrever para assumir o painel de qualquer restaurante.
//   - C2: o tenant era resolvido pelo cabeçalho Host *antes* do token, pelo que um lojista
//     autenticado que abrisse o subdomínio de um concorrente operava na conta dele.
//
// Os testes correm contra um MySQL real (o GORM gera SQL específico do dialecto, e um
// SQLite em memória não provaria o mesmo). Exigem TEST_DB_* no ambiente; sem isso são
// ignorados, para que `go test ./...` não falhe em máquinas sem base de dados.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
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
	"github.com/google/uuid"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

const (
	dominioTeste = "teste.local"
	segredoTeste = "segredo-de-teste-com-mais-de-32-bytes-aqui!!"
)

type ambiente struct {
	router *gin.Engine
	gdb    *gorm.DB
	tokens *auth.TokenService

	// Dois tenants: A é o atacante autenticado, B é a vítima.
	tenantA, tenantB models.Tenant
	userA, userB     models.Usuario
	itemA, itemB     models.MenuItem
	pedidoA, pedidoB models.Pedido
}

func montarAmbiente(t *testing.T) *ambiente {
	t.Helper()

	host := os.Getenv("TEST_DB_HOST")
	if host == "" {
		t.Skip("TEST_DB_HOST não definido: testes de isolamento ignorados")
	}

	dsn := fmt.Sprintf("%s:%s@tcp(%s:%s)/%s?charset=utf8mb4&parseTime=True&loc=Local",
		os.Getenv("TEST_DB_USER"), os.Getenv("TEST_DB_PASSWORD"),
		host, envOu("TEST_DB_PORT", "3306"), envOu("TEST_DB_NAME", "cardapio_test"))

	gdb, err := gorm.Open(mysql.Open(dsn), &gorm.Config{
		Logger:                                   gormlogger.Default.LogMode(gormlogger.Silent),
		DisableForeignKeyConstraintWhenMigrating: true,
	})
	if err != nil {
		t.Fatalf("ligar à base de testes: %v", err)
	}

	limparBase(t, gdb)

	if err := db.RunMigrations(gdb); err != nil {
		t.Fatalf("migrações: %v", err)
	}

	cfg := &config.Config{
		MainDomain: dominioTeste,
		GinMode:    "release",
		JWTSecret:  segredoTeste,
		BaseURL:    "https://" + dominioTeste,
	}
	tokens, err := auth.NewTokenService(segredoTeste, time.Hour, 24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}

	resolver := middleware.NewTenantResolver(gdb, dominioTeste)
	h := handlers.New(&handlers.Deps{
		DB: gdb, Cfg: cfg, Tokens: tokens,
		Mailer:   mail.LogSender{},
		Traefik:  traefik.NewWriter(t.TempDir(), dominioTeste, "http://api:8081"),
		Resolver: resolver,
	})

	amb := &ambiente{gdb: gdb, tokens: tokens}
	amb.semear(t, gdb)
	amb.router = montarRotas(h, resolver, tokens)
	return amb
}

// montarRotas replica a árvore de rotas administrativas de cmd/api/main.go.
//
// Duplicação consciente: extrair a construção do router para um pacote partilhado é a
// alternativa correcta e está registada como dívida técnica.
//
// ATENÇÃO: enquanto a duplicação existir, qualquer rota nova acrescentada em main.go tem
// de ser acrescentada aqui também — caso contrário fica sem cobertura de isolamento sem
// que nada falhe.
func montarRotas(h *handlers.Handler, resolver *middleware.TenantResolver, tokens *auth.TokenService) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()

	api := r.Group("/api")
	api.GET("/encomendas/:token", h.GetOrderPublico)
	publico := api.Group("", resolver.ResolvePublic())
	publico.GET("/public-menu", h.GetPublicMenu)
	publico.POST("/pedidos", h.CreateOrder)

	admin := r.Group("/api/admin", middleware.RequireAuth(tokens))
	{
		admin.GET("/config", h.GetConfig)
		admin.POST("/conta/alterar-senha", h.AlterarSenha)

		gestao := admin.Group("", middleware.RequireRole(middleware.RoleAdmin))
		gestao.PUT("/config", h.UpdateConfig)
		gestao.POST("/config/dominio", h.SolicitarDominio)
		gestao.POST("/config/dominio/verificar", h.VerificarDominio)
		gestao.GET("/config/check-dns", h.CheckDNS)

		cardapio := admin.Group("/cardapio", middleware.RequireRole(middleware.RoleGerente))
		cardapio.GET("", h.GetMenu)
		cardapio.POST("", h.CreateMenuItem)
		cardapio.PUT("/:id", h.UpdateMenuItem)
		cardapio.DELETE("/:id", h.DeleteMenuItem)

		encomendas := admin.Group("/pedidos", middleware.RequireRole(middleware.RoleFuncionario))
		encomendas.GET("", h.GetAdminOrders)
		encomendas.PUT("/:id/status", h.UpdateOrderStatus)

		usuarios := admin.Group("/usuarios", middleware.RequireRole(middleware.RoleAdmin))
		usuarios.GET("", h.ListUsuarios)
		usuarios.POST("", h.CreateUsuario)
		usuarios.DELETE("/:id", h.DeleteUsuario)
	}
	return r
}

func limparBase(t *testing.T, gdb *gorm.DB) {
	t.Helper()
	// Ordem inversa das dependências.
	tabelas := []string{
		"audit_logs", "refresh_tokens", "password_resets",
		"itens_pedido", "pedidos", "menu_items", "usuarios", "tenants",
		"schema_migrations",
	}
	gdb.Exec("SET FOREIGN_KEY_CHECKS = 0")
	for _, tabela := range tabelas {
		gdb.Exec("DROP TABLE IF EXISTS " + tabela)
	}
	gdb.Exec("SET FOREIGN_KEY_CHECKS = 1")
}

func (a *ambiente) semear(t *testing.T, gdb *gorm.DB) {
	t.Helper()
	agora := time.Now()
	hash, err := auth.HashPassword("SenhaTeste123")
	if err != nil {
		t.Fatal(err)
	}

	criar := func(nome, slug string) (models.Tenant, models.Usuario, models.MenuItem, models.Pedido) {
		tenant := models.Tenant{
			Nome: nome, Slug: slug, Ativo: true, SenhaHash: hash,
			DomainStatus: models.DomainNenhum, DinheiroAtivo: true, CartaoDebitoAtivo: true,
		}
		if err := gdb.Create(&tenant).Error; err != nil {
			t.Fatalf("criar tenant %s: %v", slug, err)
		}

		usuario := models.Usuario{
			TenantID: tenant.ID, Nome: "Dono " + nome,
			Email: slug + "@teste.local", SenhaHash: hash,
			EmailVerifiedAt: &agora, Role: models.RoleOwner, Ativo: true,
		}
		if err := gdb.Create(&usuario).Error; err != nil {
			t.Fatalf("criar utilizador %s: %v", slug, err)
		}

		item := models.MenuItem{
			TenantID: tenant.ID, Nome: "Prato de " + nome, Preco: 10,
			Categoria: "Pratos", Disponivel: true,
		}
		if err := gdb.Create(&item).Error; err != nil {
			t.Fatalf("criar item %s: %v", slug, err)
		}

		pedido := models.Pedido{
			TenantID: tenant.ID, PublicToken: uuid.NewString(),
			ClienteNome: "Cliente de " + nome, ClienteTelefone: "+351912345678",
			Status: models.StatusPendente, ValorTotal: 10,
			FormaPagamento: "dinheiro", CreatedAt: agora, UpdatedAt: agora,
		}
		if err := gdb.Create(&pedido).Error; err != nil {
			t.Fatalf("criar pedido %s: %v", slug, err)
		}

		return tenant, usuario, item, pedido
	}

	a.tenantA, a.userA, a.itemA, a.pedidoA = criar("Restaurante A", "restaurante-a")
	a.tenantB, a.userB, a.itemB, a.pedidoB = criar("Restaurante B", "restaurante-b")
}

// tokenDe emite um access token legítimo para um utilizador.
func (a *ambiente) tokenDe(u models.Usuario) string {
	tok, _, err := a.tokens.IssueAccessToken(u.TenantID, u.ID, u.Role)
	if err != nil {
		panic(err)
	}
	return tok
}

type pedidoHTTP struct {
	metodo string
	rota   string
	corpo  string
	token  string
	host   string
}

func (a *ambiente) fazer(p pedidoHTTP) *httptest.ResponseRecorder {
	var body *bytes.Reader
	if p.corpo != "" {
		body = bytes.NewReader([]byte(p.corpo))
	} else {
		body = bytes.NewReader(nil)
	}

	req := httptest.NewRequest(p.metodo, p.rota, body)
	req.Header.Set("Content-Type", "application/json")
	if p.token != "" {
		req.Header.Set("Authorization", "Bearer "+p.token)
	}
	if p.host != "" {
		req.Host = p.host
	}

	rec := httptest.NewRecorder()
	a.router.ServeHTTP(rec, req)
	return rec
}

func envOu(chave, omissao string) string {
	if v := os.Getenv(chave); v != "" {
		return v
	}
	return omissao
}

// --- C1: tokens forjados ---

// TestTokenLegadoForjadoNaoDaAcesso é a prova de que a falha C1 está fechada.
func TestTokenLegadoForjadoNaoDaAcesso(t *testing.T) {
	amb := montarAmbiente(t)

	rotasAdmin := []pedidoHTTP{
		{metodo: "GET", rota: "/api/admin/config"},
		{metodo: "PUT", rota: "/api/admin/config", corpo: `{"nome":"Invadido"}`},
		{metodo: "GET", rota: "/api/admin/cardapio"},
		{metodo: "POST", rota: "/api/admin/cardapio", corpo: `{"nome":"X","preco":1,"categoria":"Y"}`},
		{metodo: "GET", rota: "/api/admin/pedidos"},
		{metodo: "GET", rota: "/api/admin/usuarios"},
		{metodo: "POST", rota: "/api/admin/usuarios", corpo: `{"nome":"Mau","email":"mau@x.pt","password":"Senha1234","role":"owner"}`},
	}

	// Formatos aceites pela versão anterior, mais variações plausíveis.
	tokensForjados := []string{
		fmt.Sprintf("admin_user_%d_%d", amb.tenantB.ID, amb.userB.ID),
		fmt.Sprintf("admin_tenant_%d", amb.tenantB.ID),
		"admin_user_1_1",
		"admin_user_0_0",
	}

	for _, tok := range tokensForjados {
		for _, p := range rotasAdmin {
			p.token = tok
			rec := amb.fazer(p)
			if rec.Code != http.StatusUnauthorized {
				t.Errorf("%s %s com token forjado %q: status %d, esperado 401\ncorpo: %s",
					p.metodo, p.rota, tok, rec.Code, rec.Body.String())
			}
		}
	}
}

func TestSemTokenNaoDaAcesso(t *testing.T) {
	amb := montarAmbiente(t)

	rec := amb.fazer(pedidoHTTP{metodo: "GET", rota: "/api/admin/pedidos"})
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("acesso sem token: status %d, esperado 401", rec.Code)
	}
}

// TestCabecalhoXTenantSlugIgnorado: o cabeçalho permitia escolher o tenant.
func TestCabecalhoXTenantSlugIgnorado(t *testing.T) {
	amb := montarAmbiente(t)

	req := httptest.NewRequest("GET", "/api/admin/cardapio", nil)
	req.Header.Set("Authorization", "Bearer "+amb.tokenDe(amb.userA))
	req.Header.Set("X-Tenant-Slug", amb.tenantB.Slug)

	rec := httptest.NewRecorder()
	amb.router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}

	var itens []models.MenuItem
	if err := json.Unmarshal(rec.Body.Bytes(), &itens); err != nil {
		t.Fatalf("resposta: %v", err)
	}
	for _, it := range itens {
		if it.TenantID != amb.tenantA.ID {
			t.Errorf("X-Tenant-Slug mudou o tenant: item do tenant %d visível", it.TenantID)
		}
	}
}

// --- C2: escalonamento via Host ---

// TestHostDeOutroTenantNaoMudaOEscopo é a prova de que a falha C2 está fechada.
//
// O tenant A abre o subdomínio do tenant B com o seu próprio token válido. Antes, o
// middleware resolvia o tenant pelo Host primeiro e o token só era usado se nada tivesse
// sido encontrado, pelo que A passava a operar sobre os dados de B.
func TestHostDeOutroTenantNaoMudaOEscopo(t *testing.T) {
	amb := montarAmbiente(t)
	tokenA := amb.tokenDe(amb.userA)

	hostsDeB := []string{
		amb.tenantB.Slug + "." + dominioTeste,
		"www." + amb.tenantB.Slug + "." + dominioTeste,
	}

	for _, host := range hostsDeB {
		// Leitura do cardápio.
		rec := amb.fazer(pedidoHTTP{
			metodo: "GET", rota: "/api/admin/cardapio", token: tokenA, host: host,
		})
		if rec.Code != http.StatusOK {
			t.Fatalf("host %s: status %d: %s", host, rec.Code, rec.Body.String())
		}
		var itens []models.MenuItem
		if err := json.Unmarshal(rec.Body.Bytes(), &itens); err != nil {
			t.Fatal(err)
		}
		if len(itens) == 0 {
			t.Fatalf("host %s: nenhum item devolvido", host)
		}
		for _, it := range itens {
			if it.TenantID != amb.tenantA.ID {
				t.Errorf("host %s: item do tenant %d visível para o tenant %d",
					host, it.TenantID, amb.tenantA.ID)
			}
		}

		// Leitura da configuração: o nome tem de continuar a ser o de A.
		rec = amb.fazer(pedidoHTTP{
			metodo: "GET", rota: "/api/admin/config", token: tokenA, host: host,
		})
		var cfg map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &cfg); err != nil {
			t.Fatal(err)
		}
		if cfg["slug"] != amb.tenantA.Slug {
			t.Errorf("host %s: config devolveu slug %v, esperado %s",
				host, cfg["slug"], amb.tenantA.Slug)
		}
	}
}

// TestEscritaCruzadaEhBloqueada verifica cada rota de escrita com o ID de um recurso do
// outro tenant.
func TestEscritaCruzadaEhBloqueada(t *testing.T) {
	amb := montarAmbiente(t)
	tokenA := amb.tokenDe(amb.userA)

	casos := []struct {
		nome     string
		pedido   pedidoHTTP
		esperado int
	}{
		{
			nome: "editar produto de outro tenant",
			pedido: pedidoHTTP{
				metodo: "PUT",
				rota:   fmt.Sprintf("/api/admin/cardapio/%d", amb.itemB.ID),
				corpo:  `{"nome":"Invadido","preco":1,"categoria":"X"}`,
				token:  tokenA,
			},
			esperado: http.StatusNotFound,
		},
		{
			nome: "apagar produto de outro tenant",
			pedido: pedidoHTTP{
				metodo: "DELETE",
				rota:   fmt.Sprintf("/api/admin/cardapio/%d", amb.itemB.ID),
				token:  tokenA,
			},
			esperado: http.StatusNotFound,
		},
		{
			nome: "alterar estado de encomenda de outro tenant",
			pedido: pedidoHTTP{
				metodo: "PUT",
				rota:   fmt.Sprintf("/api/admin/pedidos/%d/status", amb.pedidoB.ID),
				corpo:  `{"status":"cancelado"}`,
				token:  tokenA,
			},
			esperado: http.StatusNotFound,
		},
		{
			nome: "remover utilizador de outro tenant",
			pedido: pedidoHTTP{
				metodo: "DELETE",
				rota:   fmt.Sprintf("/api/admin/usuarios/%d", amb.userB.ID),
				token:  tokenA,
			},
			esperado: http.StatusNotFound,
		},
	}

	for _, caso := range casos {
		t.Run(caso.nome, func(t *testing.T) {
			rec := amb.fazer(caso.pedido)
			if rec.Code != caso.esperado {
				t.Errorf("status %d, esperado %d\ncorpo: %s", rec.Code, caso.esperado, rec.Body.String())
			}
		})
	}

	// Confirmação directa na base: nada de B foi alterado nem removido.
	var itemB models.MenuItem
	if err := amb.gdb.First(&itemB, amb.itemB.ID).Error; err != nil {
		t.Fatalf("item do tenant B desapareceu: %v", err)
	}
	if itemB.Nome != amb.itemB.Nome {
		t.Errorf("item do tenant B foi alterado: %q", itemB.Nome)
	}

	var pedidoB models.Pedido
	if err := amb.gdb.First(&pedidoB, amb.pedidoB.ID).Error; err != nil {
		t.Fatal(err)
	}
	if pedidoB.Status != models.StatusPendente {
		t.Errorf("encomenda do tenant B mudou de estado para %q", pedidoB.Status)
	}

	var userB models.Usuario
	if err := amb.gdb.First(&userB, amb.userB.ID).Error; err != nil {
		t.Errorf("utilizador do tenant B foi removido: %v", err)
	}
}

// TestListagensSoDevolvemODoProprioTenant cobre as rotas de leitura em bloco.
func TestListagensSoDevolvemODoProprioTenant(t *testing.T) {
	amb := montarAmbiente(t)
	tokenA := amb.tokenDe(amb.userA)

	t.Run("encomendas", func(t *testing.T) {
		rec := amb.fazer(pedidoHTTP{metodo: "GET", rota: "/api/admin/pedidos", token: tokenA})
		if rec.Code != http.StatusOK {
			t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
		}
		var resp struct {
			Encomendas []models.Pedido `json:"encomendas"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatal(err)
		}
		if len(resp.Encomendas) == 0 {
			t.Fatal("nenhuma encomenda devolvida")
		}
		for _, p := range resp.Encomendas {
			if p.TenantID != amb.tenantA.ID {
				t.Errorf("encomenda do tenant %d visível", p.TenantID)
			}
		}
	})

	t.Run("utilizadores", func(t *testing.T) {
		rec := amb.fazer(pedidoHTTP{metodo: "GET", rota: "/api/admin/usuarios", token: tokenA})
		var usuarios []models.Usuario
		if err := json.Unmarshal(rec.Body.Bytes(), &usuarios); err != nil {
			t.Fatal(err)
		}
		if len(usuarios) == 0 {
			t.Fatal("nenhum utilizador devolvido")
		}
		for _, u := range usuarios {
			if u.TenantID != amb.tenantA.ID {
				t.Errorf("utilizador do tenant %d visível", u.TenantID)
			}
		}
	})
}

// TestProdutoCriadoFicaNoTenantDoToken: um tenant_id enviado no corpo é ignorado.
func TestProdutoCriadoFicaNoTenantDoToken(t *testing.T) {
	amb := montarAmbiente(t)

	corpo := fmt.Sprintf(
		`{"nome":"Tentativa","preco":5,"categoria":"X","tenant_id":%d}`, amb.tenantB.ID)

	rec := amb.fazer(pedidoHTTP{
		metodo: "POST", rota: "/api/admin/cardapio",
		corpo: corpo, token: amb.tokenDe(amb.userA),
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}

	var criado models.MenuItem
	if err := json.Unmarshal(rec.Body.Bytes(), &criado); err != nil {
		t.Fatal(err)
	}
	if criado.TenantID != amb.tenantA.ID {
		t.Errorf("produto criado no tenant %d, esperado %d", criado.TenantID, amb.tenantA.ID)
	}
}

// --- C3: IDOR no rastreio de encomendas ---

// TestRastreioNaoAceitaIDSequencial cobre a falha C3.
func TestRastreioNaoAceitaIDSequencial(t *testing.T) {
	amb := montarAmbiente(t)

	// Enumerar IDs já não devolve nada.
	for id := 1; id <= 10; id++ {
		rec := amb.fazer(pedidoHTTP{metodo: "GET", rota: fmt.Sprintf("/api/encomendas/%d", id)})
		if rec.Code != http.StatusNotFound {
			t.Errorf("ID sequencial %d devolveu status %d: %s", id, rec.Code, rec.Body.String())
		}
	}

	// Com o token opaco funciona.
	rec := amb.fazer(pedidoHTTP{metodo: "GET", rota: "/api/encomendas/" + amb.pedidoB.PublicToken})
	if rec.Code != http.StatusOK {
		t.Fatalf("token válido rejeitado: status %d: %s", rec.Code, rec.Body.String())
	}

	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	// O telefone tem de vir mascarado.
	if tel, _ := resp["cliente_telefone"].(string); tel == "+351912345678" {
		t.Errorf("telefone completo exposto no rastreio público: %q", tel)
	}
}

// --- C7: RBAC ---

// TestRBACImpedeEscalonamento verifica que os papéis são aplicados.
func TestRBACImpedeEscalonamento(t *testing.T) {
	amb := montarAmbiente(t)

	agora := time.Now()
	hash, _ := auth.HashPassword("SenhaTeste123")
	funcionario := models.Usuario{
		TenantID: amb.tenantA.ID, Nome: "Funcionário", Email: "func@teste.local",
		SenhaHash: hash, EmailVerifiedAt: &agora,
		Role: models.RoleFuncionario, Ativo: true,
	}
	if err := amb.gdb.Create(&funcionario).Error; err != nil {
		t.Fatal(err)
	}
	tokenFunc := amb.tokenDe(funcionario)

	proibidas := []pedidoHTTP{
		{metodo: "PUT", rota: "/api/admin/config", corpo: `{"nome":"Novo"}`},
		{metodo: "POST", rota: "/api/admin/config/dominio", corpo: `{"domain":"mau.pt"}`},
		{metodo: "GET", rota: "/api/admin/usuarios"},
		{metodo: "POST", rota: "/api/admin/usuarios", corpo: `{"nome":"X","email":"x@y.pt","password":"Senha1234","role":"owner"}`},
		{metodo: "POST", rota: "/api/admin/cardapio", corpo: `{"nome":"X","preco":1,"categoria":"Y"}`},
		{metodo: "DELETE", rota: fmt.Sprintf("/api/admin/cardapio/%d", amb.itemA.ID)},
	}
	for _, p := range proibidas {
		p.token = tokenFunc
		rec := amb.fazer(p)
		if rec.Code != http.StatusForbidden {
			t.Errorf("funcionário em %s %s: status %d, esperado 403", p.metodo, p.rota, rec.Code)
		}
	}

	// Mas tem de conseguir operar as encomendas: é o trabalho dele.
	permitidas := []pedidoHTTP{
		{metodo: "GET", rota: "/api/admin/pedidos"},
		{metodo: "PUT", rota: fmt.Sprintf("/api/admin/pedidos/%d/status", amb.pedidoA.ID), corpo: `{"status":"preparando"}`},
	}
	for _, p := range permitidas {
		p.token = tokenFunc
		rec := amb.fazer(p)
		if rec.Code != http.StatusOK {
			t.Errorf("funcionário em %s %s: status %d, esperado 200: %s",
				p.metodo, p.rota, rec.Code, rec.Body.String())
		}
	}
}

// TestAdminNaoPodeCriarOwner impede escalonamento de privilégios por criação de conta.
func TestAdminNaoPodeCriarOwner(t *testing.T) {
	amb := montarAmbiente(t)

	agora := time.Now()
	hash, _ := auth.HashPassword("SenhaTeste123")
	admin := models.Usuario{
		TenantID: amb.tenantA.ID, Nome: "Admin", Email: "admin@teste.local",
		SenhaHash: hash, EmailVerifiedAt: &agora, Role: models.RoleAdmin, Ativo: true,
	}
	if err := amb.gdb.Create(&admin).Error; err != nil {
		t.Fatal(err)
	}

	rec := amb.fazer(pedidoHTTP{
		metodo: "POST", rota: "/api/admin/usuarios",
		corpo: `{"nome":"Novo Owner","email":"novo@teste.local","password":"SenhaForte9","role":"owner"}`,
		token: amb.tokenDe(admin),
	})
	if rec.Code != http.StatusForbidden {
		t.Errorf("admin criou owner: status %d: %s", rec.Code, rec.Body.String())
	}
}

// --- Storefront público ---

// TestStorefrontResolvePeloHost confirma que a via pública continua a funcionar pelo Host.
func TestStorefrontResolvePeloHost(t *testing.T) {
	amb := montarAmbiente(t)

	rec := amb.fazer(pedidoHTTP{
		metodo: "GET", rota: "/api/public-menu",
		host: amb.tenantB.Slug + "." + dominioTeste,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}

	var resp struct {
		Restaurante struct {
			Slug string `json:"slug"`
		} `json:"restaurante"`
		Itens []models.MenuItem `json:"itens"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Restaurante.Slug != amb.tenantB.Slug {
		t.Errorf("storefront resolveu %q, esperado %q", resp.Restaurante.Slug, amb.tenantB.Slug)
	}
	for _, it := range resp.Itens {
		if it.TenantID != amb.tenantB.ID {
			t.Errorf("cardápio público mistura tenants: item do tenant %d", it.TenantID)
		}
	}
}

// TestEncomendaNaoAceitaProdutoDeOutroTenant impede montar uma encomenda com produtos
// alheios, que produziria totais e cozinha erradas.
func TestEncomendaNaoAceitaProdutoDeOutroTenant(t *testing.T) {
	amb := montarAmbiente(t)

	corpo := fmt.Sprintf(`{
		"cliente_nome":"Cliente",
		"cliente_telefone":"912345678",
		"forma_pagamento":"dinheiro",
		"itens":[{"menu_item_id":%d,"quantidade":1}]
	}`, amb.itemB.ID)

	rec := amb.fazer(pedidoHTTP{
		metodo: "POST", rota: "/api/pedidos", corpo: corpo,
		host: amb.tenantA.Slug + "." + dominioTeste,
	})
	if rec.Code != http.StatusBadRequest {
		t.Errorf("encomenda com produto de outro tenant: status %d, esperado 400: %s",
			rec.Code, rec.Body.String())
	}
}

// TestPrecoVemDaBaseDeDados: o cliente não pode influenciar o total.
func TestPrecoVemDaBaseDeDados(t *testing.T) {
	amb := montarAmbiente(t)

	corpo := fmt.Sprintf(`{
		"cliente_nome":"Cliente",
		"cliente_telefone":"912345678",
		"forma_pagamento":"dinheiro",
		"itens":[{"menu_item_id":%d,"quantidade":2,"preco_unitario":0.01}]
	}`, amb.itemA.ID)

	rec := amb.fazer(pedidoHTTP{
		metodo: "POST", rota: "/api/pedidos", corpo: corpo,
		host: amb.tenantA.Slug + "." + dominioTeste,
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}

	var resp struct {
		Encomenda struct {
			ValorTotal float64 `json:"valor_total"`
		} `json:"encomenda"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	// 2 x 10 = 20, independentemente do que o cliente enviou.
	if resp.Encomenda.ValorTotal != 20 {
		t.Errorf("valor total = %v, esperado 20", resp.Encomenda.ValorTotal)
	}
}

// TestIdempotenciaNaoDuplicaEncomenda cobre o duplo-toque no checkout.
func TestIdempotenciaNaoDuplicaEncomenda(t *testing.T) {
	amb := montarAmbiente(t)

	corpo := fmt.Sprintf(`{
		"cliente_nome":"Cliente",
		"cliente_telefone":"912345678",
		"forma_pagamento":"dinheiro",
		"itens":[{"menu_item_id":%d,"quantidade":1}]
	}`, amb.itemA.ID)

	fazerComChave := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest("POST", "/api/pedidos", bytes.NewReader([]byte(corpo)))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Idempotency-Key", "chave-fixa-do-teste")
		req.Host = amb.tenantA.Slug + "." + dominioTeste
		rec := httptest.NewRecorder()
		amb.router.ServeHTTP(rec, req)
		return rec
	}

	primeiro := fazerComChave()
	if primeiro.Code != http.StatusCreated {
		t.Fatalf("primeira encomenda: status %d: %s", primeiro.Code, primeiro.Body.String())
	}
	segundo := fazerComChave()
	if segundo.Code != http.StatusOK {
		t.Fatalf("segunda encomenda: status %d, esperado 200: %s", segundo.Code, segundo.Body.String())
	}

	extrairToken := func(rec *httptest.ResponseRecorder) string {
		var resp struct {
			Encomenda struct {
				PublicToken string `json:"public_token"`
			} `json:"encomenda"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatal(err)
		}
		return resp.Encomenda.PublicToken
	}
	if extrairToken(primeiro) != extrairToken(segundo) {
		t.Error("a mesma Idempotency-Key criou duas encomendas diferentes")
	}

	var total int64
	amb.gdb.Model(&models.Pedido{}).
		Where("tenant_id = ? AND idempotency_key = ?", amb.tenantA.ID, "chave-fixa-do-teste").
		Count(&total)
	if total != 1 {
		t.Errorf("%d encomendas com a mesma chave de idempotência", total)
	}
}

// TestTransicaoDeEstadoInvalidaEhRejeitada cobre a máquina de estados.
func TestTransicaoDeEstadoInvalidaEhRejeitada(t *testing.T) {
	amb := montarAmbiente(t)
	token := amb.tokenDe(amb.userA)

	rota := fmt.Sprintf("/api/admin/pedidos/%d/status", amb.pedidoA.ID)

	// pendente -> finalizado salta etapas.
	rec := amb.fazer(pedidoHTTP{metodo: "PUT", rota: rota, corpo: `{"status":"finalizado"}`, token: token})
	if rec.Code != http.StatusConflict {
		t.Errorf("salto de estado aceite: status %d: %s", rec.Code, rec.Body.String())
	}

	// Caminho válido, e depois tentar sair de um estado terminal.
	for _, s := range []string{"preparando", "pronto", "finalizado"} {
		rec := amb.fazer(pedidoHTTP{metodo: "PUT", rota: rota, corpo: `{"status":"` + s + `"}`, token: token})
		if rec.Code != http.StatusOK {
			t.Fatalf("transição para %s falhou: status %d: %s", s, rec.Code, rec.Body.String())
		}
	}
	rec = amb.fazer(pedidoHTTP{metodo: "PUT", rota: rota, corpo: `{"status":"preparando"}`, token: token})
	if rec.Code != http.StatusConflict {
		t.Errorf("reabertura de encomenda finalizada aceite: status %d", rec.Code)
	}
}

// TestUpdateConfigParcialNaoDesactivaPagamentos cobre a regressão dos campos opcionais:
// um payload sem os booleanos desactivava todos os métodos de pagamento.
func TestUpdateConfigParcialNaoDesactivaPagamentos(t *testing.T) {
	amb := montarAmbiente(t)

	rec := amb.fazer(pedidoHTTP{
		metodo: "PUT", rota: "/api/admin/config",
		corpo: `{"nome":"Restaurante A Renomeado"}`,
		token: amb.tokenDe(amb.userA),
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}

	var t2 models.Tenant
	if err := amb.gdb.First(&t2, amb.tenantA.ID).Error; err != nil {
		t.Fatal(err)
	}
	if t2.Nome != "Restaurante A Renomeado" {
		t.Errorf("nome não actualizado: %q", t2.Nome)
	}
	if !t2.DinheiroAtivo {
		t.Error("pagamento em dinheiro foi desactivado por um payload parcial")
	}
	if !t2.CartaoDebitoAtivo {
		t.Error("pagamento com cartão foi desactivado por um payload parcial")
	}
}

// TestNaoPodeDesactivarTodosOsPagamentos: sem nenhum método activo o restaurante receberia
// encomendas que não consegue cobrar na caixa.
func TestNaoPodeDesactivarTodosOsPagamentos(t *testing.T) {
	amb := montarAmbiente(t)

	rec := amb.fazer(pedidoHTTP{
		metodo: "PUT", rota: "/api/admin/config",
		corpo: `{"dinheiro_ativo":false,"cartao_ativo":false}`,
		token: amb.tokenDe(amb.userA),
	})
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status %d, esperado 400: %s", rec.Code, rec.Body.String())
	}

	var t2 models.Tenant
	if err := amb.gdb.First(&t2, amb.tenantA.ID).Error; err != nil {
		t.Fatal(err)
	}
	if !t2.DinheiroAtivo || !t2.CartaoDebitoAtivo {
		t.Error("os métodos de pagamento foram alterados apesar da rejeição")
	}
}

// TestPagamentoOnlineNaoEhAceite documenta o âmbito do MVP: só pagamento na caixa.
func TestPagamentoOnlineNaoEhAceite(t *testing.T) {
	amb := montarAmbiente(t)

	for _, metodo := range []string{"pix", "cartao_credito", "tpa", "mbway", "multibanco"} {
		corpo := fmt.Sprintf(`{
			"cliente_nome":"Cliente",
			"cliente_telefone":"912345678",
			"forma_pagamento":%q,
			"itens":[{"menu_item_id":%d,"quantidade":1}]
		}`, metodo, amb.itemA.ID)

		rec := amb.fazer(pedidoHTTP{
			metodo: "POST", rota: "/api/pedidos", corpo: corpo,
			host: amb.tenantA.Slug + "." + dominioTeste,
		})
		if rec.Code != http.StatusBadRequest {
			t.Errorf("método %q aceite: status %d: %s", metodo, rec.Code, rec.Body.String())
		}
	}

	// Os dois métodos do MVP funcionam.
	for _, metodo := range []string{"dinheiro", "cartao"} {
		corpo := fmt.Sprintf(`{
			"cliente_nome":"Cliente",
			"cliente_telefone":"912345678",
			"forma_pagamento":%q,
			"itens":[{"menu_item_id":%d,"quantidade":1}]
		}`, metodo, amb.itemA.ID)

		rec := amb.fazer(pedidoHTTP{
			metodo: "POST", rota: "/api/pedidos", corpo: corpo,
			host: amb.tenantA.Slug + "." + dominioTeste,
		})
		if rec.Code != http.StatusCreated {
			t.Errorf("método %q rejeitado: status %d: %s", metodo, rec.Code, rec.Body.String())
		}
	}
}

// TestDominioSoEncaminhaDepoisDeVerificado cobre C5 ao nível do handler.
func TestDominioSoEncaminhaDepoisDeVerificado(t *testing.T) {
	amb := montarAmbiente(t)

	rec := amb.fazer(pedidoHTTP{
		metodo: "POST", rota: "/api/admin/config/dominio",
		corpo: `{"domain":"marca-de-terceiros.pt"}`,
		token: amb.tokenDe(amb.userA),
	})
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status %d, esperado 202: %s", rec.Code, rec.Body.String())
	}

	var t2 models.Tenant
	if err := amb.gdb.First(&t2, amb.tenantA.ID).Error; err != nil {
		t.Fatal(err)
	}
	if t2.DomainStatus != models.DomainPendente {
		t.Errorf("domain_status = %q, esperado %q", t2.DomainStatus, models.DomainPendente)
	}
	if t2.DomainAtivo() {
		t.Error("domínio não verificado considerado activo para encaminhamento")
	}
	if t2.DomainVerifyToken == "" {
		t.Error("token de verificação não gerado")
	}

	// O storefront não deve resolver por um domínio ainda não verificado.
	rec = amb.fazer(pedidoHTTP{metodo: "GET", rota: "/api/public-menu", host: "marca-de-terceiros.pt"})
	if rec.Code != http.StatusNotFound {
		t.Errorf("domínio pendente resolveu o storefront: status %d", rec.Code)
	}
}

// TestNaoPodeReclamarDominioDaPlataforma impede desviar a landing page ou o painel.
func TestNaoPodeReclamarDominioDaPlataforma(t *testing.T) {
	amb := montarAmbiente(t)
	token := amb.tokenDe(amb.userA)

	for _, d := range []string{dominioTeste, "www." + dominioTeste, "outro." + dominioTeste} {
		rec := amb.fazer(pedidoHTTP{
			metodo: "POST", rota: "/api/admin/config/dominio",
			corpo: fmt.Sprintf(`{"domain":%q}`, d), token: token,
		})
		if rec.Code != http.StatusBadRequest {
			t.Errorf("domínio da plataforma %q aceite: status %d: %s", d, rec.Code, rec.Body.String())
		}
	}
}

// TestErroInternoNaoExpoeDetalhesEmProducao cobre a fuga de informação dos handlers.
func TestErroInternoNaoExpoeDetalhesEmProducao(t *testing.T) {
	amb := montarAmbiente(t)

	// Um ID inexistente provoca 404 com mensagem genérica, não uma mensagem do MySQL.
	rec := amb.fazer(pedidoHTTP{
		metodo: "PUT", rota: "/api/admin/cardapio/999999",
		corpo: `{"nome":"X","preco":1,"categoria":"Y"}`,
		token: amb.tokenDe(amb.userA),
	})

	corpo := rec.Body.String()
	for _, fuga := range []string{"menu_items", "SELECT", "gorm", "sql:", "tenant_id ="} {
		if bytes.Contains([]byte(corpo), []byte(fuga)) {
			t.Errorf("resposta expõe detalhe interno %q: %s", fuga, corpo)
		}
	}
}
