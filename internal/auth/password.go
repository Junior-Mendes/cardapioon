package auth

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"unicode"

	"golang.org/x/crypto/bcrypt"
)

// bcryptCost 12 é o compromisso habitual entre resistência a força-bruta offline e
// latência de login (~250ms em hardware corrente).
const bcryptCost = 12

var (
	ErrSenhaIncorreta = errors.New("senha incorrecta")

	// legacySHA256 identifica os hashes SHA-256 sem salt gerados pela versão anterior.
	// São 64 caracteres hexadecimais; um hash bcrypt começa sempre por "$2".
	legacySHA256 = regexp.MustCompile(`^[a-f0-9]{64}$`)
)

// HashPassword devolve um hash bcrypt da senha.
func HashPassword(senha string) (string, error) {
	h, err := bcrypt.GenerateFromPassword([]byte(senha), bcryptCost)
	if err != nil {
		return "", fmt.Errorf("gerar hash da senha: %w", err)
	}
	return string(h), nil
}

// VerifyPassword valida a senha contra o hash guardado.
//
// Aceita ainda os hashes SHA-256 legados para não bloquear utilizadores criados antes
// da migração para bcrypt. Devolve needsRehash=true nesse caso, para que o chamador
// regrave o hash em bcrypt após um login bem-sucedido — assim os hashes fracos
// desaparecem gradualmente sem forçar um reset de senha a toda a base.
func VerifyPassword(hashGuardado, senha string) (needsRehash bool, err error) {
	if legacySHA256.MatchString(hashGuardado) {
		sum := sha256.Sum256([]byte(senha))
		calculado := hex.EncodeToString(sum[:])
		// Comparação em tempo constante para não expor o hash por temporização.
		if subtle.ConstantTimeCompare([]byte(calculado), []byte(hashGuardado)) != 1 {
			return false, ErrSenhaIncorreta
		}
		return true, nil
	}

	if err := bcrypt.CompareHashAndPassword([]byte(hashGuardado), []byte(senha)); err != nil {
		if errors.Is(err, bcrypt.ErrMismatchedHashAndPassword) {
			return false, ErrSenhaIncorreta
		}
		return false, fmt.Errorf("verificar senha: %w", err)
	}
	return false, nil
}

// DummyVerify executa trabalho equivalente a uma verificação real.
//
// Serve para o caminho "utilizador não existe": sem isto, um login com email inexistente
// responde muito mais depressa do que um com senha errada, e essa diferença de tempo
// permite enumerar as contas registadas na plataforma.
func DummyVerify() {
	// Hash bcrypt fixo, de custo igual ao de produção, sobre um valor que nunca coincide.
	const placeholder = "$2a$12$eImiTXuWVxfM37uY4JANjQ0oOFDGAo9x7cVoCvHo8LhLTM/Kdx9bO"
	_ = bcrypt.CompareHashAndPassword([]byte(placeholder), []byte("senha-invalida"))
}

// ValidarForcaSenha rejeita as senhas triviais. Mínimo de 8 caracteres com pelo menos
// uma letra e um dígito: o limite anterior de 6 caracteres sem regras permitia "123456".
func ValidarForcaSenha(senha string) error {
	if len([]rune(senha)) < 8 {
		return errors.New("a senha deve ter pelo menos 8 caracteres")
	}
	if len(senha) > 72 {
		// O bcrypt trunca silenciosamente aos 72 bytes; rejeitar é preferível a truncar.
		return errors.New("a senha não pode exceder 72 caracteres")
	}

	var temLetra, temDigito bool
	for _, r := range senha {
		switch {
		case unicode.IsLetter(r):
			temLetra = true
		case unicode.IsDigit(r):
			temDigito = true
		}
	}
	if !temLetra || !temDigito {
		return errors.New("a senha deve conter pelo menos uma letra e um número")
	}

	if senhasComuns[senha] {
		return errors.New("esta senha é demasiado comum; escolha outra")
	}
	return nil
}

// senhasComuns cobre as escolhas mais frequentes em fugas de dados conhecidas.
var senhasComuns = map[string]bool{
	"password":    true,
	"password1":   true,
	"passw0rd":    true,
	"12345678":    true,
	"123456789":   true,
	"1234567890":  true,
	"qwerty123":   true,
	"admin123":    true,
	"benfica123":  true,
	"portugal1":   true,
	"restaurante": true,
}
