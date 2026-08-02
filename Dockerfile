# Build multi-estágio do Cardápio Online.
#
# Diferenças em relação à versão anterior:
#   - go.mod/go.sum são copiados antes do código, para que a camada de dependências fique
#     em cache e o build não repita o download a cada alteração de código.
#   - `go mod download` em vez de `go mod tidy`: tidy resolve versões em tempo de build, o
#     que torna a imagem não reproduzível e permite que uma dependência mude sem aviso.
#   - `-mod=readonly` faz o build falhar se go.mod estiver desactualizado, em vez de o
#     corrigir silenciosamente.
#   - O processo corre como utilizador sem privilégios.

FROM golang:1.23-alpine AS builder

WORKDIR /app

RUN apk add --no-cache ca-certificates tzdata

COPY go.mod go.sum ./
RUN go mod download && go mod verify

COPY cmd/ ./cmd/
COPY internal/ ./internal/

# -trimpath remove caminhos absolutos do binário; -w -s retiram tabelas de debug.
RUN CGO_ENABLED=0 GOOS=linux go build \
    -mod=readonly -trimpath -ldflags="-w -s" \
    -o /app/servidor ./cmd/api

# --- Imagem final ---
FROM alpine:3.20

RUN apk add --no-cache ca-certificates tzdata wget \
    && addgroup -S cardapio && adduser -S -G cardapio cardapio

WORKDIR /app

COPY --from=builder /app/servidor ./servidor
COPY static/ ./static/

# O directório de uploads e o de configuração do Traefik são escritos em runtime.
RUN mkdir -p /app/static/uploads /traefik_dynamic \
    && chown -R cardapio:cardapio /app /traefik_dynamic

USER cardapio

ENV TZ=Europe/Lisbon \
    GIN_MODE=release \
    UPLOAD_DIR=/app/static/uploads

EXPOSE 8081

# O healthcheck usa /ready, que também verifica a base de dados: um contentor que não
# consegue falar com o MySQL não deve ser considerado saudável.
HEALTHCHECK --interval=30s --timeout=5s --start-period=15s --retries=3 \
    CMD wget --spider -q http://127.0.0.1:8081/ready || exit 1

ENTRYPOINT ["./servidor"]
