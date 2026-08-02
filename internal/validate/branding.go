package validate

import (
	"errors"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
)

var corHexRe = regexp.MustCompile(`^#[0-9a-f]{6}$`)

var ErrCorInvalida = errors.New("cor inválida: use o formato #RRGGBB, por exemplo #c1121f")

// CorHex normaliza e valida uma cor hexadecimal.
//
// Aceita a forma abreviada de três dígitos (#abc) e maiúsculas, normalizando sempre para
// seis dígitos em minúsculas — assim a comparação e o armazenamento são estáveis.
func CorHex(raw string) (string, error) {
	c := strings.ToLower(strings.TrimSpace(raw))
	if c == "" {
		return "", nil // vazio = usar a cor por omissão do tema
	}
	if !strings.HasPrefix(c, "#") {
		c = "#" + c
	}

	// Expande #abc para #aabbcc.
	if len(c) == 4 {
		c = fmt.Sprintf("#%c%c%c%c%c%c", c[1], c[1], c[2], c[2], c[3], c[3])
	}

	if !corHexRe.MatchString(c) {
		return "", ErrCorInvalida
	}
	return c, nil
}

// LuminanciaRelativa devolve a luminância de uma cor #RRGGBB, entre 0 (preto) e 1 (branco),
// segundo a definição da WCAG.
func LuminanciaRelativa(corHex string) float64 {
	if len(corHex) != 7 {
		return 0
	}
	canal := func(inicio int) float64 {
		v, err := strconv.ParseUint(corHex[inicio:inicio+2], 16, 8)
		if err != nil {
			return 0
		}
		s := float64(v) / 255.0
		// Linearização definida pela WCAG.
		if s <= 0.04045 {
			return s / 12.92
		}
		return math.Pow((s+0.055)/1.055, 2.4)
	}
	return 0.2126*canal(1) + 0.7152*canal(3) + 0.0722*canal(5)
}

// TextoSobreCor indica se o texto sobre a cor indicada deve ser escuro ou claro.
//
// O lojista escolhe a cor da marca sem pensar em contraste. Sem isto, uma cor clara como
// #ffe66d ficaria com texto branco por cima e o botão de encomendar tornar-se-ia ilegível
// — o que custa vendas e falha os critérios de acessibilidade.
func TextoSobreCor(corHex string) string {
	// O limiar 0.45 corresponde aproximadamente ao ponto em que o contraste com branco
	// deixa de cumprir 4.5:1.
	if LuminanciaRelativa(corHex) > 0.45 {
		return "#111111"
	}
	return "#ffffff"
}

var ErrLogoInvalido = errors.New("o endereço do logótipo tem de começar por https:// ou ser um ficheiro carregado")

// LogoURL valida o endereço do logótipo.
//
// Restringido a HTTPS ou a uploads locais, pelas mesmas razões das imagens de produtos:
// impede javascript: e data: em atributos src, e evita conteúdo misto numa página HTTPS.
func LogoURL(raw string) (string, error) {
	u := strings.TrimSpace(raw)
	if u == "" {
		return "", nil
	}
	if len(u) > 1000 {
		return "", ErrLogoInvalido
	}
	l := strings.ToLower(u)
	if !strings.HasPrefix(l, "https://") && !strings.HasPrefix(l, "/static/uploads/") {
		return "", ErrLogoInvalido
	}
	// Um newline no URL permitiria injectar cabeçalhos ou quebrar atributos HTML.
	if strings.ContainsAny(u, "\r\n\"'<>`") {
		return "", ErrLogoInvalido
	}
	return u, nil
}
