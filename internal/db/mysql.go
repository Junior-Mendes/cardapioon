package db

import (
	"fmt"
	"log/slog"
	"net/url"
	"time"

	"cardapio-online/internal/auth"
	"cardapio-online/internal/config"
	"cardapio-online/internal/models"

	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

// DB é a instância global. Mantida para não reescrever todos os handlers de uma vez;
// a injecção explícita de dependências é dívida técnica registada.
var DB *gorm.DB

// Init estabelece a ligação, configura o pool e aplica as migrações.
func Init(cfg *config.Config) (*gorm.DB, error) {
	nivelLog := gormlogger.Warn
	if cfg.DevMode() {
		nivelLog = gormlogger.Info
	}

	gdb, err := gorm.Open(mysql.Open(dsn(cfg, cfg.DBName)), &gorm.Config{
		Logger: gormlogger.Default.LogMode(nivelLog),
		// O GORM cria as constraints de chave estrangeira a partir das structs; como as
		// migrações passam a ser SQL explícito, desligamos essa geração automática.
		DisableForeignKeyConstraintWhenMigrating: true,
	})
	if err != nil {
		return nil, fmt.Errorf("ligar ao MySQL em %s:%s: %w", cfg.DBHost, cfg.DBPort, err)
	}

	sqlDB, err := gdb.DB()
	if err != nil {
		return nil, fmt.Errorf("obter pool de conexões: %w", err)
	}

	// Sem estes limites o pool cresce sem travão sob carga e esgota
	// max_connections do MySQL, derrubando todos os tenants ao mesmo tempo.
	sqlDB.SetMaxOpenConns(25)
	sqlDB.SetMaxIdleConns(10)
	sqlDB.SetConnMaxLifetime(30 * time.Minute)
	sqlDB.SetConnMaxIdleTime(5 * time.Minute)

	if err := sqlDB.Ping(); err != nil {
		return nil, fmt.Errorf("ping ao MySQL: %w", err)
	}

	slog.Info("ligação ao MySQL estabelecida",
		"host", cfg.DBHost, "porta", cfg.DBPort, "base", cfg.DBName)

	DB = gdb

	if err := RunMigrations(gdb); err != nil {
		return nil, fmt.Errorf("aplicar migrações: %w", err)
	}

	// Contas antigas cujo tenant não tinha utilizador associado. Idempotente.
	if err := garantirUtilizadorPorTenant(gdb); err != nil {
		return nil, fmt.Errorf("garantir utilizador por tenant: %w", err)
	}

	if cfg.SeedDemoData {
		if err := SeedDemo(gdb); err != nil {
			return nil, fmt.Errorf("seeders de demonstração: %w", err)
		}
	}

	return gdb, nil
}

// dsn constrói a string de ligação com a senha escapada.
//
// A versão anterior interpolava a senha directamente; a senha em uso contém '!' e '@',
// e um '@' numa senha não escapada parte o DSN no sítio errado.
func dsn(cfg *config.Config, nomeBase string) string {
	credenciais := url.QueryEscape(cfg.DBUser)
	if cfg.DBPassword != "" {
		credenciais += ":" + url.QueryEscape(cfg.DBPassword)
	}
	return fmt.Sprintf("%s@tcp(%s:%s)/%s?charset=utf8mb4&parseTime=True&loc=Local&timeout=10s&readTimeout=30s&writeTimeout=30s",
		credenciais, cfg.DBHost, cfg.DBPort, nomeBase)
}

// garantirUtilizadorPorTenant cria um utilizador owner para tenants que não tenham
// nenhum, reaproveitando o hash de senha legado guardado em tenants.senha_hash.
//
// Sem isto, tenants criados pela versão anterior (que autenticava contra tenants) ficariam
// sem forma de entrar no painel.
func garantirUtilizadorPorTenant(gdb *gorm.DB) error {
	var tenants []models.Tenant
	if err := gdb.Find(&tenants).Error; err != nil {
		return err
	}

	agora := time.Now()
	for _, t := range tenants {
		var contagem int64
		if err := gdb.Model(&models.Usuario{}).Where("tenant_id = ?", t.ID).Count(&contagem).Error; err != nil {
			return err
		}
		if contagem > 0 {
			continue
		}

		email := fmt.Sprintf("admin+%s@%s.invalid", t.Slug, t.Slug)
		u := models.Usuario{
			TenantID:        t.ID,
			Nome:            "Administrador " + t.Nome,
			Email:           email,
			EmailVerifiedAt: &agora,
			SenhaHash:       t.SenhaHash,
			Role:            models.RoleOwner,
			Ativo:           true,
		}
		if err := gdb.Create(&u).Error; err != nil {
			// Um conflito de email não deve impedir o arranque do servidor.
			slog.Error("falha ao criar utilizador para tenant sem utilizadores",
				"slug", t.Slug, "erro", err)
			continue
		}
		slog.Warn("criado utilizador owner para tenant legado; a senha é a antiga do restaurante",
			"slug", t.Slug, "email", email)
	}
	return nil
}

// SeedDemo cria dados de demonstração. Só corre quando SEED_DEMO_DATA=true, o que a
// configuração proíbe em produção: a versão anterior semeava sempre, gravando
// restaurantes com a senha pública "admin123" na base real.
func SeedDemo(gdb *gorm.DB) error {
	var contagem int64
	if err := gdb.Model(&models.Tenant{}).Count(&contagem).Error; err != nil {
		return err
	}
	if contagem > 0 {
		slog.Info("base já contém tenants; seeders ignorados")
		return nil
	}

	slog.Info("a criar dados de demonstração")

	hash, err := auth.HashPassword("Demo!2345")
	if err != nil {
		return err
	}
	agora := time.Now()

	demos := []struct {
		tenant models.Tenant
		email  string
		itens  []models.MenuItem
	}{
		{
			tenant: models.Tenant{
				Nome: "Tasca do Bairro", Slug: "tasca-do-bairro", Ativo: true,
				SenhaHash: hash, DomainStatus: models.DomainNenhum,
				CartaoCreditoAtivo: true, CartaoDebitoAtivo: true, DinheiroAtivo: true,
			},
			email: "demo@tasca-do-bairro.invalid",
			itens: []models.MenuItem{
				{Nome: "Bacalhau à Brás", Descricao: "Bacalhau desfiado, batata palha, ovo e azeitonas.", Preco: 12.50, Categoria: "Pratos principais", Disponivel: true},
				{Nome: "Francesinha", Descricao: "Pão, fiambre, linguiça, bife e molho da casa.", Preco: 11.00, PrecoDesconto: 9.50, DescontoAtivo: true, Categoria: "Pratos principais", Disponivel: true},
				{Nome: "Pastel de nata", Descricao: "Feito no dia, com canela.", Preco: 1.40, Categoria: "Sobremesas", Disponivel: true},
				{Nome: "Água 0,5L", Descricao: "Água mineral natural.", Preco: 1.20, Categoria: "Bebidas", Disponivel: true},
			},
		},
		{
			tenant: models.Tenant{
				Nome: "Pizzaria Alfama", Slug: "pizzaria-alfama", Ativo: true,
				SenhaHash: hash, DomainStatus: models.DomainNenhum,
				CartaoCreditoAtivo: true, DinheiroAtivo: true,
			},
			email: "demo@pizzaria-alfama.invalid",
			itens: []models.MenuItem{
				{Nome: "Pizza Margherita", Descricao: "Molho de tomate, mozzarella e manjericão.", Preco: 9.90, Categoria: "Pizzas", Disponivel: true},
				{Nome: "Pizza Diavola", Descricao: "Salame picante e mozzarella.", Preco: 11.90, Categoria: "Pizzas", Disponivel: true},
			},
		},
	}

	return gdb.Transaction(func(tx *gorm.DB) error {
		for _, d := range demos {
			t := d.tenant
			if err := tx.Create(&t).Error; err != nil {
				return err
			}
			if err := tx.Create(&models.Usuario{
				TenantID: t.ID, Nome: "Demo " + t.Nome, Email: d.email,
				EmailVerifiedAt: &agora, SenhaHash: hash,
				Role: models.RoleOwner, Ativo: true,
			}).Error; err != nil {
				return err
			}
			for i := range d.itens {
				d.itens[i].TenantID = t.ID
				if err := tx.Create(&d.itens[i]).Error; err != nil {
					return err
				}
			}
		}
		slog.Info("dados de demonstração criados", "senha", "Demo!2345")
		return nil
	})
}
