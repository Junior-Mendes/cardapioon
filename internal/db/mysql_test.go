package db

import (
	"strings"
	"testing"

	"cardapio-online/internal/config"

	mysqldriver "github.com/go-sql-driver/mysql"
)

// TestDSNPreservaSenhaComCaracteresEspeciais cobre uma regressão que impedia o arranque.
//
// A versão anterior aplicava url.QueryEscape à senha. O DSN do go-sql-driver/mysql não é
// URL-encoded, pelo que os '%XX' eram enviados literalmente como parte da senha e o MySQL
// respondia "Access denied" apesar de a senha estar correcta — o servidor não arrancava.
func TestDSNPreservaSenhaComCaracteresEspeciais(t *testing.T) {
	senhas := []string{
		"uk45po!@Cm#@2022", // a senha real em uso, com '!', '@' e '#'
		"simples",
		"com:dois:pontos",
		"com/barra",
		"com espaço",
		"acentuada-çãõ",
		"100%percent",
		"",
	}

	for _, senha := range senhas {
		cfg := &config.Config{
			DBUser:     "junior",
			DBPassword: senha,
			DBHost:     "10.0.0.143",
			DBPort:     "3306",
			DBName:     "cardapio_online",
		}

		s := dsn(cfg, cfg.DBName)

		// O próprio driver é a autoridade: se ele reler o DSN e recuperar a senha
		// original, então o que enviamos ao MySQL é o que o utilizador configurou.
		parsed, err := mysqldriver.ParseDSN(s)
		if err != nil {
			t.Errorf("senha %q: DSN gerado é inválido: %v", senha, err)
			continue
		}
		if parsed.Passwd != senha {
			t.Errorf("senha %q: DSN devolve %q — a senha foi corrompida", senha, parsed.Passwd)
		}
		if parsed.User != "junior" {
			t.Errorf("senha %q: utilizador ficou %q", senha, parsed.User)
		}
		if parsed.DBName != "cardapio_online" {
			t.Errorf("senha %q: base de dados ficou %q", senha, parsed.DBName)
		}
		if parsed.Addr != "10.0.0.143:3306" {
			t.Errorf("senha %q: endereço ficou %q", senha, parsed.Addr)
		}

		// Sem estes, datas voltam como []byte e os timeouts ficam ilimitados.
		if !parsed.ParseTime {
			t.Errorf("senha %q: parseTime desligado", senha)
		}
		if parsed.Timeout == 0 || parsed.ReadTimeout == 0 || parsed.WriteTimeout == 0 {
			t.Errorf("senha %q: timeouts não configurados", senha)
		}
	}
}

// TestDSNNaoContemSequenciasEscapadas é uma verificação directa contra o erro original.
func TestDSNNaoContemSequenciasEscapadas(t *testing.T) {
	cfg := &config.Config{
		DBUser: "junior", DBPassword: "uk45po!@Cm#@2022",
		DBHost: "10.0.0.143", DBPort: "3306", DBName: "cardapio_online",
	}

	s := dsn(cfg, cfg.DBName)
	for _, escapado := range []string{"%21", "%40", "%23"} {
		if strings.Contains(s, escapado) {
			t.Errorf("DSN contém a sequência escapada %q: a senha foi URL-encoded", escapado)
		}
	}
}

// TestDSNIPv6 garante que um host IPv6 não parte o endereço.
func TestDSNIPv6(t *testing.T) {
	cfg := &config.Config{
		DBUser: "u", DBPassword: "p",
		DBHost: "::1", DBPort: "3306", DBName: "d",
	}

	parsed, err := mysqldriver.ParseDSN(dsn(cfg, cfg.DBName))
	if err != nil {
		t.Fatalf("DSN com IPv6 inválido: %v", err)
	}
	if parsed.Addr != "[::1]:3306" {
		t.Errorf("endereço IPv6 ficou %q, esperado \"[::1]:3306\"", parsed.Addr)
	}
}
