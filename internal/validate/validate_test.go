package validate

import "testing"

// TestSlugRejeitaPathTraversalEInjeccaoYAML cobre a falha C4: o slug entra num caminho de
// ficheiro e no corpo de um YAML consumido pelo Traefik.
func TestSlugRejeitaPathTraversalEInjeccaoYAML(t *testing.T) {
	maliciosos := []string{
		"../etc/passwd",
		"..",
		".",
		"../../traefik_dynamic/default",
		"loja/../default",
		"default", // reservado: sobrescreveria a rota do domínio principal
		"www",     // reservado
		"api",     // reservado
		"admin",   // reservado
		"traefik", // reservado
		"loja`",   // crase fecha o Host(`...`) do Traefik
		"a`)||Host(`outro.pt",
		"loja\nrouters:",
		"loja\r\nx",
		"loja com espaco",
		"loja;rm",
		"LOJA$(id)",
		"loja'",
		"loja\"",
		"xn--loja", // punycode: permite homógrafos
		"lo--ja",   // dois hífenes
		"-loja",    // não pode começar por hífen
		"loja-",    // nem terminar
		"ab",       // curto demais
		"",
		"a234567890123456789012345678901234567890", // longo demais
	}

	for _, s := range maliciosos {
		if got, err := Slug(s); err == nil {
			t.Errorf("slug perigoso aceite: %q -> %q", s, got)
		}
	}
}

func TestSlugAceitaValidos(t *testing.T) {
	casos := map[string]string{
		"tasca-do-bairro": "tasca-do-bairro",
		"  Pizzaria10  ":  "pizzaria10",
		"ABC":             "abc",
		"restaurante-1":   "restaurante-1",
	}
	for entrada, esperado := range casos {
		got, err := Slug(entrada)
		if err != nil {
			t.Errorf("slug válido rejeitado %q: %v", entrada, err)
			continue
		}
		if got != esperado {
			t.Errorf("Slug(%q) = %q, esperado %q", entrada, got, esperado)
		}
	}
}

func TestDomainNormalizaERejeita(t *testing.T) {
	validos := map[string]string{
		"Exemplo.PT":              "exemplo.pt",
		"https://www.exemplo.pt/": "www.exemplo.pt",
		"exemplo.pt.":             "exemplo.pt",
		"http://loja.exemplo.com": "loja.exemplo.com",
		"exemplo.pt:443":          "exemplo.pt",
	}
	for entrada, esperado := range validos {
		got, err := Domain(entrada)
		if err != nil {
			t.Errorf("domínio válido rejeitado %q: %v", entrada, err)
			continue
		}
		if got != esperado {
			t.Errorf("Domain(%q) = %q, esperado %q", entrada, got, esperado)
		}
	}

	invalidos := []string{
		"sem-tld", "exemplo..pt", "-exemplo.pt", "exemplo.p", "loja`.pt",
		"exem\nplo.pt", // newline no meio: não é removível por TrimSpace
		"exemplo.pt;x",
	}
	for _, s := range invalidos {
		if _, err := Domain(s); err == nil {
			t.Errorf("domínio inválido aceite: %q", s)
		}
	}

	// Espaços e newlines nas extremidades são normalizados, não rejeitados: colar de um
	// email é o caso comum e o valor gravado fica limpo.
	if got, err := Domain("  exemplo.pt\n"); err != nil || got != "exemplo.pt" {
		t.Errorf("Domain com espaços nas extremidades = %q, %v", got, err)
	}
}

// TestDomainNaoPodeSerDoSaaS cobre a apropriação do domínio da própria plataforma.
func TestDomainNaoPodeSerDoSaaS(t *testing.T) {
	principal := "exemplo.pt"

	proibidos := []string{"exemplo.pt", "www.exemplo.pt", "qualquer.exemplo.pt"}
	for _, d := range proibidos {
		if err := DomainNaoPodeSerDoSaaS(d, principal); err == nil {
			t.Errorf("domínio da plataforma aceite: %q", d)
		}
	}

	if err := DomainNaoPodeSerDoSaaS("pizzaria.com", principal); err != nil {
		t.Errorf("domínio de terceiro legítimo rejeitado: %v", err)
	}
}

func TestNIFPortugues(t *testing.T) {
	// NIFs com dígito de controlo correcto.
	validos := []string{"501442600", "980405319", "123456789"}
	for _, n := range validos {
		if !NIFPortugues(n) {
			t.Errorf("NIF válido rejeitado: %s", n)
		}
	}

	invalidos := []string{
		"123456780", // dígito de controlo errado
		"12345678",  // curto
		"1234567890",
		"000000000", // prefixo inválido
		"abcdefghi",
		"",
	}
	for _, n := range invalidos {
		if NIFPortugues(n) {
			t.Errorf("NIF inválido aceite: %s", n)
		}
	}

	// Aceita prefixo PT e espaços.
	if !NIFPortugues("PT 501442600") {
		t.Error("NIF com prefixo PT e espaços rejeitado")
	}
}

func TestCodigoPostal(t *testing.T) {
	casos := map[string]string{
		"1000-001":   "1000-001",
		"1000001":    "1000-001",
		" 4700-025 ": "4700-025",
	}
	for entrada, esperado := range casos {
		got, err := CodigoPostal(entrada)
		if err != nil {
			t.Errorf("CP válido rejeitado %q: %v", entrada, err)
			continue
		}
		if got != esperado {
			t.Errorf("CodigoPostal(%q) = %q, esperado %q", entrada, got, esperado)
		}
	}

	invalidos := []string{"0100-001", "100-001", "10000-001", "abcd-123", "1000 001x", ""}
	for _, s := range invalidos {
		if _, err := CodigoPostal(s); err == nil {
			t.Errorf("CP inválido aceite: %q", s)
		}
	}
}

func TestTelefonePortugues(t *testing.T) {
	casos := map[string]string{
		"912345678":      "+351912345678",
		"912 345 678":    "+351912345678",
		"+351912345678":  "+351912345678",
		"351912345678":   "+351912345678",
		"00351912345678": "+351912345678",
		"213456789":      "+351213456789", // fixo de Lisboa
	}
	for entrada, esperado := range casos {
		got, err := TelefonePortugues(entrada)
		if err != nil {
			t.Errorf("telefone válido rejeitado %q: %v", entrada, err)
			continue
		}
		if got != esperado {
			t.Errorf("TelefonePortugues(%q) = %q, esperado %q", entrada, got, esperado)
		}
	}

	invalidos := []string{"12345", "0123456789", "", "abc"}
	for _, s := range invalidos {
		if _, err := TelefonePortugues(s); err == nil {
			t.Errorf("telefone inválido aceite: %q", s)
		}
	}
}

func TestTelemovelPortugues(t *testing.T) {
	if !TelemovelPortugues("+351912345678") {
		t.Error("móvel não reconhecido")
	}
	if TelemovelPortugues("+351213456789") {
		t.Error("fixo classificado como móvel")
	}
}
