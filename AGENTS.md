# AGENTS.md — guia do projecto para assistentes de código

Este ficheiro é para quem trabalha no código sem ter acompanhado a sua história: outro
modelo, outro programador, ou tu próprio dentro de três meses. Descreve **o que o projecto
é, o que não é, e quais as decisões que não devem ser desfeitas sem intenção**.

Lê a secção "Invariantes" antes de mudar qualquer coisa relacionada com dinheiro,
autenticação ou multi-tenant. São os pontos onde um erro tem consequência real: dinheiro
errado na caixa, ou dados de um restaurante visíveis a outro.

---

## 1. O que é

SaaS multi-tenant de **menu digital para restaurantes em Portugal**. Cada restaurante tem o
seu endereço próprio (`restaurante.dominio.pt` ou domínio próprio) com certificado TLS
automático. O cliente final abre o menu no telemóvel, encomenda, e vai levantar ao balcão.

**Âmbito deliberado do MVP:**

| Existe | Não existe, por decisão |
|---|---|
| Levantamento ao balcão | Entrega ao domicílio |
| Pagamento na caixa (dinheiro ou cartão) | Pagamento na aplicação |
| Menu, encomendas, IVA, white label | Emissão de facturas |
| Aviso em tempo real ao lojista | Modificadores de produto (tamanhos, adicionais) |

**Não implementes entrega nem pagamento online sem instrução explícita.** Foram removidos
de propósito: sem pagamento na aplicação não existe estado de "pago" para falsificar, o que
elimina toda uma classe de fraude. A facturação é feita por software externo do restaurante,
o que evita a exigência legal de certificação pela Autoridade Tributária.

**Mercado: Portugal.** Escreve em **pt-PT**, formata em **EUR**. Não introduzas Pix, Real,
CEP, CPF/CNPJ nem terminologia brasileira. O código nasceu de um projecto brasileiro e ainda
podes encontrar resíduos — corrige-os quando os vires.

Vocabulário: *encomenda* (não "pedido"), *levantamento* (não "retirada"), *morada*,
*telemóvel*, *código postal*, *menu* ou *ementa*, *talão*, *IVA*.

---

## 2. Como correr, testar e publicar

O Go **não está instalado na máquina**; tudo corre em contentores. Há dois wrappers usados
durante o desenvolvimento (recria-os se não existirem):

```bash
# Compilar, formatar, etc.
docker run --rm -v /root/cardapio:/src \
  -v gocache_cardapio:/go/pkg/mod -v gobuildcache_cardapio:/root/.cache/go-build \
  --network crm_default -w /src -e GOFLAGS=-mod=mod -e CGO_ENABLED=0 \
  golang:1.23 go build ./...

# Testes. Exigem MySQL: sem TEST_DB_HOST os testes de isolamento são IGNORADOS.
docker run --rm -v /root/cardapio:/src \
  -v gocache_cardapio:/go/pkg/mod -v gobuildcache_cardapio:/root/.cache/go-build \
  --network crm_default -w /src -e GOFLAGS=-mod=mod -e CGO_ENABLED=0 \
  -e TEST_DB_HOST=... -e TEST_DB_PORT=3306 -e TEST_DB_USER=... \
  -e TEST_DB_PASSWORD=... -e TEST_DB_NAME=cardapio_test \
  golang:1.23 go test ./...

# Lint do frontend. OBRIGATÓRIO: apanha identificadores inexistentes.
docker run --rm -v /root/cardapio:/app -w /app/static/js node:20-alpine \
  sh -c 'npm i -g eslint@8 >/dev/null 2>&1; eslint *.js'

# Publicar
docker compose build && docker compose up -d
```

`gofmt -w .` antes de commitar; o CI falha com ficheiros não formatados.

**Um teste ignorado não é um teste passado.** Se vires `--- SKIP` nos testes de
`internal/handlers`, a base de dados de teste não está configurada e a cobertura de
isolamento de tenant não correu. O CI tem um passo específico que falha nesse caso.

---

## 3. Mapa do código

