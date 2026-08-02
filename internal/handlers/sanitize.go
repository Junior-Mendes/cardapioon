package handlers

import (
	"html"
	"strings"
	"unicode"
)

// htmlEscape escapa texto para inclusão em HTML (usado nos emails).
func htmlEscape(s string) string { return html.EscapeString(s) }

// limparTexto normaliza texto vindo do cliente antes de o gravar.
//
// Sanitização na entrada, complementar ao escape na saída: remove caracteres de controlo
// e zero-width, que servem tanto para esconder payloads como para falsificar nomes de
// produtos visualmente idênticos a outros.
//
// O escape de HTML NÃO é feito aqui de propósito: gravar "&amp;" na base corromperia os
// dados. O escape pertence ao ponto de renderização.
func limparTexto(s string, maxLen int) string {
	s = strings.TrimSpace(s)

	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch {
		case r == '\n' || r == '\t':
			// Permitidos em descrições; normalizados para espaço em campos de uma linha
			// pelo chamador, se necessário.
			b.WriteRune(r)
		case unicode.IsControl(r):
			continue
		case r == '\u200b' || r == '\u200c' || r == '\u200d' || r == '\ufeff':
			// Zero-width space/joiner e BOM: invisíveis, permitem criar nomes de produtos
			// visualmente idênticos a outros.
			continue
		case r == '\u202e' || r == '\u202d' || r == '\u2066' || r == '\u2067':
			// Override de direcção do texto: usado para disfarçar conteúdo.
			continue
		default:
			b.WriteRune(r)
		}
	}

	out := strings.TrimSpace(b.String())
	if maxLen > 0 {
		r := []rune(out)
		if len(r) > maxLen {
			out = strings.TrimSpace(string(r[:maxLen]))
		}
	}
	return out
}

// limparLinha é como limparTexto mas colapsa qualquer espaço em branco num único espaço.
func limparLinha(s string, maxLen int) string {
	s = limparTexto(s, 0)
	s = strings.Join(strings.Fields(s), " ")
	if maxLen > 0 {
		r := []rune(s)
		if len(r) > maxLen {
			s = strings.TrimSpace(string(r[:maxLen]))
		}
	}
	return s
}
