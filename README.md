# Menu Online — SaaS de menu digital para restaurantes

Plataforma multi-tenant onde cada restaurante tem o seu próprio endereço
(`restaurante.dominio.pt`, ou um domínio próprio) com certificado TLS automático. O cliente
abre o menu no telemóvel, encomenda, e vai levantar ao balcão.

**Mercado: Portugal.** Interface em pt-PT, valores em euros, IVA por produto com o preço
final ao consumidor.

> **A trabalhar no código?** Lê primeiro o [`AGENTS.md`](AGENTS.md). Tem as invariantes que
> não devem ser quebradas, as armadilhas já encontradas, e o mapa do código. Este README é
> só a visão geral.

---

## O que faz

**Para o cliente** — menu por categorias com barra fixa que acompanha o scroll, pesquisa,
detalhe do prato com quantidade e observações («sem cebola»), e acompanhamento da encomenda
por link.

**Para o lojista** — quadro de encomendas em tempo real com aviso sonoro, gestão do menu por
categorias com pausa de um toque, identidade visual própria (logótipo, cores), upload de
fotografias do telemóvel, e painel instalável como aplicação.

**Âmbito do MVP:** levantamento ao balcão com pagamento na caixa. Sem entrega, sem pagamento
na aplicação, sem emissão de facturas — decisões deliberadas, explicadas no `AGENTS.md`.

---

## Stack

Go 1.23 (Gin, GORM) · MySQL 8 · Traefik v3 com Let's Encrypt · HTML, CSS e JavaScript sem
framework · Docker Compose.

Sem dependências de frontend: o JavaScript são scripts clássicos que comunicam por globais,
e não há passo de build para o cliente.

---

## Pôr a correr

```bash
cp .env.example .env
# Preencher DB_*, MAIN_DOMAIN, e gerar o segredo:
#   openssl rand -base64 48   → JWT_SECRET

docker compose build
docker compose up -d
```

O arranque aplica as migrações e escreve as rotas do Traefik para os restaurantes activos.

O contentor corre como utilizador sem privilégios (uid 100). O directório
`traefik_dynamic/` é montado do host e tem de lhe pertencer, ou a aplicação não consegue
publicar rotas:

```bash
chown -R 100:101 traefik_dynamic uploads
```

Depois, cria o primeiro restaurante no formulário da página inicial.

### Variáveis essenciais

| Variável | Para quê |
|---|---|
| `DB_HOST`, `DB_USER`, `DB_PASSWORD`, `DB_NAME` | MySQL |
| `MAIN_DOMAIN` | Domínio do SaaS; os subdomínios dos restaurantes derivam dele |
| `JWT_SECRET` | Assinatura das sessões. Sem ele o servidor não arranca, de propósito |
| `SMTP_HOST`, `MAIL_FROM` | Sem isto o reset de senha fica no log em vez de ser enviado |
| `TRUSTED_PROXIES` | Sem isto o IP do cliente é falsificável e o rate limit contornável |
| `SEED_DEMO_DATA` | Dados de demonstração. Proibido em produção pela configuração |

O ficheiro `.env` não é versionado, e não deve ser: contém a senha da base de dados.

---

## Testar

Os testes usam **MySQL real** — o GORM gera SQL específico do dialecto, e um SQLite em
memória não provaria o mesmo comportamento.

```bash
docker run --rm -v "$PWD":/src --network crm_default -w /src \
  -e GOFLAGS=-mod=mod -e CGO_ENABLED=0 \
  -e TEST_DB_HOST=... -e TEST_DB_USER=... -e TEST_DB_PASSWORD=... \
  -e TEST_DB_NAME=cardapio_test \
  golang:1.23 go test ./...

# Lint do frontend — apanha identificadores inexistentes, que a verificação
# de sintaxe não apanha.
docker run --rm -v "$PWD":/app -w /app/static/js node:20-alpine \
  sh -c 'npm i -g eslint@8 >/dev/null 2>&1; eslint *.js'
```

**Sem `TEST_DB_HOST` os testes de isolamento de tenant são ignorados**, não passados. São a
cobertura mais importante do projecto: verificam que um restaurante não consegue ver nem
alterar os dados de outro. O CI falha se forem ignorados.

81 testes: aritmética de dinheiro e IVA, isolamento entre restaurantes, autenticação,
processamento de imagens, validações portuguesas (NIF, código postal, telefone) e geração da
configuração do Traefik.

---

## Como o encaminhamento funciona

1. Um restaurante registra-se e escolhe o seu endereço (`slug`).
2. O backend escreve `traefik_dynamic/<slug>.yml` de forma atómica.
3. O Traefik detecta o ficheiro (`watch=true`) e cria a rota.
4. No primeiro acesso por HTTPS, o Let's Encrypt emite o certificado.

Entre os passos 2 e 4 passam cerca de catorze segundos, durante os quais o endereço ainda
não responde. O painel mostra esse estado em vez de dar um link que falha — ver
`GET /api/admin/storefront/status`.

Domínios próprios exigem prova de propriedade por registo TXT antes de serem encaminhados.

---

## Documentação

- [`AGENTS.md`](AGENTS.md) — guia para quem trabalha no código: invariantes, armadilhas,
  convenções, API completa.
- `internal/db/migrations/` — a fonte de verdade do esquema, com o motivo de cada alteração.
- Comentários no código explicam **porquê**, não o quê. Vale a pena lê-los antes de mudar
  uma decisão que pareça estranha.