```
cmd/api/main.go              Arranque, árvore de rotas, graceful shutdown
internal/
  auth/        JWT (HS256) e senhas (bcrypt, com migração do SHA-256 legado)
  config/      Leitura e validação do ambiente. Falha no arranque se faltar o essencial
  db/          Ligação, pool, runner de migrações
  db/migrations/  SQL versionado, aplicado por ordem no arranque
  dinheiro/    Cêntimos como inteiros e extracção de IVA  ← ler antes de tocar em preços
  eventos/     Broker em memória que alimenta o SSE
  handlers/    HTTP. Um ficheiro por área
  imagens/     Descodifica, redimensiona e recodifica uploads
  mail/        SMTP com fallback para log
  middleware/  Autenticação, resolução de tenant, CORS, CSP, rate limit, logging
  models/      Structs GORM
  traefik/     Escreve a configuração dinâmica do encaminhador
  validate/    Slug, domínio, NIF, código postal, telefone, cor, logótipo
static/
  index.html   Landing do SaaS      + js/landing.js
  menu.html    Menu do cliente      + js/menu.js   + css/menu.css  (MOBILE-FIRST)
  order.html   Acompanhar encomenda + js/order.js
  admin.html   Painel do lojista    + js/admin.js  + css/admin.css
  plataforma.html  Painel do dono do SaaS + js/plataforma.js + css/plataforma.css
  js/common.js   API partilhada: esc, api, Sessao, formatCents, SegGroup
  js/alertas.js  Som, título do separador, stream SSE, PWA
  js/branding.js White label no storefront
  sw.js, manifest.json  PWA do painel
```

Os ficheiros JavaScript são **scripts clássicos**, não módulos: comunicam por globais e a
**ordem das tags `<script>` importa** — `common.js` primeiro, sempre.

---

## 4. Invariantes

Quebrar qualquer destas coisas causa dano real. Cada uma tem teste; se um teste destes
falhar, o problema é o teu código, não o teste.

### 4.1 Isolamento entre restaurantes

- Em `/api/admin/*` o `tenant_id` vem **exclusivamente das claims do JWT**. Nunca do
  cabeçalho `Host`, nunca do corpo do pedido, nunca de uma query string.
- Toda a leitura e escrita administrativa usa `middleware.TenantScope(c)`.
- Um `tenant_id` enviado no corpo é ignorado.
- O `Host` serve **apenas** para resolver qual o storefront a mostrar ao cliente.

Historicamente o tenant era resolvido pelo `Host` **antes** do token, e um lojista
autenticado que abrisse o subdomínio de um concorrente operava na conta dele.

### 4.1.1 O painel da plataforma é outro realm de autenticação

O painel do dono do SaaS (`/plataforma`) é a única parte da aplicação que lê os dados de
vários restaurantes. **Não é uma role nova**: é um sistema de contas paralelo.

- Contas em `plataforma_admins`, sessões em `plataforma_refresh_tokens`. Nenhuma das duas
  tem `tenant_id`.
- Os tokens dos dois painéis têm **audiências diferentes** (`admin` e `plataforma`).
  `ParseAccessToken` exige a primeira, `ParsePlataformaToken` a segunda, pelo que cada
  validador rejeita o token do outro na verificação da assinatura — antes de qualquer
  handler correr.
- `RequirePlataforma` **nunca** escreve `tenant_id`, `user_id` nem `role` no contexto. Se um
  handler da plataforma chamar `TenantScope` por engano, o tenant é zero e a query filtra
  por `1 = 0`: ecrã vazio, nunca os dados do restaurante errado.
- Rotas em `/api/plataforma/*`, nunca em `/api/admin/*`.

**Não unifiques as duas funções de parse nem acrescentes um `role: superadmin` a
`usuarios`.** Uma role que ignorasse o escopo de tenant tornaria a invariante 4.1 —
que hoje não tem excepções — condicional a um `if` espalhado por cada handler
administrativo. `TestTokenDeLojistaNaoAbrePainelDaPlataforma` e
`TestTokenDaPlataformaNaoAbreRotasAdministrativas` cobrem as duas direcções.

Minimização de dados: o painel mostra contagens, volumes e estados, mas **não** o nome nem
o telefone dos consumidores finais. O responsável por esses dados é o restaurante.

### 4.2 Dinheiro

- **Todos os montantes são `dinheiro.Cents` (inteiro).** Nunca `float64`.
- Os preços introduzidos chegam como **texto** (`preco_texto: "12,50"`) e são convertidos
  com `dinheiro.Parse`. Três casas decimais são **recusadas**, não truncadas.
- O preço é sempre o **valor final ao consumidor, com IVA incluído** — é o que a lei
  portuguesa exige que seja afixado.
- O IVA **extrai-se**, não se soma: `iva = valor × taxa / (100 + taxa)`. Multiplicar por
  1,23 é o erro clássico e inflaciona o que o cliente paga.
