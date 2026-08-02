package traefik

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func writerTeste(t *testing.T) (*Writer, string) {
	t.Helper()
	dir := t.TempDir()
	return NewWriter(dir, "exemplo.pt", "http://api:8081"), dir
}

func TestWriteTenantGeraYAMLValido(t *testing.T) {
	w, dir := writerTeste(t)

	if err := w.WriteTenant(TenantRoute{Slug: "tasca-do-bairro"}); err != nil {
		t.Fatalf("WriteTenant: %v", err)
	}

	dados, err := os.ReadFile(filepath.Join(dir, "tasca-do-bairro.yml"))
	if err != nil {
		t.Fatalf("ler ficheiro: %v", err)
	}

	// O YAML tem de ser parseável: era isto que falhava com a construção por Sprintf.
	var cfg dynamicConfig
	if err := yaml.Unmarshal(dados, &cfg); err != nil {
		t.Fatalf("YAML inválido: %v\n%s", err, dados)
	}

	r, existe := cfg.HTTP.Routers["tenant-tasca-do-bairro"]
	if !existe {
		t.Fatalf("router ausente; routers=%v", cfg.HTTP.Routers)
	}
	for _, esperado := range []string{
		"Host(`tasca-do-bairro.exemplo.pt`)",
		"Host(`www.tasca-do-bairro.exemplo.pt`)",
	} {
		if !strings.Contains(r.Rule, esperado) {
			t.Errorf("regra %q não contém %q", r.Rule, esperado)
		}
	}
	if r.TLS == nil || r.TLS.CertResolver != "myresolver" {
		t.Error("certResolver ausente")
	}
}

// TestDominioPersonalizadoSoQuandoIndicado cobre C5: a rota não deve conter o domínio do
// cliente enquanto ele não estiver verificado (o chamador não o passa nesse caso).
func TestDominioPersonalizadoSoQuandoIndicado(t *testing.T) {
	w, dir := writerTeste(t)

	if err := w.WriteTenant(TenantRoute{Slug: "loja"}); err != nil {
		t.Fatal(err)
	}
	dados, _ := os.ReadFile(filepath.Join(dir, "loja.yml"))
	if strings.Contains(string(dados), "pizzaria.pt") {
		t.Error("domínio não solicitado presente na rota")
	}

	if err := w.WriteTenant(TenantRoute{Slug: "loja", CustomDomain: "pizzaria.pt"}); err != nil {
		t.Fatal(err)
	}
	dados, _ = os.ReadFile(filepath.Join(dir, "loja.yml"))
	conteudo := string(dados)
	if !strings.Contains(conteudo, "Host(`pizzaria.pt`)") {
		t.Error("domínio verificado ausente da rota")
	}
	if !strings.Contains(conteudo, "Host(`www.pizzaria.pt`)") {
		t.Error("variante www ausente")
	}
}

// TestRejeitaSlugPerigoso é a defesa em profundidade de C4 ao nível do writer.
func TestRejeitaSlugPerigoso(t *testing.T) {
	w, dir := writerTeste(t)

	perigosos := []string{
		"../default",
		"..",
		"loja/x",
		"loja`",
		"a`)||Host(`mau.pt",
		"loja\nrouters:",
		"loja com espaco",
		"",
	}

	for _, s := range perigosos {
		if err := w.WriteTenant(TenantRoute{Slug: s}); err == nil {
			t.Errorf("slug perigoso aceite pelo writer: %q", s)
		}
	}

	// Nada deve ter sido escrito fora do directório nem lá dentro.
	entradas, _ := os.ReadDir(dir)
	if len(entradas) != 0 {
		nomes := make([]string, 0, len(entradas))
		for _, e := range entradas {
			nomes = append(nomes, e.Name())
		}
		t.Errorf("ficheiros criados a partir de slugs perigosos: %v", nomes)
	}
}

// TestEscritaNaoDeixaFicheirosTemporarios garante que o rename atómico limpa o temporário.
func TestEscritaNaoDeixaFicheirosTemporarios(t *testing.T) {
	w, dir := writerTeste(t)

	for i := 0; i < 20; i++ {
		if err := w.WriteTenant(TenantRoute{Slug: "loja"}); err != nil {
			t.Fatal(err)
		}
	}

	entradas, _ := os.ReadDir(dir)
	if len(entradas) != 1 {
		nomes := make([]string, 0, len(entradas))
		for _, e := range entradas {
			nomes = append(nomes, e.Name())
		}
		t.Errorf("esperado 1 ficheiro, obtidos %d: %v", len(entradas), nomes)
	}
}

func TestPruneRemoveOrfaos(t *testing.T) {
	w, dir := writerTeste(t)

	if err := w.WriteDefault(); err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{"activa", "desactivada"} {
		if err := w.WriteTenant(TenantRoute{Slug: s}); err != nil {
			t.Fatal(err)
		}
	}

	if err := w.Prune([]string{"activa"}); err != nil {
		t.Fatalf("Prune: %v", err)
	}

	if _, err := os.Stat(filepath.Join(dir, "desactivada.yml")); !os.IsNotExist(err) {
		t.Error("rota órfã não foi removida")
	}
	for _, deveExistir := range []string{"activa.yml", "default.yml"} {
		if _, err := os.Stat(filepath.Join(dir, deveExistir)); err != nil {
			t.Errorf("%s foi removido indevidamente", deveExistir)
		}
	}
}

func TestHostRuleEstavelESemDuplicados(t *testing.T) {
	r1 := hostRule([]string{"b.pt", "a.pt", "b.pt", ""})
	r2 := hostRule([]string{"b.pt", "a.pt"})
	if r1 != r2 {
		t.Errorf("regra instável: %q vs %q", r1, r2)
	}
	if strings.Count(r1, "Host(") != 2 {
		t.Errorf("duplicados ou vazios na regra: %q", r1)
	}
}
