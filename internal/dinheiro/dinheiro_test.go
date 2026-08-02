package dinheiro

import (
	"testing"
)

func TestParseFormasQueUmLojistaEscreve(t *testing.T) {
	casos := map[string]Cents{
		"12,50":     1250,
		"12.50":     1250,
		"12":        1200,
		"12,5":      1250, // "12,5" é 12,50 e não 12,05
		"0,05":      5,
		"0,5":       50,
		",50":       50,
		"1234,56":   123456,
		"1 234,56":  123456, // separador de milhares
		"12,50 €":   1250,
		"€12,50":    1250,
		"  12,50  ": 1250,
		"0":         0,
		"0,00":      0,
	}

	for entrada, esperado := range casos {
		got, err := Parse(entrada)
		if err != nil {
			t.Errorf("Parse(%q) devolveu erro: %v", entrada, err)
			continue
		}
		if got != esperado {
			t.Errorf("Parse(%q) = %d, esperado %d", entrada, got, esperado)
		}
	}
}

// TestParseRejeitaEmVezDeTruncar: um preço com três casas decimais é um erro de
// introdução, e arredondar em silêncio seria decidir pelo lojista.
func TestParseRejeitaValoresAmbiguos(t *testing.T) {
	invalidos := []string{
		"12,505",   // três casas decimais
		"12,50,30", // dois separadores
		"abc",
		"",
		"12abc",
		"1e5", // notação científica
		"12,5€0",
		"99999999999999", // acima do limite
	}
	for _, s := range invalidos {
		if got, err := Parse(s); err == nil {
			t.Errorf("Parse(%q) foi aceite como %d", s, got)
		}
	}
}

func TestStringFormataEmPortugues(t *testing.T) {
	casos := map[Cents]string{
		0:         "0,00 €",
		5:         "0,05 €",
		50:        "0,50 €",
		1250:      "12,50 €",
		123456:    "1 234,56 €",
		100000000: "1 000 000,00 €",
		-1250:     "-12,50 €",
	}
	for entrada, esperado := range casos {
		if got := entrada.String(); got != esperado {
			t.Errorf("Cents(%d).String() = %q, esperado %q", entrada, got, esperado)
		}
	}
}

// TestSomaEhExacta é a razão de existir deste pacote.
//
// Com float64, somar 0,10 dez vezes não dá 1,00. Com inteiros, dá sempre.
func TestSomaEhExacta(t *testing.T) {
	var total Cents
	for i := 0; i < 10; i++ {
		total += 10 // 0,10 €
	}
	if total != 100 {
		t.Errorf("soma de dez linhas de 0,10 = %d cêntimos, esperado 100", total)
	}

	// O caso clássico onde float64 falha: 0,1 + 0,2 != 0,3
	if Cents(10)+Cents(20) != Cents(30) {
		t.Error("0,10 + 0,20 != 0,30 em cêntimos")
	}

	// Um cardápio realista somado mil vezes tem de dar exactamente o mesmo.
	precos := []Cents{1250, 1100, 140, 120, 990, 1190, 350, 275}
	var esperado Cents
	for _, p := range precos {
		esperado += p
	}
	for repeticao := 0; repeticao < 1000; repeticao++ {
		var soma Cents
		for _, p := range precos {
			soma += p
		}
		if soma != esperado {
			t.Fatalf("soma instável na repetição %d: %d != %d", repeticao, soma, esperado)
		}
	}
}

// TestIVAIncluidoExtraiEmVezDeSomar cobre o erro conceptual mais comum.
func TestIVAIncluidoExtraiEmVezDeSomar(t *testing.T) {
	// 12,50 € a 13%: IVA = 12,50 × 13/113 = 1,4380... -> 1,44
	iva := IVAIncluido(1250, TaxaIntermedia)
	if iva != 144 {
		t.Errorf("IVA de 12,50 € a 13%% = %d cêntimos, esperado 144", iva)
	}

	base := BaseSemIVA(1250, TaxaIntermedia)
	if base != 1106 {
		t.Errorf("base de 12,50 € a 13%% = %d cêntimos, esperado 1106", base)
	}

	// O erro que se quer evitar: 12,50 × 1,13 = 14,13, que inflaciona o preço.
	if base+iva != 1250 {
		t.Errorf("base + IVA = %d, tem de ser exactamente 1250", base+iva)
	}
}

func TestIVATaxasPortuguesas(t *testing.T) {
	casos := []struct {
		bruto    Cents
		taxa     TaxaBP
		ivaEsper Cents
	}{
		{1250, TaxaIntermedia, 144}, // prato a 12,50 € @ 13%
		{250, TaxaNormal, 47},       // cerveja a 2,50 € @ 23%
		{140, TaxaReduzida, 8},      // pastel de nata a 1,40 € @ 6%
		{100, TaxaNormal, 19},       // 1,00 € @ 23% -> 0,1869... -> 0,19
		{1000, TaxaNormal, 187},     // 10,00 € @ 23% -> 1,8699... -> 1,87
		{999, TaxaIntermedia, 115},  // 9,99 € @ 13% -> 1,1494... -> 1,15
		{1, TaxaNormal, 0},          // 0,01 € @ 23% -> 0,0018... -> 0,00
		{500, TaxaIsenta, 0},        // isento
	}

	for _, c := range casos {
		got := IVAIncluido(c.bruto, c.taxa)
		if got != c.ivaEsper {
			t.Errorf("IVAIncluido(%d, %s) = %d, esperado %d",
				c.bruto, c.taxa.Percentagem(), got, c.ivaEsper)
		}
		// A invariante que garante o "valor exato": nunca há cêntimos perdidos.
		if BaseSemIVA(c.bruto, c.taxa)+got != c.bruto {
			t.Errorf("base + IVA != bruto para %d @ %s", c.bruto, c.taxa.Percentagem())
		}
	}
}

