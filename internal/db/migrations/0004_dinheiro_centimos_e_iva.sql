-- Dinheiro em cêntimos e IVA por produto.
--
-- Os montantes eram DECIMAL(10,2) lidos para float64 em Go. O DECIMAL na base é exacto,
-- mas o float64 na aplicação não: somar dez linhas de 0,10 € não dá exactamente 1,00 €, e
-- a diferença aparece como um cêntimo no total. A lei portuguesa exige que o preço afixado
-- seja o preço final exacto que o consumidor paga, pelo que essa divergência é uma falha
-- de conformidade e não apenas um detalhe.
--
-- Passa tudo a BIGINT em cêntimos. A conversão dos dados existentes é feita com
-- ROUND(valor * 100), que é exacta porque a origem é DECIMAL com duas casas.

-- --- Produtos ---
ALTER TABLE menu_items
  ADD COLUMN preco_cents          BIGINT NOT NULL DEFAULT 0 AFTER preco,
  ADD COLUMN preco_desconto_cents BIGINT NOT NULL DEFAULT 0 AFTER preco_desconto,
  -- Taxa de IVA em pontos base: 2300 = 23%, 1300 = 13%, 600 = 6%.
  -- Inteiro, e não percentagem decimal, pela mesma razão do dinheiro. O valor por omissão
  -- é a taxa intermédia, mas a escolha é do estabelecimento, produto a produto.
  ADD COLUMN taxa_iva_bp          INT    NOT NULL DEFAULT 1300 AFTER preco_desconto_cents;

UPDATE menu_items SET
  preco_cents          = ROUND(preco * 100),
  preco_desconto_cents = ROUND(COALESCE(preco_desconto, 0) * 100);

-- --- Encomendas ---
ALTER TABLE pedidos
  ADD COLUMN valor_total_cents BIGINT NOT NULL DEFAULT 0 AFTER valor_total,
  ADD COLUMN troco_para_cents  BIGINT NOT NULL DEFAULT 0 AFTER troco_para,
  -- Base e IVA totais, guardados no momento da encomenda. As taxas mudam por Orçamento do
  -- Estado; uma encomenda antiga tem de continuar a reproduzir o imposto que teve.
  ADD COLUMN base_cents        BIGINT NOT NULL DEFAULT 0 AFTER valor_total_cents,
  ADD COLUMN iva_cents         BIGINT NOT NULL DEFAULT 0 AFTER base_cents;

UPDATE pedidos SET
  valor_total_cents = ROUND(valor_total * 100),
  troco_para_cents  = ROUND(COALESCE(troco_para, 0) * 100);

-- --- Linhas de encomenda ---
ALTER TABLE itens_pedido
  ADD COLUMN preco_unitario_cents BIGINT NOT NULL DEFAULT 0 AFTER preco_unitario,
  -- Snapshot da taxa aplicada a esta linha.
  ADD COLUMN taxa_iva_bp          INT    NOT NULL DEFAULT 0 AFTER preco_unitario_cents,
  ADD COLUMN total_linha_cents    BIGINT NOT NULL DEFAULT 0 AFTER taxa_iva_bp;

UPDATE itens_pedido SET
  preco_unitario_cents = ROUND(preco_unitario * 100),
  total_linha_cents    = ROUND(preco_unitario * 100) * quantidade;

-- --- Resumo de IVA por taxa, por encomenda ---
-- Uma linha por taxa presente na encomenda. É esta a decomposição que o restaurante usa
-- para reconciliar com o software de facturação que já tem.
CREATE TABLE IF NOT EXISTS pedido_iva (
  id          BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  pedido_id   BIGINT UNSIGNED NOT NULL,
  taxa_iva_bp INT    NOT NULL,
  bruto_cents BIGINT NOT NULL,
  base_cents  BIGINT NOT NULL,
  iva_cents   BIGINT NOT NULL,
  PRIMARY KEY (id),
  UNIQUE KEY idx_pedido_iva_taxa (pedido_id, taxa_iva_bp),
  CONSTRAINT fk_pedido_iva_pedido FOREIGN KEY (pedido_id) REFERENCES pedidos (id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- --- Taxa por omissão do estabelecimento ---
-- Poupa ao lojista escolher a taxa em cada produto quando a maioria tem a mesma.
ALTER TABLE tenants
  ADD COLUMN taxa_iva_omissao_bp INT NOT NULL DEFAULT 1300 AFTER nif;

-- As colunas DECIMAL antigas ficam por remover numa migração posterior, depois de a
-- aplicação estar em produção a escrever apenas nas novas. Removê-las agora impediria um
-- rollback para a versão anterior do binário.
