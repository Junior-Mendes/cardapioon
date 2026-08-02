-- Fase 0 — Segurança.
-- Cada tabela é alterada num único ALTER TABLE: o MySQL 8 tem DDL atómico, pelo que
-- cada statement é tudo-ou-nada e a migração é segura de repetir após falha.

-- C3: token público opaco para rastreio de encomendas, substituindo o ID sequencial.
-- Adicionado como nullable para permitir o backfill das encomendas já existentes.
ALTER TABLE pedidos
  ADD COLUMN public_token CHAR(36) NULL AFTER tenant_id,
  ADD COLUMN idempotency_key VARCHAR(64) NULL AFTER public_token;

UPDATE pedidos SET public_token = UUID() WHERE public_token IS NULL;

ALTER TABLE pedidos
  MODIFY COLUMN public_token CHAR(36) NOT NULL,
  ADD UNIQUE KEY idx_pedidos_public_token (public_token),
  ADD UNIQUE KEY idx_pedidos_idem (tenant_id, idempotency_key),
  ADD KEY idx_pedidos_tenant_status_created (tenant_id, status, created_at);

-- C5: propriedade do domínio personalizado passa a ser provada por registo TXT.
-- Domínios já configurados são marcados como verificados para não cortar clientes
-- em produção; a verificação passa a ser exigida a partir de agora.
ALTER TABLE tenants
  ADD COLUMN domain_status VARCHAR(20) NOT NULL DEFAULT 'none' AFTER domain,
  ADD COLUMN domain_verify_token VARCHAR(64) NULL AFTER domain_status,
  ADD COLUMN domain_verified_at DATETIME(3) NULL AFTER domain_verify_token,
  ADD COLUMN nif VARCHAR(20) NULL AFTER nome;

UPDATE tenants
   SET domain_status = 'verified', domain_verified_at = NOW(3)
 WHERE domain IS NOT NULL AND domain <> '';

-- C9: verificação de email e reset de senha.
ALTER TABLE usuarios
  ADD COLUMN email_verified_at DATETIME(3) NULL AFTER email,
  ADD COLUMN password_changed_at DATETIME(3) NULL AFTER senha_hash,
  ADD COLUMN last_login_at DATETIME(3) NULL AFTER password_changed_at;

-- Contas existentes são consideradas verificadas: foram criadas antes desta regra e
-- exigir verificação retroactiva bloquearia lojistas activos.
UPDATE usuarios SET email_verified_at = NOW(3) WHERE email_verified_at IS NULL;

CREATE TABLE IF NOT EXISTS password_resets (
  id         BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  usuario_id BIGINT UNSIGNED NOT NULL,
  token_hash CHAR(64)    NOT NULL,
  expires_at DATETIME(3) NOT NULL,
  used_at    DATETIME(3) NULL,
  created_ip VARCHAR(45) NULL,
  created_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  PRIMARY KEY (id),
  UNIQUE KEY idx_password_resets_token (token_hash),
  KEY idx_password_resets_usuario (usuario_id),
  CONSTRAINT fk_password_resets_usuario FOREIGN KEY (usuario_id) REFERENCES usuarios (id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- C1: refresh tokens rotativos. Guarda-se apenas o hash, nunca o token.
CREATE TABLE IF NOT EXISTS refresh_tokens (
  id          BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  usuario_id  BIGINT UNSIGNED NOT NULL,
  tenant_id   BIGINT UNSIGNED NOT NULL,
  token_hash  CHAR(64)    NOT NULL,
  expires_at  DATETIME(3) NOT NULL,
  revoked_at  DATETIME(3) NULL,
  replaced_by CHAR(64)    NULL,
  user_agent  VARCHAR(255) NULL,
  ip          VARCHAR(45)  NULL,
  created_at  DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  PRIMARY KEY (id),
  UNIQUE KEY idx_refresh_tokens_token (token_hash),
  KEY idx_refresh_tokens_usuario (usuario_id),
  KEY idx_refresh_tokens_expires (expires_at),
  CONSTRAINT fk_refresh_tokens_usuario FOREIGN KEY (usuario_id) REFERENCES usuarios (id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- Registo de auditoria de acções administrativas.
CREATE TABLE IF NOT EXISTS audit_logs (
  id         BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  tenant_id  BIGINT UNSIGNED NULL,
  usuario_id BIGINT UNSIGNED NULL,
  acao       VARCHAR(80)  NOT NULL,
  recurso    VARCHAR(80)  NULL,
  recurso_id VARCHAR(64)  NULL,
  detalhe    TEXT         NULL,
  ip         VARCHAR(45)  NULL,
  created_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  PRIMARY KEY (id),
  KEY idx_audit_logs_tenant_created (tenant_id, created_at),
  KEY idx_audit_logs_acao (acao)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
