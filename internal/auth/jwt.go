package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const (
	issuer        = "cardapio-online"
	audienceAdmin = "admin"
	// audiencePlataforma separa os tokens do painel do dono do SaaS dos tokens dos
	// lojistas. É esta separação, e não uma verificação num handler, que impede que um
	// token de lojista abra o painel da plataforma: ParseAccessToken exige a audiência
	// "admin" e ParsePlataformaToken exige "plataforma", pelo que cada um dos dois
	// rejeita o token do outro na validação da assinatura, antes de qualquer handler.
	audiencePlataforma = "plataforma"
)

var (
	ErrTokenInvalido  = errors.New("token inválido")
	ErrTokenExpirado  = errors.New("token expirado")
	ErrSegredoAusente = errors.New("JWT_SECRET não configurado")
)

// Claims são as afirmações assinadas do access token.
//
// O tenant_id vive aqui, assinado: é esta a única fonte de verdade do tenant nas rotas
// administrativas. A versão anterior derivava o tenant do cabeçalho Host, o que permitia
// a um lojista autenticado operar na conta de outro bastando abrir o subdomínio dele.
type Claims struct {
	TenantID uint   `json:"tid"`
	UserID   uint   `json:"uid"`
	Role     string `json:"role"`
	jwt.RegisteredClaims
}

// TokenService emite e valida tokens. Criado uma vez no arranque.
type TokenService struct {
	secret     []byte
	accessTTL  time.Duration
	refreshTTL time.Duration
}

// NewTokenService valida o segredo no arranque — é preferível falhar de imediato a
// servir tráfego com tokens assináveis por qualquer pessoa.
func NewTokenService(secret string, accessTTL, refreshTTL time.Duration) (*TokenService, error) {
	if strings.TrimSpace(secret) == "" {
		return nil, ErrSegredoAusente
	}
	if len(secret) < 32 {
		return nil, fmt.Errorf("JWT_SECRET demasiado curto (%d bytes): use pelo menos 32; gere com 'openssl rand -base64 48'", len(secret))
	}
	if accessTTL <= 0 {
		accessTTL = time.Hour
	}
	if refreshTTL <= 0 {
		refreshTTL = 30 * 24 * time.Hour
	}
	return &TokenService{secret: []byte(secret), accessTTL: accessTTL, refreshTTL: refreshTTL}, nil
}

func (s *TokenService) AccessTTL() time.Duration  { return s.accessTTL }
func (s *TokenService) RefreshTTL() time.Duration { return s.refreshTTL }

// IssueAccessToken emite um JWT HS256 de curta duração.
func (s *TokenService) IssueAccessToken(tenantID, userID uint, role string) (string, time.Time, error) {
	agora := time.Now()
	expira := agora.Add(s.accessTTL)

	claims := Claims{
		TenantID: tenantID,
		UserID:   userID,
		Role:     role,
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    issuer,
			Subject:   fmt.Sprintf("%d", userID),
			Audience:  jwt.ClaimStrings{audienceAdmin},
			IssuedAt:  jwt.NewNumericDate(agora),
			NotBefore: jwt.NewNumericDate(agora),
			ExpiresAt: jwt.NewNumericDate(expira),
			ID:        randomHex(16),
		},
	}

	assinado, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(s.secret)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("assinar access token: %w", err)
	}
	return assinado, expira, nil
}

// ParseAccessToken valida assinatura, algoritmo, emissor, audiência e validade.
func (s *TokenService) ParseAccessToken(tokenStr string) (*Claims, error) {
	claims := &Claims{}

	_, err := jwt.ParseWithClaims(tokenStr, claims,
		func(t *jwt.Token) (interface{}, error) {
			// Fixar o algoritmo evita o ataque clássico de trocar HS256 por "none"
			// ou por RS256 com a chave pública como segredo.
			if t.Method.Alg() != jwt.SigningMethodHS256.Alg() {
				return nil, fmt.Errorf("algoritmo inesperado: %v", t.Header["alg"])
			}
			return s.secret, nil
		},
		jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}),
		jwt.WithIssuer(issuer),
		jwt.WithAudience(audienceAdmin),
	)
	if err != nil {
		if errors.Is(err, jwt.ErrTokenExpired) {
			return nil, ErrTokenExpirado
		}
		return nil, fmt.Errorf("%w: %v", ErrTokenInvalido, err)
	}

	if claims.TenantID == 0 || claims.UserID == 0 {
		return nil, fmt.Errorf("%w: claims incompletas", ErrTokenInvalido)
	}
	return claims, nil
}

