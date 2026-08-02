package middleware

import (
	"errors"
	"net/http"
	"strings"

	"cardapio-online/internal/auth"

	"github.com/gin-gonic/gin"
)

// Chaves de contexto. Tipadas para não colidirem com strings arbitrárias.
const (
	CtxTenantID = "tenant_id"
	CtxUserID   = "user_id"
	CtxRole     = "role"
	CtxSlug     = "tenant_slug"
)

// Papéis, do mais para o menos privilegiado.
const (
	RoleOwner       = "owner"
	RoleAdmin       = "admin"
	RoleGerente     = "gerente"
	RoleFuncionario = "funcionario"
)

// nivelRole permite comparações hierárquicas sem enumerar todas as combinações.
var nivelRole = map[string]int{
	RoleFuncionario: 1,
	RoleGerente:     2,
	RoleAdmin:       3,
	RoleOwner:       4,
}

// RequireAuth valida o JWT e coloca tenant_id, user_id e role no contexto.
//
// Esta é a única forma de o tenant_id entrar numa rota administrativa. A versão anterior
// derivava-o do cabeçalho Host antes de olhar para o token, o que permitia a um lojista
// autenticado operar sobre os dados de outro simplesmente abrindo o subdomínio dele.
func RequireAuth(ts *auth.TokenService) gin.HandlerFunc {
	return func(c *gin.Context) {
		tokenStr, ok := bearerToken(c)
		if !ok {
			abortJSON(c, http.StatusUnauthorized, "Autenticação necessária")
			return
		}

		claims, err := ts.ParseAccessToken(tokenStr)
		if err != nil {
			if errors.Is(err, auth.ErrTokenExpirado) {
				// Código distinto para o frontend saber que deve tentar renovar em vez
				// de mandar o utilizador para o login.
				c.Header("X-Token-Expired", "1")
				abortJSON(c, http.StatusUnauthorized, "Sessão expirada")
				return
			}
			abortJSON(c, http.StatusUnauthorized, "Autenticação inválida")
			return
		}

		c.Set(CtxTenantID, claims.TenantID)
		c.Set(CtxUserID, claims.UserID)
		c.Set(CtxRole, claims.Role)
		c.Next()
	}
}

// RequireRole exige que o papel do utilizador seja pelo menos um dos indicados.
//
// O campo Role existia no modelo desde o início e nunca era verificado em nenhum handler:
// um 'funcionario' podia alterar métodos de pagamento, criar utilizadores e mudar o
// domínio do restaurante.
func RequireRole(permitidos ...string) gin.HandlerFunc {
	minimo := 0
	for _, r := range permitidos {
		if n := nivelRole[r]; minimo == 0 || n < minimo {
			minimo = n
		}
	}

	return func(c *gin.Context) {
		role, _ := c.Get(CtxRole)
		roleStr, _ := role.(string)

		if nivelRole[roleStr] < minimo {
			abortJSON(c, http.StatusForbidden, "Não tem permissões para esta operação")
			return
		}
		c.Next()
	}
}

// GetTenantID devolve o tenant do contexto, ou 0.
func GetTenantID(c *gin.Context) uint { return uintFromCtx(c, CtxTenantID) }
func GetUserID(c *gin.Context) uint   { return uintFromCtx(c, CtxUserID) }
func GetRole(c *gin.Context) string   { v, _ := c.Get(CtxRole); s, _ := v.(string); return s }

func uintFromCtx(c *gin.Context, chave string) uint {
	v, existe := c.Get(chave)
	if !existe {
		return 0
	}
	n, _ := v.(uint)
	return n
}

// bearerToken extrai o token do cabeçalho Authorization.
func bearerToken(c *gin.Context) (string, bool) {
	h := c.GetHeader("Authorization")
	if h == "" {
		return "", false
	}
	partes := strings.SplitN(h, " ", 2)
	if len(partes) == 2 && strings.EqualFold(partes[0], "Bearer") {
		t := strings.TrimSpace(partes[1])
		return t, t != ""
	}
	// Sem prefixo: rejeitado de propósito. O formato antigo era um token opaco sem
	// esquema, e aceitá-lo aqui reabriria a porta a tentativas com o formato forjável.
	return "", false
}

func abortJSON(c *gin.Context, status int, msg string) {
	c.AbortWithStatusJSON(status, gin.H{"error": msg})
}
