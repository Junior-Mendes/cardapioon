-- Baseline: reproduz o schema que o AutoMigrate criou até aqui.
-- Escrita com IF NOT EXISTS para ser aplicável tanto a uma base nova como à base
-- de produção já existente (onde funciona como marcação, sem alterar nada).

CREATE TABLE IF NOT EXISTS tenants (
  id                   BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  nome                 VARCHAR(150) NOT NULL,
  slug                 VARCHAR(50)  NOT NULL,
  ativo                TINYINT(1)   DEFAULT 1,
  senha_hash           VARCHAR(255) NOT NULL,
  pix_ativo            TINYINT(1)   DEFAULT 0,
  pix_chave            VARCHAR(100) DEFAULT NULL,
  cartao_credito_ativo TINYINT(1)   DEFAULT 0,
  cartao_debito_ativo  TINYINT(1)   DEFAULT 0,
  dinheiro_ativo       TINYINT(1)   DEFAULT 0,
  created_at           DATETIME(3)  DEFAULT NULL,
  updated_at           DATETIME(3)  DEFAULT NULL,
  domain               VARCHAR(255) DEFAULT NULL,
  PRIMARY KEY (id),
  UNIQUE KEY idx_tenants_slug (slug),
  UNIQUE KEY idx_tenants_domain (domain)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS usuarios (
  id         BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  tenant_id  BIGINT UNSIGNED NOT NULL,
  nome       VARCHAR(150) NOT NULL,
  email      VARCHAR(150) NOT NULL,
  senha_hash VARCHAR(255) NOT NULL,
  role       VARCHAR(50)  DEFAULT 'admin',
  ativo      TINYINT(1)   DEFAULT 1,
  created_at DATETIME(3)  DEFAULT NULL,
  updated_at DATETIME(3)  DEFAULT NULL,
  PRIMARY KEY (id),
  UNIQUE KEY idx_usuarios_email (email),
  KEY idx_usuarios_tenant_id (tenant_id),
  CONSTRAINT fk_usuarios_tenant FOREIGN KEY (tenant_id) REFERENCES tenants (id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS menu_items (
  id             BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  tenant_id      BIGINT UNSIGNED NOT NULL,
  nome           VARCHAR(150)   NOT NULL,
  descricao      TEXT,
  preco          DECIMAL(10,2)  NOT NULL,
  preco_desconto DECIMAL(10,2)  DEFAULT '0.00',
  desconto_ativo TINYINT(1)     DEFAULT 0,
  categoria      VARCHAR(50)    NOT NULL,
  imagem_url     TEXT,
  disponivel     TINYINT(1)     DEFAULT 1,
  created_at     DATETIME(3)    DEFAULT NULL,
  updated_at     DATETIME(3)    DEFAULT NULL,
  PRIMARY KEY (id),
  KEY idx_menu_items_tenant_id (tenant_id),
  KEY idx_menu_items_categoria (categoria),
  CONSTRAINT fk_menu_items_tenant FOREIGN KEY (tenant_id) REFERENCES tenants (id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS pedidos (
  id                     BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  tenant_id              BIGINT UNSIGNED NOT NULL,
  cliente_nome           VARCHAR(150)  NOT NULL,
  cliente_telefone       VARCHAR(20)   NOT NULL,
  status                 VARCHAR(50)   DEFAULT 'pendente',
  valor_total            DECIMAL(10,2) NOT NULL,
  forma_pagamento        VARCHAR(50)   NOT NULL,
  troco_para             DECIMAL(10,2) DEFAULT '0.00',
  pix_pago               TINYINT(1)    DEFAULT 0,
  cartao_ultimos_digitos VARCHAR(4)    DEFAULT NULL,
  created_at             DATETIME(3)   DEFAULT NULL,
  updated_at             DATETIME(3)   DEFAULT NULL,
  PRIMARY KEY (id),
  KEY idx_pedidos_tenant_id (tenant_id),
  KEY idx_pedidos_status (status),
  KEY idx_pedidos_created_at (created_at),
  CONSTRAINT fk_pedidos_tenant FOREIGN KEY (tenant_id) REFERENCES tenants (id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS itens_pedido (
  id             BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  pedido_id      BIGINT UNSIGNED NOT NULL,
  nome           VARCHAR(150)  NOT NULL,
  quantidade     BIGINT        NOT NULL,
  preco_unitario DECIMAL(10,2) NOT NULL,
  created_at     DATETIME(3)   DEFAULT NULL,
  PRIMARY KEY (id),
  KEY idx_itens_pedido_pedido_id (pedido_id),
  CONSTRAINT fk_pedidos_itens FOREIGN KEY (pedido_id) REFERENCES pedidos (id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