- A base obtém-se por **subtracção** (`bruto - iva`), nunca por uma segunda divisão.
- Numa encomenda, o IVA é extraído do **total de cada taxa**, não linha a linha.
- **`base + iva == total`, sempre.** `CreateOrder` aborta a transacção se não fechar.

As colunas `DECIMAL` antigas (`preco`, `valor_total`, …) continuam a ser escritas por
`SincronizarLegado()` só para permitir rollback do binário. **Não as leias em cálculos.**

### 4.3 Taxa de IVA

- A taxa é **por produto**, escolhida pelo estabelecimento. Guardada em pontos base
  (`2300` = 23%).
- Cada linha de encomenda guarda um **snapshot** da taxa: as taxas mudam por Orçamento do
  Estado e uma encomenda antiga tem de reproduzir o imposto que teve.
- **O software não decide a taxa aplicável.** A qualificação do take-away é decisão do
  contabilista do restaurante.

### 4.4 Segurança

- Senhas em **bcrypt** (custo 12). Hashes SHA-256 legados são aceites no login e
  re-hashados de forma transparente.
- O rastreio público de encomendas usa um **token opaco** (UUID), nunca o id sequencial.
  Iterar ids extraía nome, telefone e pagamento de todas as encomendas da plataforma.
- Slug e domínio passam por `validate` **antes** de chegarem ao `traefik.Writer`: entram num
  caminho de ficheiro e no corpo de um YAML.
- Um domínio próprio só é encaminhado depois de a propriedade ser provada por registo TXT.
- Erros internos **nunca** vão para o cliente em produção; usa `h.erroInterno(...)`.
- Escapa tudo o que vem do servidor antes de entrar em `innerHTML`, com `esc()`/`escAttr()`.

---

## 5. Armadilhas já encontradas

Cada uma destas custou tempo. Estão aqui para não custarem outra vez.

**Etiqueta `default:` do GORM omite valores zero.** Com `gorm:"default:1300"`, o GORM
**omite o campo do INSERT** quando o valor é o zero da linguagem, e a base aplica o seu
default. Resultado real: escolher "Isento" (taxa 0) gravava 13%. O mesmo com `bool`:
desligar uma opção seria omitido e voltaria a `true`. **Não uses `default:` em campos onde o
zero seja um valor legítimo**; define os valores explicitamente no código.

**`Updates(map)` do GORM altera a struct em memória.** Ler `p.Status` depois da escrita
devolve o valor **novo**. Guarda o anterior antes de escrever, ou o registo de auditoria
grava `preparando -> preparando`.

**O DSN do MySQL não é URL-encoded.** `url.QueryEscape` na senha faz os `%XX` chegarem
como parte da senha e o MySQL responde `Access denied` com a senha correcta. Usa
`mysql.Config` + `FormatDSN()`. Isto impedia o servidor de arrancar.

**`WriteTimeout` mata streams.** O servidor tem 60 segundos, o que é correcto para pedidos
normais e fatal para SSE. O handler de eventos remove o prazo **só naquela ligação**, com
`http.NewResponseController`.

**`EventSource` não aceita cabeçalhos.** O token vai no `Authorization`, logo o cliente usa
`fetch` com `ReadableStream`. Não mudes para `EventSource` "para simplificar": obrigaria a
pôr o token na query string, onde fica em registos de acesso.

**A CSP não permite `script-src 'unsafe-inline'`.** Nada de `onclick=` no HTML nem blocos
`<script>` inline — usa `addEventListener`. O formulário de registo da landing esteve
completamente inoperacional por isso, sem qualquer erro visível.

**iOS amplia campos com `font-size` abaixo de 16px** e não volta atrás. Já corrigido; não
reduzas o tamanho de fonte dos inputs no storefront.

**`node --check` só valida sintaxe.** Uma variável nunca declarada passa e explode em
runtime — foi assim que a submissão de encomendas ficou silenciosamente quebrada. **Corre o
ESLint.**

**Rota nova em `main.go` tem de ser acrescentada a `montarRotas` no teste de isolamento**
(`internal/handlers/isolamento_test.go`), ou fica sem cobertura sem nada falhar.

**Estado em memória impede várias réplicas.** O broker de eventos, o rate limiter e o cache
de estado do storefront vivem no processo. Ao escalar horizontalmente, passam a precisar de
um canal partilhado (Redis, NATS).

