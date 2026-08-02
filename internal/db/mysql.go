package db

import (
	"fmt"
	"log"

	"cardapio-online/internal/config"
	"cardapio-online/internal/models"

	"gorm.io/driver/mysql"
	"gorm.io/gorm"
)

var DB *gorm.DB

func InitDB(cfg *config.Config) *gorm.DB {
	// Cria o banco de dados se não existir
	var dsnWithoutDB string
	if cfg.DBPassword == "" {
		dsnWithoutDB = fmt.Sprintf("%s@tcp(%s:%s)/?charset=utf8mb4&parseTime=True&loc=Local",
			cfg.DBUser, cfg.DBHost, cfg.DBPort)
	} else {
		dsnWithoutDB = fmt.Sprintf("%s:%s@tcp(%s:%s)/?charset=utf8mb4&parseTime=True&loc=Local",
			cfg.DBUser, cfg.DBPassword, cfg.DBHost, cfg.DBPort)
	}

	dbInit, errInit := gorm.Open(mysql.Open(dsnWithoutDB), &gorm.Config{})
	if errInit == nil {
		dbInit.Exec(fmt.Sprintf("CREATE DATABASE IF NOT EXISTS %s CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci;", cfg.DBName))
		if sqlDBInit, errDBInit := dbInit.DB(); errDBInit == nil {
			sqlDBInit.Close()
		}
	}

	var dsn string
	if cfg.DBPassword == "" {
		dsn = fmt.Sprintf("%s@tcp(%s:%s)/%s?charset=utf8mb4&parseTime=True&loc=Local",
			cfg.DBUser, cfg.DBHost, cfg.DBPort, cfg.DBName)
	} else {
		dsn = fmt.Sprintf("%s:%s@tcp(%s:%s)/%s?charset=utf8mb4&parseTime=True&loc=Local",
			cfg.DBUser, cfg.DBPassword, cfg.DBHost, cfg.DBPort, cfg.DBName)
	}

	log.Printf("Conectando ao MySQL em %s:%s no banco '%s'...", cfg.DBHost, cfg.DBPort, cfg.DBName)
	db, err := gorm.Open(mysql.Open(dsn), &gorm.Config{})
	if err != nil {
		log.Fatalf("Erro ao conectar ao banco: %v", err)
	}

	log.Println("Conexão com o MySQL estabelecida com sucesso!")

	// Roda as migrações automáticas
	err = db.AutoMigrate(
		&models.Tenant{},
		&models.Usuario{},
		&models.MenuItem{},
		&models.Pedido{},
		&models.OrderItem{},
	)
	if err != nil {
		log.Fatalf("Falha na migração (AutoMigrate): %v", err)
	}

	log.Println("AutoMigrate executado com sucesso!")
	DB = db

	// Migração retroativa de usuários para tenants existentes
	MigrateExistingTenantsToUsers(db)

	// Executa os Seeders para criar tenants e itens de demonstração
	SeedData(db)

	return db
}

