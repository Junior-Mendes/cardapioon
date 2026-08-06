-- Painel da plataforma: as contas de quem opera o SaaS, não de quem opera um restaurante.
--
-- Tabela própria, e não uma role nova em `usuarios`, por causa do isolamento entre
-- restaurantes: toda a conta em `usuarios` pertence a um tenant, e o `tenant_id` viaja
-- assinado no access token. Um "super-utilizador" nessa tabela precisaria de um tenant_id
-- fictício e transformaria o isolamento — que hoje é uma regra sem excepções — num caso
-- especial espalhado por cada handler administrativo. Aqui não há tenant_id nenhum.
--
-- A sessão também é separada: as audiências dos tokens são diferentes (ver
-- internal/auth/jwt.go), pelo que um token de lojista não abre o painel da plataforma nem
-- o contrário. Daí a tabela de refresh tokens própria: a existente tem chave estrangeira
-- para `usuarios`.
--
-- Migração puramente aditiva: só cria tabelas novas. Nenhuma tabela em uso é alterada,
-- para que aplicá-la não possa afectar o funcionamento do produto já em produção.
CREATE TABLE IF NOT EXISTS plataforma_admins (
  id                  BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  nome                VARCHAR(150) NOT NULL,
  email               VARCHAR(150) NOT NULL,
  senha_hash          VARCHAR(255) NOT NULL,
  ativo               TINYINT(1)   NOT NULL DEFAULT 1,
  password_changed_at DATETIME(3)  NULL,
  last_login_at       DATETIME(3)  NULL,
  created_at          DATETIME(3)  NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  updated_at          DATETIME(3)  NULL,
  PRIMARY KEY (id),
  UNIQUE KEY idx_plataforma_admins_email (email)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- Mesma rotação obrigatória dos refresh tokens dos lojistas: cada uso emite um token novo
-- e marca o anterior como substituído, de modo a que a reapresentação de um token já gasto
-- seja detectável e indique roubo de sessão.
CREATE TABLE IF NOT EXISTS plataforma_refresh_tokens (
  id          BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  admin_id    BIGINT UNSIGNED NOT NULL,
  token_hash  CHAR(64)     NOT NULL,
  expires_at  DATETIME(3)  NOT NULL,
  revoked_at  DATETIME(3)  NULL,
  replaced_by CHAR(64)     NULL,
  user_agent  VARCHAR(255) NULL,
  ip          VARCHAR(45)  NULL,
  created_at  DATETIME(3)  NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  PRIMARY KEY (id),
  UNIQUE KEY idx_plataforma_refresh_token (token_hash),
  KEY idx_plataforma_refresh_admin (admin_id),
  KEY idx_plataforma_refresh_expires (expires_at),
  CONSTRAINT fk_plataforma_refresh_admin FOREIGN KEY (admin_id) REFERENCES plataforma_admins (id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