**Uma rota nova do Traefik demora ~14 segundos a ficar utilizável** (detecção do ficheiro +
emissão do certificado). Nessa janela não é possível servir uma página de espera no
subdomínio — sem rota o pedido não chega à aplicação, sem certificado o browser falha antes
do HTTP. Daí `GET /api/admin/storefront/status`, sondado pelo painel.

---

## 6. Dados

Dez tabelas. As migrações em `internal/db/migrations/` são a fonte de verdade e correm por
ordem no arranque; `schema_migrations` registra o que já foi aplicado.

| Tabela | Papel |
|---|---|
| `tenants` | Restaurante: slug, domínio, identidade visual, métodos de pagamento, taxa de IVA por omissão |
| `usuarios` | Contas por restaurante, com `role` (`owner`/`admin`/`gerente`/`funcionario`) |
| `menu_items` | Produtos: preço em cêntimos, taxa de IVA, disponibilidade |
| `pedidos` | Encomendas: total, base e IVA em cêntimos, token público, chave de idempotência |
| `itens_pedido` | Linhas: snapshot de preço e taxa, observações, ligação ao produto |
| `pedido_iva` | Decomposição por taxa, uma linha por taxa presente |
| `refresh_tokens` | Sessões, com rotação e detecção de reutilização |
| `password_resets` | Tokens de uso único, guardados como hash |
| `audit_logs` | Acções administrativas sensíveis |
| `plataforma_admins` | Contas de quem opera o SaaS. Sem `tenant_id`, de propósito (ver 4.1.1) |
| `plataforma_refresh_tokens` | Sessões do painel da plataforma, com a mesma rotação |
| `schema_migrations` | Controlo de migrações |

Ao criar uma migração: numera em sequência, escreve-a **idempotente quando possível**, e
usa **um `ALTER TABLE` por tabela** (o MySQL 8 tem DDL atómico por statement, o que torna
cada um tudo-ou-nada).

---

## 7. API

Público, sem autenticação:

```
GET   /api/tenant/detect                Que restaurante corresponde a este domínio
GET   /api/:slug/public-menu            Menu, identidade visual, destaques
GET   /api/public-menu                  Idem, resolvido pelo Host
POST  /api/:slug/pedidos                Criar encomenda  (usa Idempotency-Key)
GET   /api/encomendas/:token            Acompanhar, por token opaco
POST  /api/tenant/registrar | login | refresh | logout
POST  /api/tenant/esqueci-senha | redefinir-senha
```

Autenticado com `Authorization: Bearer <jwt>`:

```
GET   /api/admin/config                 Configuração do restaurante
GET   /api/admin/storefront/status      O endereço já responde?
GET   /api/admin/eventos                Stream SSE de acontecimentos
POST  /api/admin/conta/alterar-senha
PUT   /api/admin/config                 (admin+)  nome, NIF, pagamentos, identidade visual
POST  /api/admin/config/dominio         (admin+)  pedir domínio próprio → devolve TXT
POST  /api/admin/config/dominio/verificar (admin+)
POST  /api/admin/upload                 (gerente+) imagem, ?uso=produto|logo
GET   /api/admin/cardapio               (gerente+)
POST  /api/admin/cardapio               (gerente+)
PUT   /api/admin/cardapio/:id           (gerente+)
PATCH /api/admin/cardapio/:id/disponibilidade  (gerente+)  pausar num toque
DELETE /api/admin/cardapio/:id          (gerente+)
GET   /api/admin/pedidos                (funcionario+) paginado
PUT   /api/admin/pedidos/:id/status     (funcionario+)
GET   /api/admin/usuarios               (admin+)
POST  /api/admin/usuarios               (admin+)
DELETE /api/admin/usuarios/:id          (admin+)
```

Hierarquia de papéis: `funcionario` < `gerente` < `admin` < `owner`. Só um `owner` pode
criar outro `owner`.

Painel da plataforma (`Authorization: Bearer <jwt de audiência "plataforma">`). Realm de
autenticação separado — ver 4.1.1:

```
POST  /api/plataforma/login | refresh | logout
GET   /api/plataforma/eu
POST  /api/plataforma/conta/alterar-senha
GET   /api/plataforma/resumo                  Indicadores de toda a plataforma
GET   /api/plataforma/restaurantes            ?q=&estado=&ordem=&pagina=&por_pagina=
GET   /api/plataforma/restaurantes/:id        Detalhe, equipa e métricas
PATCH /api/plataforma/restaurantes/:id/estado Suspender ou reactivar
POST  /api/plataforma/restaurantes/:id/recuperacao  Link de reset para o proprietário
GET   /api/plataforma/auditoria               ?tenant_id=&acao=
```

