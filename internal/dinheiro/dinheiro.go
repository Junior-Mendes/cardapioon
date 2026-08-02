// Package dinheiro representa valores monetários como inteiros em cêntimos.
//
// Todos os montantes eram float64. Isso é inadequado para dinheiro por uma razão simples:
// 0,10 não tem representação exacta em binário. Somar dez linhas de 0,10 € não dá
// exactamente 1,00 €, e a diferença aparece como um cêntimo a mais ou a menos no total.
//
// Num cardápio isso não é um detalhe estético: a lei portuguesa exige que o preço afixado
// seja o preço final exacto que o consumidor paga. Um total que não fecha com a soma das
// linhas é uma divergência na caixa e uma falha de conformidade.
//
// Com inteiros em cêntimos, a soma é exacta por construção. A única operação que introduz
// arredondamento é a extracção do IVA, e essa é feita num único ponto, com regra definida
// e testada.
package dinheiro

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// Cents é um valor monetário em cêntimos de euro. 1250 = 12,50 €.
type Cents int64

// Limite defensivo: 10 milhões de euros. Um preço acima disto é seguramente um erro de
// introdução, e permitir valores arbitrários abre a porta a overflow nos cálculos.
const MaxCents Cents = 1_000_000_000

var (
	ErrValorInvalido  = errors.New("valor monetário inválido")
	ErrValorNegativo  = errors.New("o valor não pode ser negativo")
	ErrValorExcessivo = errors.New("valor acima do limite permitido")
)

// Euros devolve o valor como float64. Usar APENAS para apresentação e serialização de
// compatibilidade — nunca para cálculo.
func (c Cents) Euros() float64 { return float64(c) / 100 }

// String formata no padrão português: 1 234,56 €.
func (c Cents) String() string {
	negativo := c < 0
	if negativo {
		c = -c
	}

	inteiros := int64(c) / 100
	centimos := int64(c) % 100

	// Separador de milhares por espaço, como é convenção em Portugal.
	s := strconv.FormatInt(inteiros, 10)
	var b strings.Builder
	for i, r := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteRune(' ')
		}
		b.WriteRune(r)
	}

	sinal := ""
	if negativo {
		sinal = "-"
	}
	return fmt.Sprintf("%s%s,%02d €", sinal, b.String(), centimos)
}

// Parse converte texto introduzido por uma pessoa em cêntimos.
//
// Aceita as formas que um lojista português escreve na prática: "12,50", "12.50", "12",
// "1 234,56", "12,5" e com o símbolo do euro. Rejeita mais de duas casas decimais em vez
// de as truncar em silêncio: um preço de "12,505" é um erro de introdução, e adivinhar
// para que lado arredondar seria decidir pelo lojista.
func Parse(s string) (Cents, error) {
	original := s
	s = strings.TrimSpace(s)
	// O símbolo do euro só é removido nas extremidades. Removê-lo em qualquer posição
	// faria com que "12,5€0" passasse a "12,50" e fosse aceite como preço válido.
	s = strings.TrimPrefix(s, "€")
	s = strings.TrimSuffix(s, "€")
	s = strings.TrimSpace(s)
	s = strings.ReplaceAll(s, " ", "") // separador de milhares
	s = strings.ReplaceAll(s, " ", "") // espaço inquebrável, comum ao copiar
	s = strings.TrimSpace(s)

	if s == "" {
		return 0, fmt.Errorf("%w: vazio", ErrValorInvalido)
	}

	negativo := strings.HasPrefix(s, "-")
	s = strings.TrimPrefix(s, "-")

	// Vírgula e ponto são ambos aceites como separador decimal.
	s = strings.ReplaceAll(s, ",", ".")
	if strings.Count(s, ".") > 1 {
		return 0, fmt.Errorf("%w: %q", ErrValorInvalido, original)
	}

	parteInteira, parteDecimal := s, ""
	if i := strings.Index(s, "."); i >= 0 {
		parteInteira, parteDecimal = s[:i], s[i+1:]
	}
	if parteInteira == "" {
		parteInteira = "0"
	}

	if !apenasDigitos(parteInteira) || !apenasDigitos(parteDecimal) {
		return 0, fmt.Errorf("%w: %q", ErrValorInvalido, original)
	}
	if len(parteDecimal) > 2 {
		return 0, fmt.Errorf("%w: %q tem mais de duas casas decimais", ErrValorInvalido, original)
	}

	inteiros, err := strconv.ParseInt(parteInteira, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%w: %q", ErrValorInvalido, original)
	}

	// "12,5" significa 12,50 e não 12,05.
	for len(parteDecimal) < 2 {
		parteDecimal += "0"
	}
	centimos, err := strconv.ParseInt(parteDecimal, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%w: %q", ErrValorInvalido, original)
	}

	if inteiros > int64(MaxCents)/100 {
		return 0, ErrValorExcessivo
	}

	total := Cents(inteiros*100 + centimos)
	if negativo {
		total = -total
	}
	if total > MaxCents || total < -MaxCents {
		return 0, ErrValorExcessivo
	}
	return total, nil
}

