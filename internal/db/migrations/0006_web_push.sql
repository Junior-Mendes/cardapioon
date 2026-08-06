-- Criação da tabela de subscrições do Web Push.
--
-- Cada utilizador administrativo pode subscrever o recebimento de notificações no telemóvel
-- ou tablet, e o navegador gera um par de chaves e endpoint exclusivo. O servidor envia o
-- push assinado para este endpoint para alertar de novas encomendas mesmo com o app fechado.
CREATE TABLE IF NOT EXISTS push_subscriptions (
  id BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
  tenant_id BIGINT UNSIGNED NOT NULL,
  usuario_id BIGINT UNSIGNED NOT NULL,
  endpoint VARCHAR(512) NOT NULL,
  p256dh VARCHAR(256) NOT NULL,
  auth VARCHAR(256) NOT NULL,
  created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
  UNIQUE KEY idx_push_sub_endpoint (endpoint),
  CONSTRAINT fk_push_sub_tenant FOREIGN KEY (tenant_id) REFERENCES tenants (id) ON DELETE CASCADE,
  CONSTRAINT fk_push_sub_usuario FOREIGN KEY (usuario_id) REFERENCES usuarios (id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