func SeedData(db *gorm.DB) {
	var count int64
	db.Model(&models.Tenant{}).Count(&count)
	if count == 0 {
		log.Println("Alimentando o banco de dados com dados iniciais de demonstração...")
		
		// Hash SHA-256 de "admin123" para senha padrão do lojista
		passwordHash := "8c6976e5b5410415bde908bd4dee15dfb167a9c873fc4bb8a81f6f2ab448a918"
		
		bellaItalia := models.Tenant{
			Nome:               "Bella Italia Pizzaria",
			Slug:               "bella-italia",
			Ativo:              true,
			SenhaHash:          passwordHash,
			PixAtivo:           true,
			PixChave:           "12345678909",
			CartaoCreditoAtivo: true,
			CartaoDebitoAtivo:  true,
			DinheiroAtivo:      true,
		}
		db.Create(&bellaItalia)

		db.Create(&models.Usuario{
			TenantID:  bellaItalia.ID,
			Nome:      "Bella Italia Admin",
			Email:     "admin@bellaitalia.com",
			SenhaHash: passwordHash,
			Role:      "owner",
			Ativo:     true,
		})

		burgersCo := models.Tenant{
			Nome:               "Burgers & Co.",
			Slug:               "burgers-co",
			Ativo:              true,
			SenhaHash:          passwordHash,
			PixAtivo:           true,
			PixChave:           "pix@burgersco.com",
			CartaoCreditoAtivo: true,
			CartaoDebitoAtivo:  false,
			DinheiroAtivo:      true,
		}
		db.Create(&burgersCo)

		db.Create(&models.Usuario{
			TenantID:  burgersCo.ID,
			Nome:      "Burgers & Co Admin",
			Email:     "admin@burgersco.com",
			SenhaHash: passwordHash,
			Role:      "owner",
			Ativo:     true,
		})

		// Itens da Pizzaria Bella Italia
		itemsBella := []models.MenuItem{
			{
				TenantID:      bellaItalia.ID,
				Nome:          "Pizza Margherita",
				Descricao:     "Molho de tomate artesanal, muçarela fior di latte, manjericão fresco e azeite extravirgem.",
				Preco:         45.00,
				PrecoDesconto: 39.90,
				DescontoAtivo: true,
				Categoria:     "Pizzas",
				ImagemURL:     "https://images.unsplash.com/photo-1574071318508-1cdbab80d002?w=500&auto=format&fit=crop&q=60",
				Disponivel:    true,
			},
			{
				TenantID:      bellaItalia.ID,
				Nome:          "Pizza Calabresa",
				Descricao:     "Molho de tomate fresco, muçarela, calabresa defumada fatiada e cebola roxa.",
				Preco:         48.00,
				PrecoDesconto: 0.00,
				DescontoAtivo: false,
				Categoria:     "Pizzas",
				ImagemURL:     "https://images.unsplash.com/photo-1513104890138-7c749659a591?w=500&auto=format&fit=crop&q=60",
				Disponivel:    true,
			},
			{
				TenantID:      bellaItalia.ID,
				Nome:          "Bruschetta Tradicional",
				Descricao:     "Pão italiano tostado com tomate concassé, alho, manjericão e queijo gratinado.",
				Preco:         22.00,
				PrecoDesconto: 18.00,
				DescontoAtivo: true,
				Categoria:     "Entradas",
				ImagemURL:     "https://images.unsplash.com/photo-1572656631137-7935297eff55?w=500&auto=format&fit=crop&q=60",
				Disponivel:    true,
			},
			{
				TenantID:      bellaItalia.ID,
				Nome:          "Coca-Cola Lata",
				Descricao:     "Refrigerante Coca-Cola original de 350ml bem gelado.",
				Preco:         6.00,
				PrecoDesconto: 0.00,
				DescontoAtivo: false,
				Categoria:     "Bebidas",
				ImagemURL:     "https://images.unsplash.com/photo-1622483767028-3f66f32aef97?w=500&auto=format&fit=crop&q=60",
				Disponivel:    true,
			},
		}
		for _, item := range itemsBella {
			db.Create(&item)
		}

		// Itens de Hamburgueria Burgers & Co
		itemsBurgers := []models.MenuItem{
			{
				TenantID:      burgersCo.ID,
				Nome:          "Classic Smash Burger",
				Descricao:     "Dois blends smash de 80g, queijo cheddar derretido, picles artesanal e molho da casa.",
				Preco:         29.90,
				PrecoDesconto: 24.90,
				DescontoAtivo: true,
				Categoria:     "Hambúrgueres",
				ImagemURL:     "https://images.unsplash.com/photo-1568901346375-23c9450c58cd?w=500&auto=format&fit=crop&q=60",
				Disponivel:    true,
			},
			{
				TenantID:      burgersCo.ID,
				Nome:          "Bacon & Cheddar Monster",
				Descricao:     "Blend de 150g grelhado no fogo, muito bacon crocante, cheddar cremoso e cebola caramelizada.",
				Preco:         38.00,
				PrecoDesconto: 0.00,
				DescontoAtivo: false,
				Categoria:     "Hambúrgueres",
				ImagemURL:     "https://images.unsplash.com/photo-1553979459-d2229ba7433b?w=500&auto=format&fit=crop&q=60",
				Disponivel:    true,
			},
			{
				TenantID:      burgersCo.ID,
				Nome:          "Batata Frita Rústica",
				Descricao:     "Porção individual de batatas fritas crocantes com sal de páprica e alecrim fresco.",
				Preco:         14.00,
				PrecoDesconto: 12.00,
				DescontoAtivo: true,
				Categoria:     "Acompanhamentos",
				ImagemURL:     "https://images.unsplash.com/photo-1573080496219-bb080dd4f877?w=500&auto=format&fit=crop&q=60",
				Disponivel:    true,
			},
		}
		for _, item := range itemsBurgers {
			db.Create(&item)
		}
		log.Println("Alimentação de dados concluída com sucesso.")
	}
}

func MigrateExistingTenantsToUsers(db *gorm.DB) {
	var tenants []models.Tenant
	if err := db.Find(&tenants).Error; err != nil {
		log.Printf("Erro ao buscar tenants para migração de usuários: %v", err)
		return
	}

	for _, tenant := range tenants {
		var userCount int64
		db.Model(&models.Usuario{}).Where("tenant_id = ?", tenant.ID).Count(&userCount)
		if userCount == 0 {
			email := fmt.Sprintf("admin@%s.com", tenant.Slug)
			var emailCount int64
			db.Model(&models.Usuario{}).Where("email = ?", email).Count(&emailCount)
			if emailCount > 0 {
				email = fmt.Sprintf("admin_%d@%s.com", tenant.ID, tenant.Slug)
			}

			usuario := models.Usuario{
				TenantID:  tenant.ID,
				Nome:      "Administrador " + tenant.Nome,
				Email:     email,
				SenhaHash: tenant.SenhaHash,
				Role:      "owner",
				Ativo:     true,
			}
			if err := db.Create(&usuario).Error; err != nil {
				log.Printf("Erro ao criar usuário migrado para tenant %s: %v", tenant.Slug, err)
			} else {
				log.Printf("Usuário administrador migrado e criado para tenant %s com e-mail %s", tenant.Slug, email)
			}
		}
	}
}
