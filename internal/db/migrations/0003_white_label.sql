-- White label: identidade visual por restaurante.
--
-- O storefront tinha a marca da plataforma fixa no HTML ("MenuOnline", ícone "C"), pelo
-- que o cliente final via a marca do SaaS em vez da do restaurante que está a servir.

ALTER TABLE tenants
  ADD COLUMN logo_url      VARCHAR(1000) NULL AFTER nome,
  ADD COLUMN cor_primaria  CHAR(7)       NULL AFTER logo_url,
  ADD COLUMN cor_secundaria CHAR(7)      NULL AFTER cor_primaria,
  -- Assinatura discreta da plataforma no fundo do storefront. Fica activa por omissão e
  -- passa a ser um argumento de venda: desligá-la é uma funcionalidade de plano pago.
  ADD COLUMN mostrar_marca_plataforma TINYINT(1) NOT NULL DEFAULT 1 AFTER cor_secundaria,
  -- Texto curto sob o nome do restaurante no storefront (ex.: "Cozinha tradicional
  -- portuguesa"). Também usado na meta description, que é o que aparece no Google.
  ADD COLUMN descricao_curta VARCHAR(200) NULL AFTER mostrar_marca_plataforma;
