# 🍽️ Cardápio Online - Plataforma Multi-Tenant SaaS

Este projeto é um sistema SaaS de **Cardápio Online Multi-Tenant** de alta performance, projetado para permitir que restaurantes gerenciem seus próprios cardápios e recebam pedidos diretamente via WhatsApp ou painel de controle. 

O sistema foi arquitetado para suportar **subdomínios wildcard** (ex: `restaurante.topautomacaojr.top`) e **domínios customizados** de clientes (ex: `www.pizzariadojoao.com.br`) com provisionamento automático de certificados SSL (Let's Encrypt) gerenciado via **Traefik File Provider**.

---

## 🚀 Principais Funcionalidades

- **Multi-Tenant Arquitetura Única (Single Database, Multi-Tenant)**: Separação lógica de dados usando escopos de consulta no banco de dados.
- **Domínios Customizados & Subdomínios Wildcard**: Roteamento dinâmico automático com suporte a SSL.
- **Redirecionamento Inteligente na Raiz (`/`)**:
  - Acessos pelo domínio principal (`topautomacaojr.top`) exibem a Landing Page oficial com formulário de cadastro.
  - Acessos por subdomínios ou domínios de clientes são automaticamente redirecionados (301) para `/menu` (storefront).
- **Gestão de Cardápio (Painel do Lojista)**: Cadastro de produtos, categorias, preços com desconto, imagens e controle de disponibilidade.
- **Gestão de Pedidos**: Recepção de pedidos em tempo real no painel administrativo, alteração de status e integração de envio/rastreamento.
- **Configurações de Pagamento**: Ativação dinâmica de Pix, Cartão de Crédito, Cartão de Débito e Dinheiro.
- **Controle de Usuários por Lojista**: Possibilidade de criar novos usuários administrativos dentro do restaurante com diferentes papéis.

---

## 🛠️ Stack Tecnológica

- **Backend**: Go 1.20 (Framework Gin Gonic, ORM GORM)
- **Banco de Dados**: MySQL (Auto-Migrações & Seeders automáticos)
- **Frontend**: HTML5, Vanilla JavaScript, CSS3 Premium (Visual moderno com Glassmorphism e responsividade)
- **Proxy Reverso & Edge Router**: Traefik v2.10 (SSL automatizado via Let's Encrypt)
- **Containerização**: Docker & Docker Compose

---

## 📁 Estrutura do Projeto

```text
/root/cardapio
├── cmd/
│   └── api/
│       └── main.go                 # Ponto de entrada (Bootstrap, rotas e inicialização)
├── internal/
│   ├── config/
│   │   └── config.go               # Leitura de variáveis de ambiente
│   ├── db/
│   │   └── mysql.go                # Conexão GORM, migrações e seeders
│   ├── handlers/
│   │   ├── helper.go               # Resolvedor de inquilino compartilhado
│   │   ├── menu.go                 # Métodos de gerenciamento do cardápio
│   │   ├── order.go                # Criação e processamento de pedidos
│   │   └── tenant.go               # Registro, login, configs e geração de Traefik yml
│   ├── middleware/
│   │   └── tenant.go               # Middleware de isolamento de inquilinos (Tenant Context & Scope)
│   └── models/
│       ├── menu.go                 # Modelos do cardápio
│       ├── order.go                # Modelos de pedidos e itens
│       ├── tenant.go               # Modelo de restaurantes (Tenant)
│       └── usuario.go              # Modelo de usuários e permissões
├── static/                         # Frontend do app (Arquivos estáticos)
│   ├── admin.html / js / css       # Painel administrativo do lojista
│   ├── index.html                  # Landing Page / Cadastro do SaaS
│   ├── menu.html / js / css        # Storefront / Cardápio público do restaurante
│   └── order.html / js / css       # Página de rastreamento do pedido
├── traefik_dynamic/                # Arquivos YML dinâmicos gerados pelo Go para o Traefik
├── docker-compose.yml              # Definição dos containers (API e Traefik)
├── Dockerfile                      # Build multi-stage do binário Go
└── .env                            # Variáveis de ambiente locais
```

---

## ⚙️ Configuração de Ambiente (`.env`)

Crie um arquivo `.env` na raiz do projeto seguindo as chaves de configuração abaixo:

```ini
# Configurações do Banco de Dados MySQL na OCI
DB_HOST=10.0.0.143
DB_PORT=3306
DB_USER=seu_usuario
DB_PASSWORD="sua_senha_segura"
DB_NAME=cardapio_online

# Configurações de Porta do Servidor
PORT=8081
GIN_MODE=debug # Mude para 'release' em produção
MAIN_DOMAIN=topautomacaojr.top
```

---

## 🔄 Fluxo de Roteamento Dinâmico (Traefik File Provider)

Para permitir a geração automática de certificados SSL sem precisar reiniciar o proxy reverso a cada novo cliente cadastrado, implementamos a integração via **Traefik File Provider**:

1. **Cadastro**: Quando o cliente se cadastra na Landing Page (ex: definindo o slug `testejr`), o backend salva o registro no MySQL.
2. **Geração do Arquivo**: O backend grava dinamicamente o arquivo `/traefik_dynamic/testejr.yml`.
3. **Escuta Ativa**: O Traefik detecta a mudança instantaneamente (`watch=true`) e cria uma nova rota apontando para a API Go.
4. **Resolução de SSL**: O Traefik faz o desafio Let's Encrypt (HTTP challenge) para o subdomínio e emite o certificado SSL automaticamente.
5. **Acesso**: O cliente pode imediatamente acessar `https://testejr.topautomacaojr.top/menu` de forma segura.

### Exemplo de Configuração Gerada (`traefik_dynamic/testejr.yml`):
```yaml
http:
  routers:
    router-testejr:
      rule: "Host(`testejr.topautomacaojr.top`) || Host(`www.testejr.topautomacaojr.top`)"
      entryPoints:
        - websecure
      tls:
        certResolver: myresolver
      service: service-testejr

  services:
    service-testejr:
      loadBalancer:
        servers:
          - url: "http://cardapio_online_api:8081"
```

---

## 📡 Configuração de DNS para Clientes (B2B)

Como dono do SaaS, você não precisa fazer nenhuma alteração de DNS para cada cliente que usar o subdomínio gratuito.

### 1. Subdomínios Wildcard (Grátis):
Basta configurar uma única entrada do tipo **A** ou **CNAME** wildcard no painel da sua hospedagem DNS (como Cloudflare) apontando para o IP do seu servidor:
- **Tipo**: `A`
- **Nome/Host**: `*`
- **Destino/IP**: `[IP_DO_SEU_SERVIDOR]`

Isso garante que qualquer subdomínio (como `restaurante.topautomacaojr.top`) aponte automaticamente para a sua VPS.

### 2. Domínios Customizados (Clientes Próprios):
Quando um cliente deseja usar seu próprio domínio (ex: `pizzariadojoao.com.br`), oriente-o a criar as seguintes entradas no painel DNS dele:
- **Entrada Principal (A)**:
  - **Tipo**: `A`
  - **Nome/Host**: `@`
  - **Destino**: `[IP_DO_SEU_SERVIDOR]`
- **Entrada CNAME (www)**:
  - **Tipo**: `CNAME`
  - **Nome/Host**: `www`
  - **Destino**: `topautomacaojr.top`

Dentro do painel administrativo do lojista, ele poderá salvar o domínio dele e clicar em **"Validar DNS"**. O backend irá checar via `net.LookupIP` se os apontamentos foram feitos corretamente antes de habilitar a rota.

---

## 🐳 Executando a Aplicação localmente ou em Produção

Certifique-se de que possui o **Docker** e o **Docker Compose** instalados na máquina.

1. **Clone o repositório** para sua VPS ou ambiente local.
2. **Configure o arquivo `.env`** com as credenciais do banco de dados e domínio.
3. **Execute o comando de inicialização**:
   ```bash
   docker compose down && docker compose up --build -d
   ```
4. **Verifique os Logs**:
   - Para inspecionar os logs do backend: `docker logs cardapio_online_api -f`
   - Para inspecionar o roteamento e geração de certificados SSL no Traefik: `docker logs cardapio_traefik -f`

O sistema irá inicializar a API Go na porta `8081` (interna), mapear as pastas estáticas e de uploads e conectar com o banco. O Traefik escutará nas portas `80` e `443` para gerenciar todo o tráfego de entrada.

# cardapioon
