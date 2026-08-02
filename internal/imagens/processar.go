// Package imagens processa as imagens carregadas pelos lojistas.
//
// A imagem é sempre descodificada e recodificada, nunca guardada como veio. Isso não é só
// para redimensionar:
//
//   - Remove os metadados EXIF. Uma fotografia tirada com telemóvel traz frequentemente as
//     coordenadas GPS de onde foi tirada. Republicá-las no storefront expõe a localização
//     de quem fotografou, o que é um problema de RGPD que ninguém espera de uma foto de
//     um prato.
//   - Neutraliza ficheiros poliglotas. Um ficheiro pode ser simultaneamente um JPEG válido
//     e HTML com JavaScript; servido do nosso domínio, esse script correria com a nossa
//     origem. Recodificar destrói tudo o que não seja a matriz de píxeis.
//   - Garante que o que guardamos é de facto uma imagem, e não um ficheiro com a extensão
//     mudada.
package imagens

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/draw"
	"image/jpeg"
	"image/png"
	"io"

	// Alias necessário: colide com image/draw da stdlib. Este fornece o resampling de
	// qualidade (CatmullRom) que a stdlib não tem.
	xdraw "golang.org/x/image/draw"
	// Registo do descodificador WebP, cada vez mais comum em imagens de telemóvel.
	_ "golang.org/x/image/webp"
)

const (
	// MaxBytesEntrada limita o ficheiro recebido. 8 MB acomoda uma fotografia de telemóvel
	// moderno sem permitir esgotar a memória do servidor.
	MaxBytesEntrada = 8 << 20

	// Limites de dimensão após redimensionamento.
	maxLarguraProduto = 1200
	maxLarguraLogo    = 512

	qualidadeJPEG = 82

	// maxPixeisEntrada trava a "bomba de descompressão": um PNG de poucos KB pode declarar
	// 30000x30000 e consumir vários GB ao ser descodificado.
	maxPixeisEntrada = 40 << 20 // 40 megapíxeis
)

var (
	ErrFormatoNaoSuportado = errors.New("formato não suportado: use JPEG, PNG ou WebP")
	ErrDemasiadoGrande     = errors.New("a imagem excede o tamanho máximo de 8 MB")
	ErrDimensoesExcessivas = errors.New("a imagem tem demasiados píxeis")
	ErrImagemInvalida      = errors.New("não foi possível ler a imagem")
)

// Tipo de utilização, que define os limites aplicados.
type Uso int

const (
	UsoProduto Uso = iota
	UsoLogo
)

// Resultado do processamento.
type Resultado struct {
	Dados    []byte
	Extensao string // ".jpg" ou ".png"
	MimeType string
	Largura  int
	Altura   int
	TemAlfa  bool
}

// Processar lê, valida, redimensiona e recodifica a imagem.
func Processar(r io.Reader, uso Uso) (*Resultado, error) {
	// LimitReader com um byte extra permite distinguir "exactamente no limite" de
	// "acima do limite".
	dados, err := io.ReadAll(io.LimitReader(r, MaxBytesEntrada+1))
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrImagemInvalida, err)
	}
	if len(dados) > MaxBytesEntrada {
		return nil, ErrDemasiadoGrande
	}
	if len(dados) == 0 {
		return nil, ErrImagemInvalida
	}

	// A configuração é lida antes da imagem completa, para rejeitar dimensões absurdas
	// sem alocar memória para elas.
	cfg, formato, err := image.DecodeConfig(bytes.NewReader(dados))
	if err != nil {
		return nil, ErrFormatoNaoSuportado
	}
	switch formato {
	case "jpeg", "png", "webp":
	default:
		return nil, ErrFormatoNaoSuportado
	}
	if cfg.Width <= 0 || cfg.Height <= 0 {
		return nil, ErrImagemInvalida
	}
	if cfg.Width*cfg.Height > maxPixeisEntrada {
		return nil, ErrDimensoesExcessivas
	}

	origem, _, err := image.Decode(bytes.NewReader(dados))
	if err != nil {
		return nil, ErrFormatoNaoSuportado
	}

	maxLargura := maxLarguraProduto
	if uso == UsoLogo {
		maxLargura = maxLarguraLogo
	}

	redimensionada := redimensionar(origem, maxLargura)
	temAlfa := formato == "png" || formato == "webp"

	// Logótipos com transparência mantêm-se em PNG: converter para JPEG substituiria a
	// transparência por preto, o que estragaria o logótipo sobre fundo escuro.
	if uso == UsoLogo && temAlfa && contemTransparencia(redimensionada) {
		var buf bytes.Buffer
		codificador := png.Encoder{CompressionLevel: png.BestCompression}
		if err := codificador.Encode(&buf, redimensionada); err != nil {
			return nil, fmt.Errorf("codificar PNG: %w", err)
		}
		limites := redimensionada.Bounds()
		return &Resultado{
			Dados: buf.Bytes(), Extensao: ".png", MimeType: "image/png",
			Largura: limites.Dx(), Altura: limites.Dy(), TemAlfa: true,
		}, nil
	}

	// Sem transparência, JPEG produz ficheiros muito menores para fotografias de pratos.
	// O fundo branco evita que zonas transparentes fiquem negras.
	achatada := achatarSobreBranco(redimensionada)

	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, achatada, &jpeg.Options{Quality: qualidadeJPEG}); err != nil {
		return nil, fmt.Errorf("codificar JPEG: %w", err)
	}

	limites := achatada.Bounds()
	return &Resultado{
		Dados: buf.Bytes(), Extensao: ".jpg", MimeType: "image/jpeg",
		Largura: limites.Dx(), Altura: limites.Dy(),
	}, nil
}

// redimensionar reduz a imagem para caber em maxLargura, mantendo a proporção.
// Imagens já pequenas não são ampliadas.
func redimensionar(origem image.Image, maxLargura int) image.Image {
	limites := origem.Bounds()
	largura, altura := limites.Dx(), limites.Dy()

	if largura <= maxLargura {
		return origem
	}

	novaAltura := altura * maxLargura / largura
	if novaAltura < 1 {
		novaAltura = 1
	}

	destino := image.NewRGBA(image.Rect(0, 0, maxLargura, novaAltura))
	// CatmullRom dá o melhor resultado visível numa redução deste tipo; o custo extra é
	// irrelevante para um upload pontual.
	xdraw.CatmullRom.Scale(destino, destino.Bounds(), origem, limites, xdraw.Over, nil)
	return destino
}

// contemTransparencia indica se algum píxel não é totalmente opaco.
func contemTransparencia(img image.Image) bool {
	limites := img.Bounds()
	for y := limites.Min.Y; y < limites.Max.Y; y++ {
		for x := limites.Min.X; x < limites.Max.X; x++ {
			if _, _, _, a := img.At(x, y).RGBA(); a < 0xffff {
				return true
			}
		}
	}
	return false
}

// achatarSobreBranco compõe a imagem sobre fundo branco, para que o JPEG (que não tem
// canal alfa) não transforme as zonas transparentes em preto.
func achatarSobreBranco(img image.Image) image.Image {
	limites := img.Bounds()
	destino := image.NewRGBA(image.Rect(0, 0, limites.Dx(), limites.Dy()))
	draw.Draw(destino, destino.Bounds(), image.White, image.Point{}, draw.Src)
	draw.Draw(destino, destino.Bounds(), img, limites.Min, draw.Over)
	return destino
}
