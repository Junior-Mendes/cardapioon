-- Observações por linha de encomenda.
--
-- O cliente precisa de poder escrever "sem cebola" ou "bem passado" num prato
-- específico. É a forma mais simples de cobrir o caso mais comum sem construir ainda o
-- sistema completo de modificadores (tamanhos, adicionais), que é trabalho maior.
--
-- A coluna vive na linha da encomenda e não no produto: é uma escolha do cliente naquela
-- compra, não um atributo do prato.
ALTER TABLE itens_pedido
  ADD COLUMN observacoes VARCHAR(280) NULL AFTER nome;

-- Índice para a secção de destaques do menu ("os mais pedidos").
--
-- A consulta soma quantidades por produto nas encomendas recentes de um restaurante. Sem
-- este índice teria de percorrer todas as linhas de encomenda do tenant.
ALTER TABLE itens_pedido
  ADD COLUMN menu_item_id BIGINT UNSIGNED NULL AFTER pedido_id,
  ADD KEY idx_itens_pedido_menu_item (menu_item_id);

-- As linhas existentes não têm o produto de origem registado: guardavam apenas o nome.
-- Ficam a NULL, o que as exclui dos destaques — correcto, porque não é possível saber com
-- certeza a que produto correspondiam depois de o nome poder ter mudado.
