package middleware

import (
	"errors"
	"net/http"

	"cardapio-online/internal/auth"

	"github.com/gin-gonic/gin"
)

// CtxPlataformaAdminID identifica o administrador da plataforma autenticado.
//
// Chave distinta de CtxUserID de propósito: se um handler da plataforma chamar por engano
// GetUserID ou GetTenantID, obtém zero — e TenantScope, com tenant zero, filtra por
// "1 = 0" e devolve zero linhas. O erro resulta num ecrã vazio, nunca em dados do
// restaurante errado.
const CtxPlataformaAdminID = "plataforma_admin_id"

// RequirePlataforma valida o token do painel da plataforma.
//
// Nunca escreve CtxTenantID, CtxUserID nem CtxRole: as rotas da plataforma não operam
// dentro de um restaurante, e não haver tenant no contexto é o que impede que um handler
// da plataforma seja reutilizado por acidente como se fosse administrativo.
func RequirePlataforma(ts *auth.TokenService) gin.HandlerFunc {
	return func(c *gin.Context) {
		tokenStr, ok := bearerToken(c)
		if !ok {
			abortJSON(c, http.StatusUnauthorized, "Autenticação necessária")
			return
		}

		claims, err := ts.ParsePlataformaToken(tokenStr)
		if err != nil {
			if errors.Is(err, auth.ErrTokenExpirado) {
				c.Header("X-Token-Expired", "1")
				abortJSON(c, http.StatusUnauthorized, "Sessão expirada")
				return
			}
			abortJSON(c, http.StatusUnauthorized, "Autenticação inválida")
			return
		}

		c.Set(CtxPlataformaAdminID, claims.AdminID)
		c.Next()
	}
}

// GetPlataformaAdminID devolve o administrador da plataforma do contexto, ou 0.
func GetPlataformaAdminID(c *gin.Context) uint { return uintFromCtx(c, CtxPlataformaAdminID) }