`ordem` aceita `recentes|nome|volume|encomendas|actividade` **por lista branca**: o valor
entra numa cláusula `ORDER BY`, que não aceita parâmetros preparados.

Suspender um restaurante põe `ativo = false`, apaga a rota do Traefik e revoga os refresh
tokens da equipa. O storefront passa a dar 404 (`porSlug` filtra por `ativo`) e o login
responde «conta suspensa». **É reversível de propósito**: apagar os dados de um cliente por
uma factura em atraso destrói o histórico de IVA que ele é obrigado a conservar.

Estados de encomenda, com transições restritas:
`pendente → preparando → pronto → finalizado`, e `cancelado` a partir de qualquer activo.
Saltar etapas ou reabrir uma encomenda terminada devolve 409.

Eventos SSE: `encomenda_nova`, `encomenda_estado`, e comentários `: ping` a cada 20s.

---

## 8. Convenções

**Escreve em português.** Identificadores, comentários, mensagens de erro e texto de
interface. O código existente é consistente nisto; misturar idiomas torna a base ilegível.

**Comenta o *porquê*, não o *quê*.** O código já diz o que faz. O comentário existe para
explicar a razão de uma escolha não óbvia, ou o que acontece se for desfeita. Se um
comentário parafraseia a linha seguinte, apaga-o.

**Mensagens de erro para o lojista, não para o programador.** «Mantenha pelo menos um método
de pagamento activo, ou não poderá cobrar as encomendas» diz o que fazer; «invalid state»
não.

**Validação em dois lados.** O cliente valida para dar resposta imediata; o servidor valida
porque é a autoridade. Nunca só num.

**Mobile-first apenas em `menu.css`**, que é o ecrã do cliente e é usado no telemóvel: as
regras sem media query são as do telemóvel e as `min-width` só acrescentam.
`admin.css` é desktop-first de propósito — o lojista usa computador ou tablet ao balcão.
`plataforma.css` também: é uma consola de tabelas, usada num computador.
Não inviertas nenhum dos três sem intenção.

---

## 9. Estado e próximos passos

Em produção. Sete migrações aplicadas, lint limpo, e **90 funções de teste**: auth 12,
dinheiro 11, handlers 36, imagens 10, validate 12, traefik 6, db 3. (O total de linhas
`--- PASS` é maior, porque alguns testes têm subtestes.)

O painel da plataforma (`/plataforma`) cobre ver e gerir clientes: indicadores, listagem com
métricas, detalhe, suspensão e reactivação, link de recuperação para o lojista, e leitura
transversal da auditoria.

A seguir, por ordem de valor:

1. **Web Push** — o PWA já está instalado e habilita-o. Pede a permissão **só quando houver
   algo para notificar**: quem recusa não volta a ser perguntado.
2. **Horário de funcionamento** — actualmente aceitam-se encomendas às 4h da manhã.
3. **Impressão de talão** para impressora térmica.
4. **WhatsApp via Evolution API** — dispensa a verificação na Meta, mas é via não oficial:
   usa um número dedicado, porque há risco de bloqueio.
5. **Modificadores de produto** (tamanhos, adicionais) — impeditivo para pizzarias.
6. **Billing de assinatura** — é o que torna isto num negócio. O painel da plataforma já dá
   a suspensão manual, que é a alavanca de cobrança; falta o plano, o estado da subscrição e
   a ligação ao processador de pagamentos.
7. **Migração para remover as colunas `DECIMAL` legadas**, quando a estabilidade o permitir.

Deliberadamente **fora** do painel da plataforma, e não por esquecimento: apagar
restaurantes (a suspensão é reversível, apagar destrói o histórico de IVA do cliente) e
entrar na conta de um lojista por impersonação (útil no suporte, mas é uma via de acesso aos
dados dos consumidores que hoje não existe; se for feita, tem de ser consentida pelo
lojista, limitada no tempo e auditada).

Dívida técnica conhecida: a árvore de rotas está duplicada entre `main.go` e o teste de
isolamento; `db.DB` global ainda existe a par da injecção por `Deps`; os ficheiros estáticos
não têm hash no nome, o que limita o cache.

**Dependem de acção humana, não de código:** configurar SMTP (sem ele o reset de senha fica
no log em vez de chegar ao utilizador), confirmar a taxa de IVA do take-away com um
contabilista, e completar `static/privacidade.html` com os dados da entidade responsável.
