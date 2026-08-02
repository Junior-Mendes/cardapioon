package validate

import (
	"errors"
	"regexp"
	"strings"
)

// NIFPortugues valida um número de identificação fiscal português.
//
// Nove dígitos, com dígito de controlo módulo 11 sobre os oito primeiros ponderados de 9
// a 2. O primeiro dígito identifica o tipo de contribuinte; aceitamos os prefixos em uso
// para particulares e pessoas colectivas.
func NIFPortugues(nif string) bool {
	nif = strings.ReplaceAll(strings.TrimSpace(nif), " ", "")
	nif = strings.TrimPrefix(strings.ToUpper(nif), "PT")

	if len(nif) != 9 {
		return false
	}
	for _, r := range nif {
		if r < '0' || r > '9' {
			return false
		}
	}

	// Primeiro dígito: 1,2,3 particulares; 5 pessoas colectivas; 6 administração pública;
	// 8 empresário individual; 45,7,9 outros casos previstos.
	switch nif[0] {
	case '1', '2', '3', '5', '6', '8':
	case '4', '7', '9':
		// Estes prefixos só são válidos em combinações específicas de dois dígitos.
		doisPrimeiros := nif[:2]
		validos := map[string]bool{
			"45": true, "70": true, "71": true, "72": true, "74": true, "75": true,
			"77": true, "78": true, "79": true, "90": true, "91": true, "98": true, "99": true,
		}
		if !validos[doisPrimeiros] {
			return false
		}
	default:
		return false
	}

	soma := 0
	for i := 0; i < 8; i++ {
		soma += int(nif[i]-'0') * (9 - i)
	}
	resto := soma % 11

	controlo := 0
	if resto >= 2 {
		controlo = 11 - resto
	}
	return controlo == int(nif[8]-'0')
}

// codigoPostalRe valida o formato português de quatro dígitos, hífen e três dígitos.
var codigoPostalRe = regexp.MustCompile(`^[1-9][0-9]{3}-[0-9]{3}$`)

var ErrCodigoPostal = errors.New("código postal inválido: use o formato 1000-001")

// CodigoPostal normaliza e valida um código postal português.
//
// Aceita entrada sem hífen ("1000001") e com espaços, normalizando para "1000-001".
func CodigoPostal(raw string) (string, error) {
	s := strings.ToUpper(strings.TrimSpace(raw))
	s = strings.ReplaceAll(s, " ", "")

	// Inserir o hífen quando vêm sete dígitos seguidos.
	if len(s) == 7 && !strings.Contains(s, "-") {
		s = s[:4] + "-" + s[4:]
	}

	if !codigoPostalRe.MatchString(s) {
		return "", ErrCodigoPostal
	}
	return s, nil
}

// PrefixoCodigoPostal devolve os quatro primeiros dígitos, usados como chave das zonas
// de entrega.
func PrefixoCodigoPostal(cp string) string {
	if len(cp) >= 4 {
		return cp[:4]
	}
	return ""
}

var telefonePTRe = regexp.MustCompile(`^[1-9][0-9]{8}$`)

var ErrTelefone = errors.New("número de telefone inválido: indique 9 dígitos, por exemplo 912 345 678")

// TelefonePortugues normaliza e valida um número português.
//
// Devolve o número no formato E.164 (+351XXXXXXXXX), que é o exigido pelas APIs de SMS e
// de MB WAY.
func TelefonePortugues(raw string) (string, error) {
	s := strings.TrimSpace(raw)
	// Remove separadores comuns.
	var digitos strings.Builder
	temMais := strings.HasPrefix(s, "+")
	for _, r := range s {
		if r >= '0' && r <= '9' {
			digitos.WriteRune(r)
		}
	}
	n := digitos.String()

	// Formas aceites: 912345678, 351912345678, +351912345678, 00351912345678.
	switch {
	case len(n) == 12 && strings.HasPrefix(n, "351"):
		n = n[3:]
	case len(n) == 14 && strings.HasPrefix(n, "00351"):
		n = n[5:]
	case temMais && len(n) > 9:
		// Número internacional de outro país: aceitamos como está, sem normalizar.
		if len(n) < 8 || len(n) > 15 {
			return "", ErrTelefone
		}
		return "+" + n, nil
	}

	if !telefonePTRe.MatchString(n) {
		return "", ErrTelefone
	}
	return "+351" + n, nil
}

// TelemovelPortugues indica se o número é de rede móvel (começa por 9).
// Relevante para MB WAY e SMS, que só funcionam com móveis.
func TelemovelPortugues(e164 string) bool {
	return strings.HasPrefix(e164, "+3519") && len(e164) == 13
}
