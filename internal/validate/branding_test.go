package validate

import "testing"

func TestCorHexNormaliza(t *testing.T) {
	validas := map[string]string{
		"#E63946": "#e63946",
		"e63946":  "#e63946",
		"#abc":    "#aabbcc",
		"abc":     "#aabbcc",
		" #FFF ":  "#ffffff",
		"":        "", // vazio = usar a cor por omissão
	}
	for entrada, esperado := range validas {
		got, err := CorHex(entrada)
		if err != nil {
			t.Errorf("cor válida rejeitada %q: %v", entrada, err)
			continue
		}
		if got != esperado {
			t.Errorf("CorHex(%q) = %q, esperado %q", entrada, got, esperado)
		}
	}

	invalidas := []string{
		"#12345",                      // 5 dígitos
		"#1234567",                    // 7 dígitos
		"vermelho",                    // nome
		"#gggggg",                     // não hexadecimal
		"rgb(1,2,3)",                  // outra notação
		"#e63946; x:1",                // tentativa de injecção em CSS
		"#e6 3946",                    // espaço no meio, que TrimSpace não remove
		"#e63946\nbackground: url(x)", // newline no meio
		"javascript:alert",            // payload
	}
	for _, c := range invalidas {
		if got, err := CorHex(c); err == nil {
			t.Errorf("cor inválida aceite: %q -> %q", c, got)
		}
	}

	// Espaços e newlines nas extremidades são normalizados, não rejeitados: colar de uma
	// paleta de cores é o caso comum e o valor gravado fica limpo.
	if got, err := CorHex("  #E63946\n"); err != nil || got != "#e63946" {
		t.Errorf("CorHex com espaços nas extremidades = %q, %v", got, err)
	}
}

// TestTextoSobreCorGaranteLegibilidade é o teste que importa para o lojista: ele escolhe a
// cor da marca sem pensar em contraste, e o botão de encomendar tem de continuar legível.
func TestTextoSobreCorGaranteLegibilidade(t *testing.T) {
	casos := map[string]string{
		// Cores escuras -> texto branco
		"#000000": "#ffffff",
		"#c1121f": "#ffffff", // vermelho tipo tijolo
		"#003049": "#ffffff", // azul escuro
		"#2d6a4f": "#ffffff", // verde escuro
		// Cores claras -> texto escuro
		"#ffffff": "#111111",
		"#ffe66d": "#111111", // amarelo
		"#f1faee": "#111111", // creme
		"#a8dadc": "#111111", // azul claro
	}

	for cor, esperado := range casos {
		if got := TextoSobreCor(cor); got != esperado {
			t.Errorf("TextoSobreCor(%s) = %s, esperado %s (luminância %.3f)",
				cor, got, esperado, LuminanciaRelativa(cor))
		}
	}
}

func TestLuminanciaRelativaExtremos(t *testing.T) {
	if l := LuminanciaRelativa("#000000"); l != 0 {
		t.Errorf("luminância do preto = %v, esperado 0", l)
	}
	if l := LuminanciaRelativa("#ffffff"); l < 0.99 {
		t.Errorf("luminância do branco = %v, esperado ~1", l)
	}
	// Uma cor inválida não deve provocar pânico.
	if l := LuminanciaRelativa("invalida"); l != 0 {
		t.Errorf("luminância de cor inválida = %v, esperado 0", l)
	}
}

func TestLogoURLRestringeEsquemas(t *testing.T) {
	validos := []string{
		"https://exemplo.pt/logo.png",
		"/static/uploads/logo-123.webp",
		"",
	}
	for _, u := range validos {
		if _, err := LogoURL(u); err != nil {
			t.Errorf("logótipo válido rejeitado %q: %v", u, err)
		}
	}

	invalidos := []string{
		"http://exemplo.pt/logo.png",                // conteúdo misto numa página HTTPS
		"javascript:alert(1)",                       // execução
		"data:image/svg+xml,<svg onload=alert(1)>",  // SVG com script
		"//exemplo.pt/logo.png",                     // protocolo relativo
		"/etc/passwd",                               // caminho local arbitrário
		"https://exemplo.pt/logo.png\" onerror=\"x", // sai do atributo
		"https://exemplo.pt/a\nb",                   // newline
	}
	for _, u := range invalidos {
		if got, err := LogoURL(u); err == nil {
			t.Errorf("logótipo perigoso aceite: %q -> %q", u, got)
		}
	}
}