// TestBaseMaisIVAFechaSempre percorre todos os valores até 100 € em todas as taxas.
//
// É esta a invariante que a lei exige na prática: o que se mostra ao cliente é o total, e
// a decomposição nunca pode perder nem inventar um cêntimo.
func TestBaseMaisIVAFechaSempre(t *testing.T) {
	taxas := []TaxaBP{TaxaReduzida, TaxaIntermedia, TaxaNormal, 2200, 1200, 500, 1600, 900, 400}

	for _, taxa := range taxas {
		for bruto := Cents(0); bruto <= 10000; bruto++ {
			iva := IVAIncluido(bruto, taxa)
			base := BaseSemIVA(bruto, taxa)

			if base+iva != bruto {
				t.Fatalf("taxa %s, bruto %d: base %d + IVA %d = %d",
					taxa.Percentagem(), bruto, base, iva, base+iva)
			}
			if iva < 0 || base < 0 {
				t.Fatalf("taxa %s, bruto %d: valores negativos (base %d, IVA %d)",
					taxa.Percentagem(), bruto, base, iva)
			}
			if iva > bruto {
				t.Fatalf("taxa %s, bruto %d: IVA %d maior que o bruto",
					taxa.Percentagem(), bruto, iva)
			}
		}
	}
}

// TestResumirIVAFechaComOTotal é a garantia no talão: o resumo por taxa tem de somar
// exactamente o que o cliente paga.
func TestResumirIVAFechaComOTotal(t *testing.T) {
	// Encomenda realista: dois pratos a 13%, duas cervejas a 23%, um doce a 6%.
	brutos := map[TaxaBP]Cents{
		TaxaIntermedia: 1250 + 1100,
		TaxaNormal:     250 + 250,
		TaxaReduzida:   140,
	}

	var totalEsperado Cents
	for _, v := range brutos {
		totalEsperado += v
	}

	linhas := ResumirIVA(brutos)
	if len(linhas) != 3 {
		t.Fatalf("%d linhas de IVA, esperado 3", len(linhas))
	}

	// Ordem descendente por taxa, como num talão.
	if linhas[0].Taxa != TaxaNormal || linhas[2].Taxa != TaxaReduzida {
		t.Errorf("ordem das taxas inesperada: %v, %v, %v",
			linhas[0].Taxa, linhas[1].Taxa, linhas[2].Taxa)
	}

	var somaBruto, somaBase, somaIVA Cents
	for _, l := range linhas {
		if l.Base+l.IVA != l.Bruto {
			t.Errorf("linha %s não fecha: %d + %d != %d",
				l.Taxa.Percentagem(), l.Base, l.IVA, l.Bruto)
		}
		somaBruto += l.Bruto
		somaBase += l.Base
		somaIVA += l.IVA
	}

	if somaBruto != totalEsperado {
		t.Errorf("soma dos brutos = %d, esperado %d", somaBruto, totalEsperado)
	}
	if somaBase+somaIVA != totalEsperado {
		t.Errorf("base %d + IVA %d = %d, esperado %d",
			somaBase, somaIVA, somaBase+somaIVA, totalEsperado)
	}
}

// TestResumirIVAIgnoraTaxasSemValor evita linhas a zero no talão.
func TestResumirIVAIgnoraTaxasSemValor(t *testing.T) {
	linhas := ResumirIVA(map[TaxaBP]Cents{
		TaxaIntermedia: 1000,
		TaxaNormal:     0,
	})
	if len(linhas) != 1 {
		t.Errorf("%d linhas, esperado 1 (a taxa a zero deve ser omitida)", len(linhas))
	}
}

func TestPercentagemFormata(t *testing.T) {
	casos := map[TaxaBP]string{
		2300: "23%",
		1300: "13%",
		600:  "6%",
		0:    "0%",
		1250: "12,5%",
	}
	for taxa, esperado := range casos {
		if got := taxa.Percentagem(); got != esperado {
			t.Errorf("TaxaBP(%d).Percentagem() = %q, esperado %q", taxa, got, esperado)
		}
	}
}

func TestTaxaValida(t *testing.T) {
	if !TaxaNormal.Valida() || !TaxaIsenta.Valida() {
		t.Error("taxas legítimas rejeitadas")
	}
	if TaxaBP(-1).Valida() || TaxaBP(10001).Valida() {
		t.Error("taxas fora do intervalo aceites")
	}
}