// ClaimsPlataforma são as afirmações assinadas do token do painel da plataforma.
//
// Não tem — nem pode ter — tenant_id: quem opera a plataforma não age dentro de um
// restaurante. Se este token chegasse a uma rota /api/admin/*, não haveria tenant para
// escopar; mas nem chega, porque a audiência não corresponde.
type ClaimsPlataforma struct {
	AdminID uint `json:"pid"`
	jwt.RegisteredClaims
}

// IssuePlataformaToken emite o access token do painel da plataforma.
func (s *TokenService) IssuePlataformaToken(adminID uint) (string, time.Time, error) {
	agora := time.Now()
	expira := agora.Add(s.accessTTL)

	claims := ClaimsPlataforma{
		AdminID: adminID,
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    issuer,
			Subject:   fmt.Sprintf("p%d", adminID),
			Audience:  jwt.ClaimStrings{audiencePlataforma},
			IssuedAt:  jwt.NewNumericDate(agora),
			NotBefore: jwt.NewNumericDate(agora),
			ExpiresAt: jwt.NewNumericDate(expira),
			ID:        randomHex(16),
		},
	}

	assinado, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(s.secret)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("assinar token da plataforma: %w", err)
	}
	return assinado, expira, nil
}

// ParsePlataformaToken valida um token do painel da plataforma.
//
// Um token de lojista falha aqui na verificação de audiência, mesmo estando corretamente
// assinado com o mesmo segredo.
func (s *TokenService) ParsePlataformaToken(tokenStr string) (*ClaimsPlataforma, error) {
	claims := &ClaimsPlataforma{}

	_, err := jwt.ParseWithClaims(tokenStr, claims,
		func(t *jwt.Token) (interface{}, error) {
			if t.Method.Alg() != jwt.SigningMethodHS256.Alg() {
				return nil, fmt.Errorf("algoritmo inesperado: %v", t.Header["alg"])
			}
			return s.secret, nil
		},
		jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}),
		jwt.WithIssuer(issuer),
		jwt.WithAudience(audiencePlataforma),
	)
	if err != nil {
		if errors.Is(err, jwt.ErrTokenExpired) {
			return nil, ErrTokenExpirado
		}
		return nil, fmt.Errorf("%w: %v", ErrTokenInvalido, err)
	}

	if claims.AdminID == 0 {
		return nil, fmt.Errorf("%w: claims incompletas", ErrTokenInvalido)
	}
	return claims, nil
}

// NewRefreshToken devolve o token opaco a enviar ao cliente e o hash a guardar.
// O token em claro nunca é persistido: uma fuga da base não permite renovar sessões.
func NewRefreshToken() (token string, hash string) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		// crypto/rand só falha se o SO não tiver entropia; continuar seria emitir
		// tokens previsíveis.
		panic(fmt.Sprintf("falha ao gerar refresh token: %v", err))
	}
	token = base64.RawURLEncoding.EncodeToString(raw)
	return token, HashToken(token)
}

// HashToken devolve o SHA-256 hexadecimal de um token opaco.
//
// SHA-256 é adequado aqui (ao contrário do que sucede com senhas): o token tem 256 bits
// de entropia aleatória, logo não é vulnerável a dicionário nem a rainbow tables.
func HashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// NewOpaqueToken gera um token aleatório URL-safe para usos de uso único
// (reset de senha, verificação de email, verificação de domínio).
func NewOpaqueToken(nBytes int) string {
	if nBytes <= 0 {
		nBytes = 32
	}
	raw := make([]byte, nBytes)
	if _, err := rand.Read(raw); err != nil {
		panic(fmt.Sprintf("falha ao gerar token: %v", err))
	}
	return base64.RawURLEncoding.EncodeToString(raw)
}

func randomHex(n int) string {
	raw := make([]byte, n)
	if _, err := rand.Read(raw); err != nil {
		panic(fmt.Sprintf("falha ao gerar id: %v", err))
	}
	return hex.EncodeToString(raw)
}
