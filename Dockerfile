# Multi-stage Dockerfile para Go (Cardápio Online)
FROM golang:1.20-alpine AS builder

WORKDIR /app

# Copia código fonte
COPY . .

# Baixa dependências e gera o go.sum
RUN go mod tidy

# Compila o binário otimizado
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-w -s" -o main ./cmd/api/main.go

# Estágio final de execução
FROM alpine:latest

RUN apk --no-cache add ca-certificates tzdata

WORKDIR /root/

# Copia o binário do builder
COPY --from=builder /app/main .
COPY --from=builder /app/static ./static

EXPOSE 8081

CMD ["./main"]
