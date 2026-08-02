package auth

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const segredoTeste = "segredo-de-teste-com-mais-de-32-bytes-para-passar-validacao"

func servicoTeste(t *testing.T) *TokenService {
	t.Helper()
	s, err := NewTokenService(segredoTeste, time.Hour, 24*time.Hour)
	if err != nil {
		t.Fatalf("criar TokenService: %v", err)
	}
	return s
}

// TestTokenAntigoForjadoEhRejeitado é o teste central da falha C1: o formato anterior era
// uma string previsível que qualquer pessoa podia escrever.
func TestTokenAntigoForjadoEhRejeitado(t *testing.T) {
	s := servicoTeste(t)

	forjados := []string{
		"admin_user_1_1",
		"admin_user_2_5",
		"admin_tenant_1",
		"admin_user_999_999",
		"",
		"Bearer admin_user_1_1",
	}

	for _, tok := range forjados {
		if _, err := s.ParseAccessToken(tok); err == nil {
			t.Errorf("token forjado %q foi aceite", tok)
		}
	}
}

func TestAccessTokenValidoRoundTrip(t *testing.T) {
	s := servicoTeste(t)

	tok, expira, err := s.IssueAccessToken(7, 42, "owner")
	if err != nil {
		t.Fatalf("emitir: %v", err)
	}
	if !expira.After(time.Now()) {
		t.Error("expiração no passado")
	}

	claims, err := s.ParseAccessToken(tok)
	if err != nil {
		t.Fatalf("validar: %v", err)
	}
	if claims.TenantID != 7 || claims.UserID != 42 || claims.Role != "owner" {
		t.Errorf("claims erradas: %+v", claims)
	}
}

// TestTokenDeOutroSegredoEhRejeitado garante que a assinatura é verificada.
func TestTokenDeOutroSegredoEhRejeitado(t *testing.T) {
	emissor, _ := NewTokenService("outro-segredo-completamente-diferente-com-32-bytes", time.Hour, time.Hour)
	validador := servicoTeste(t)

	tok, _, _ := emissor.IssueAccessToken(1, 1, "owner")
	if _, err := validador.ParseAccessToken(tok); err == nil {
		t.Error("token assinado com outro segredo foi aceite")
	}
}

// TestAlgNoneEhRejeitado cobre o ataque de confusão de algoritmo: trocar HS256 por "none"
// remove a assinatura.
func TestAlgNoneEhRejeitado(t *testing.T) {
	s := servicoTeste(t)

	claims := Claims{
		TenantID: 1, UserID: 1, Role: "owner",
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    issuer,
			Audience:  jwt.ClaimStrings{audienceAdmin},
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
		},
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodNone, claims)
	assinado, err := tok.SignedString(jwt.UnsafeAllowNoneSignatureType)
	if err != nil {
		t.Fatalf("construir token alg=none: %v", err)
	}

	if _, err := s.ParseAccessToken(assinado); err == nil {
		t.Error("token com alg=none foi aceite")
	}
}

// TestTokenExpiradoDevolveErroEspecifico verifica que a expiração é distinguível de um
// token inválido: o frontend precisa dessa diferença para renovar em vez de mandar o
// utilizador para o login.
//
// O token é assinado à mão com exp no passado, porque NewTokenService normaliza um TTL
// não positivo para o valor por omissão.
func TestTokenExpiradoDevolveErroEspecifico(t *testing.T) {
	s := servicoTeste(t)
	passado := time.Now().Add(-2 * time.Hour)

	claims := Claims{
		TenantID: 1, UserID: 1, Role: "owner",
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    issuer,
			Audience:  jwt.ClaimStrings{audienceAdmin},
			IssuedAt:  jwt.NewNumericDate(passado),
			ExpiresAt: jwt.NewNumericDate(passado.Add(time.Minute)),
		},
	}
	assinado, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(segredoTeste))
	if err != nil {
		t.Fatalf("assinar token expirado: %v", err)
	}

	if _, err := s.ParseAccessToken(assinado); err != ErrTokenExpirado {
		t.Errorf("esperado ErrTokenExpirado, obtido %v", err)
	}
}