// DeEuros converte um float64 em cêntimos, arredondando ao cêntimo mais próximo.
//
// Existe para a fronteira com JSON, onde um cliente antigo pode enviar 12.5. Não deve ser
// usada em cálculos internos.
func DeEuros(v float64) Cents {
	if v >= 0 {
		return Cents(v*100 + 0.5)
	}
	return Cents(v*100 - 0.5)
}

func apenasDigitos(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// --- IVA ---

// TaxaBP é uma taxa de IVA em pontos base: 2300 = 23,00%.
//
// Inteiro em vez de percentagem decimal porque, tal como no dinheiro, uma taxa em float
// introduziria erro no único cálculo que já tem arredondamento. Pontos base acomodam
// qualquer taxa futura com duas casas decimais.
type TaxaBP int32

// Taxas em vigor no continente. Ficam aqui como valores por omissão configuráveis, não
// como verdade fixa: mudam por Orçamento do Estado, e a taxa aplicável a cada produto é
// uma decisão do contabilista do restaurante, não do software.
const (
	TaxaNormal       TaxaBP = 2300 // bebidas alcoólicas, refrigerantes, sumos
	TaxaIntermedia   TaxaBP = 1300 // serviços de alimentação e bebidas
	TaxaReduzida     TaxaBP = 600  // alguns produtos alimentares
	TaxaIsenta       TaxaBP = 0
	TaxaMaximaValida TaxaBP = 10000 // 100%
)

// Percentagem devolve a taxa como texto para apresentação: 1300 -> "13%".
func (t TaxaBP) Percentagem() string {
	if t%100 == 0 {
		return fmt.Sprintf("%d%%", t/100)
	}

	// Taxas com casas decimais: remove zeros à direita e usa a vírgula portuguesa.
	texto := strconv.FormatFloat(float64(t)/100, 'f', 2, 64)
	texto = strings.TrimRight(texto, "0")
	texto = strings.TrimRight(texto, ".")
	return strings.ReplaceAll(texto, ".", ",") + "%"
}

func (t TaxaBP) Valida() bool { return t >= 0 && t <= TaxaMaximaValida }

// IVAIncluido extrai o IVA contido num valor que já inclui imposto.
//
// Em Portugal o preço afixado ao consumidor final inclui IVA, pelo que o imposto se extrai
// e não se soma. A fórmula é valor × taxa / (100 + taxa); somar (valor × 1,23) trataria o
// preço como se fosse sem imposto e inflacionaria o que o cliente paga.
//
// O arredondamento é meio-para-cima, feito com aritmética inteira para não reintroduzir
// erro de vírgula flutuante.
func IVAIncluido(bruto Cents, taxa TaxaBP) Cents {
	if taxa <= 0 || bruto == 0 {
		return 0
	}

	negativo := bruto < 0
	if negativo {
		bruto = -bruto
	}

	denominador := int64(10000 + taxa)
	numerador := int64(bruto) * int64(taxa)
	// Somar metade do denominador antes da divisão inteira arredonda meio-para-cima.
	iva := Cents((numerador + denominador/2) / denominador)

	if negativo {
		return -iva
	}
	return iva
}

// BaseSemIVA devolve o valor sem imposto, garantindo que base + IVA == bruto exactamente.
//
// Calculado por subtracção de propósito: extrair a base com uma segunda divisão poderia
// dar um cêntimo de diferença e o total deixaria de fechar.
func BaseSemIVA(bruto Cents, taxa TaxaBP) Cents {
	return bruto - IVAIncluido(bruto, taxa)
}

// LinhaIVA é o resumo de uma taxa numa encomenda.
type LinhaIVA struct {
	Taxa  TaxaBP `json:"taxa_bp"`
	Bruto Cents  `json:"bruto"`
	Base  Cents  `json:"base"`
	IVA   Cents  `json:"iva"`
}

// ResumirIVA agrupa valores brutos por taxa e extrai o IVA de cada grupo.
//
// A extracção é feita sobre o total de cada taxa, não linha a linha. Extrair por linha e
// somar acumularia até meio cêntimo de erro por linha, e o talão deixaria de fechar com o
// que o cliente pagou. Assim, garante-se sempre:
//
//	soma(Base) + soma(IVA) == soma(Bruto)
func ResumirIVA(brutosPorTaxa map[TaxaBP]Cents) []LinhaIVA {
	// Ordem estável por taxa descendente, como aparece habitualmente num talão.
	taxas := make([]TaxaBP, 0, len(brutosPorTaxa))
	for t := range brutosPorTaxa {
		taxas = append(taxas, t)
	}
	for i := 0; i < len(taxas); i++ {
		for j := i + 1; j < len(taxas); j++ {
			if taxas[j] > taxas[i] {
				taxas[i], taxas[j] = taxas[j], taxas[i]
			}
		}
	}

	linhas := make([]LinhaIVA, 0, len(taxas))
	for _, t := range taxas {
		bruto := brutosPorTaxa[t]
		if bruto == 0 {
			continue
		}
		iva := IVAIncluido(bruto, t)
		linhas = append(linhas, LinhaIVA{
			Taxa: t, Bruto: bruto, Base: bruto - iva, IVA: iva,
		})
	}
	return linhas
}
