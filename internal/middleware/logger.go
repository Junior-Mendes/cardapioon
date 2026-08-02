package middleware

import (
	"log/slog"
	"time"

	"cardapio-online/internal/auth"

	"github.com/gin-gonic/gin"
)

// RequestLogger substitui gin.Logger() por logging estruturado.
//
// Cada linha inclui um request_id e o tenant_id, para que um problema reportado por um
// restaurante possa ser isolado nos logs sem ler o tráfego de todos os outros.
func RequestLogger() gin.HandlerFunc {
	return func(c *gin.Context) {
		inicio := time.Now()

		requestID := c.GetHeader("X-Request-ID")
		if requestID == "" {
			requestID = auth.NewOpaqueToken(8)
		}
		c.Set("request_id", requestID)
		c.Header("X-Request-ID", requestID)

		c.Next()

		duracao := time.Since(inicio)
		status := c.Writer.Status()

		atributos := []any{
			"request_id", requestID,
			"metodo", c.Request.Method,
			"caminho", c.Request.URL.Path,
			"status", status,
			"duracao_ms", duracao.Milliseconds(),
			"ip", c.ClientIP(),
		}
		if id := GetTenantID(c); id > 0 {
			atributos = append(atributos, "tenant_id", id)
		}
		if id := GetUserID(c); id > 0 {
			atributos = append(atributos, "usuario_id", id)
		}
		// Query strings e corpos não são registados: contêm dados pessoais de clientes
		// (nome, telefone, morada) e o RGPD exige minimização na recolha.

		switch {
		case status >= 500:
			atributos = append(atributos, "erros", c.Errors.String())
			slog.Error("pedido falhou", atributos...)
		case status >= 400:
			slog.Warn("pedido rejeitado", atributos...)
		default:
			slog.Info("pedido", atributos...)
		}
	}
}