// TestTTLNaoPositivoUsaOmissao documenta o guarda de NewTokenService.
func TestTTLNaoPositivoUsaOmissao(t *testing.T) {
	s, err := NewTokenService(segredoTeste, -time.Minute, -time.Hour)
	if err != nil {
		t.Fatalf("criar serviço: %v", err)
	}
	if s.AccessTTL() != time.Hour {
		t.Errorf("AccessTTL = %v, esperado 1h", s.AccessTTL())
	}
	if s.RefreshTTL() != 30*24*time.Hour {
		t.Errorf("RefreshTTL = %v, esperado 720h", s.RefreshTTL())
	}
}

func TestSegredoCurtoEhRejeitadoNoArranque(t *testing.T) {
	if _, err := NewTokenService("curto", time.Hour, time.Hour); err == nil {
		t.Error("segredo curto aceite")
	}
	if _, err := NewTokenService("", time.Hour, time.Hour); err == nil {
		t.Error("segredo vazio aceite")
	}
}

// TestClaimsIncompletasRejeitadas: um token válido mas sem tenant não deve dar acesso.
func TestClaimsIncompletasRejeitadas(t *testing.T) {
	s := servicoTeste(t)

	claims := Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    issuer,
			Audience:  jwt.ClaimStrings{audienceAdmin},
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
		},
	}
	assinado, _ := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(segredoTeste))

	if _, err := s.ParseAccessToken(assinado); err == nil {
		t.Error("token sem tenant_id/user_id foi aceite")
	}
}

// --- Senhas ---

func TestBcryptRoundTrip(t *testing.T) {
	hash, err := HashPassword("Senha!Segura9")
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	if !strings.HasPrefix(hash, "$2") {
		t.Errorf("hash não parece bcrypt: %q", hash)
	}

	rehash, err := VerifyPassword(hash, "Senha!Segura9")
	if err != nil {
		t.Fatalf("verificar: %v", err)
	}
	if rehash {
		t.Error("bcrypt não deveria pedir rehash")
	}

	if _, err := VerifyPassword(hash, "senha-errada"); err != ErrSenhaIncorreta {
		t.Errorf("esperado ErrSenhaIncorreta, obtido %v", err)
	}
}

// TestHashLegadoAceitoComRehash cobre a migração: contas antigas têm de continuar a
// entrar, mas o hash fraco deve ser sinalizado para substituição.
func TestHashLegadoAceitoComRehash(t *testing.T) {
	sum := sha256.Sum256([]byte("admin123"))
	legado := hex.EncodeToString(sum[:])

	rehash, err := VerifyPassword(legado, "admin123")
	if err != nil {
		t.Fatalf("hash legado rejeitado: %v", err)
	}
	if !rehash {
		t.Error("hash legado deveria pedir rehash")
	}

	if _, err := VerifyPassword(legado, "outra"); err != ErrSenhaIncorreta {
		t.Errorf("senha errada contra hash legado: %v", err)
	}
}

func TestValidarForcaSenha(t *testing.T) {
	invalidas := []string{
		"curta",                  // menos de 8
		"12345678",               // sem letras
		"abcdefgh",               // sem dígitos
		"admin123",               // comum
		"password1",              // comum
		strings.Repeat("a1", 40), // acima de 72 bytes
	}
	for _, s := range invalidas {
		if err := ValidarForcaSenha(s); err == nil {
			t.Errorf("senha fraca aceite: %q", s)
		}
	}

	validas := []string{"Bacalhau2024", "tasca-do-bairro9", "Xk7mQ2ppw"}
	for _, s := range validas {
		if err := ValidarForcaSenha(s); err != nil {
			t.Errorf("senha válida rejeitada %q: %v", s, err)
		}
	}
}

func TestRefreshTokenNaoEhPrevisivel(t *testing.T) {
	vistos := map[string]bool{}
	for i := 0; i < 500; i++ {
		tok, hash := NewRefreshToken()
		if vistos[tok] {
			t.Fatal("refresh token repetido")
		}
		vistos[tok] = true

		if hash != HashToken(tok) {
			t.Fatal("hash devolvido não corresponde ao token")
		}
		if len(tok) < 40 {
			t.Fatalf("token demasiado curto: %d chars", len(tok))
		}
	}
}
