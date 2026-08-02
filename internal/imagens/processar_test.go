package imagens

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"strings"
	"testing"
)

// pngDeTeste gera um PNG sólido das dimensões pedidas.
func pngDeTeste(t *testing.T, largura, altura int, c color.Color) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, largura, altura))
	for y := 0; y < altura; y++ {
		for x := 0; x < largura; x++ {
			img.Set(x, y, c)
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("gerar PNG: %v", err)
	}
	return buf.Bytes()
}

func jpegDeTeste(t *testing.T, largura, altura int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, largura, altura))
	for y := 0; y < altura; y++ {
		for x := 0; x < largura; x++ {
			img.Set(x, y, color.RGBA{uint8(x % 256), uint8(y % 256), 120, 255})
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, nil); err != nil {
		t.Fatalf("gerar JPEG: %v", err)
	}
	return buf.Bytes()
}

func TestRedimensionaImagemGrande(t *testing.T) {
	entrada := jpegDeTeste(t, 3000, 2000)

	r, err := Processar(bytes.NewReader(entrada), UsoProduto)
	if err != nil {
		t.Fatalf("processar: %v", err)
	}

	if r.Largura != 1200 {
		t.Errorf("largura = %d, esperado 1200", r.Largura)
	}
	// A proporção tem de ser mantida: 3000x2000 -> 1200x800.
	if r.Altura != 800 {
		t.Errorf("altura = %d, esperado 800", r.Altura)
	}
	if len(r.Dados) >= len(entrada) {
		t.Errorf("saída (%d bytes) não é menor que a entrada (%d bytes)", len(r.Dados), len(entrada))
	}
	if r.Extensao != ".jpg" {
		t.Errorf("extensão = %q, esperado .jpg", r.Extensao)
	}
}

// TestNaoAmpliaImagemPequena: ampliar só produziria uma imagem desfocada e maior.
func TestNaoAmpliaImagemPequena(t *testing.T) {
	r, err := Processar(bytes.NewReader(jpegDeTeste(t, 300, 200)), UsoProduto)
	if err != nil {
		t.Fatalf("processar: %v", err)
	}
	if r.Largura != 300 || r.Altura != 200 {
		t.Errorf("dimensões alteradas: %dx%d, esperado 300x200", r.Largura, r.Altura)
	}
}

// TestLogoComTransparenciaMantemPNG: converter para JPEG substituiria a transparência por
// preto, estragando o logótipo sobre fundo escuro.
func TestLogoComTransparenciaMantemPNG(t *testing.T) {
	transparente := pngDeTeste(t, 200, 200, color.RGBA{255, 0, 0, 128})

	r, err := Processar(bytes.NewReader(transparente), UsoLogo)
	if err != nil {
		t.Fatalf("processar: %v", err)
	}
	if r.Extensao != ".png" {
		t.Errorf("extensão = %q, esperado .png para logótipo com transparência", r.Extensao)
	}
	if !r.TemAlfa {
		t.Error("TemAlfa = false para imagem com transparência")
	}
}

// TestLogoOpacoVaiParaJPEG: sem transparência não há razão para pagar o tamanho do PNG.
func TestLogoOpacoVaiParaJPEG(t *testing.T) {
	opaco := pngDeTeste(t, 200, 200, color.RGBA{0, 100, 0, 255})

	r, err := Processar(bytes.NewReader(opaco), UsoLogo)
	if err != nil {
		t.Fatalf("processar: %v", err)
	}
	if r.Extensao != ".jpg" {
		t.Errorf("extensão = %q, esperado .jpg para logótipo opaco", r.Extensao)
	}
}

func TestLogoRedimensionaPara512(t *testing.T) {
	r, err := Processar(bytes.NewReader(jpegDeTeste(t, 2000, 2000)), UsoLogo)
	if err != nil {
		t.Fatalf("processar: %v", err)
	}
	if r.Largura != 512 {
		t.Errorf("largura do logótipo = %d, esperado 512", r.Largura)
	}
}

// TestRejeitaFicheiroQueNaoEImagem cobre o caso do ficheiro com a extensão mudada.
func TestRejeitaFicheiroQueNaoEImagem(t *testing.T) {
	naoImagens := map[string][]byte{
		"HTML":            []byte("<html><script>alert(1)</script></html>"),
		"SVG com script":  []byte(`<svg xmlns="http://www.w3.org/2000/svg" onload="alert(1)"/>`),
		"texto":           []byte("isto não é uma imagem"),
		"vazio":           {},
		"PDF":             []byte("%PDF-1.4\n%âãÏÓ\n"),
		"executável ELF":  {0x7f, 'E', 'L', 'F', 2, 1, 1, 0},
		"zip":             {'P', 'K', 3, 4},
		"cabeçalho falso": []byte("\xff\xd8\xff isto parece JPEG mas não é"),
	}

	for nome, dados := range naoImagens {
		if _, err := Processar(bytes.NewReader(dados), UsoProduto); err == nil {
			t.Errorf("%s foi aceite como imagem", nome)
		}
	}
}

// TestRejeitaFicheiroDemasiadoGrande protege a memória do servidor.
func TestRejeitaFicheiroDemasiadoGrande(t *testing.T) {
	grande := bytes.Repeat([]byte{0xff}, MaxBytesEntrada+1024)

	_, err := Processar(bytes.NewReader(grande), UsoProduto)
	if err == nil {
		t.Fatal("ficheiro acima do limite foi aceite")
	}
	if err != ErrDemasiadoGrande {
		t.Errorf("erro = %v, esperado ErrDemasiadoGrande", err)
	}
}

// TestRejeitaBombaDeDescompressao: um PNG pequeno pode declarar dimensões enormes e
// consumir vários GB ao ser descodificado.
func TestRejeitaBombaDeDescompressao(t *testing.T) {
	// 8000x8000 = 64 megapíxeis, acima do limite de 40.
	bomba := pngDeTeste(t, 8000, 8000, color.RGBA{1, 1, 1, 255})

	_, err := Processar(bytes.NewReader(bomba), UsoProduto)
	if err != ErrDimensoesExcessivas {
		t.Errorf("erro = %v, esperado ErrDimensoesExcessivas", err)
	}
}

// TestRecodificacaoRemoveMetadados é a garantia de RGPD: uma fotografia de telemóvel traz
// frequentemente as coordenadas GPS de onde foi tirada, e republicá-las expõe a localização
// de quem fotografou.
func TestRecodificacaoRemoveMetadados(t *testing.T) {
	base := jpegDeTeste(t, 400, 300)

	// Injecta um segmento APP1/Exif com um marcador reconhecível, imediatamente após o
	// SOI (os dois primeiros bytes do JPEG).
	marcador := "GPSLatitude41.1579"
	var exif bytes.Buffer
	exif.Write([]byte{0xff, 0xe1})
	conteudo := append([]byte("Exif\x00\x00"), []byte(marcador)...)
	comprimento := len(conteudo) + 2
	exif.Write([]byte{byte(comprimento >> 8), byte(comprimento & 0xff)})
	exif.Write(conteudo)

	comExif := append([]byte{}, base[:2]...)
	comExif = append(comExif, exif.Bytes()...)
	comExif = append(comExif, base[2:]...)

	// Confirma que o marcador está mesmo na entrada, ou o teste não provaria nada.
	if !bytes.Contains(comExif, []byte(marcador)) {
		t.Fatal("o marcador não foi inserido na imagem de entrada")
	}

	r, err := Processar(bytes.NewReader(comExif), UsoProduto)
	if err != nil {
		t.Fatalf("processar: %v", err)
	}

	if bytes.Contains(r.Dados, []byte(marcador)) {
		t.Error("os metadados sobreviveram à recodificação: coordenadas GPS seriam publicadas")
	}
	if bytes.Contains(r.Dados, []byte("Exif")) {
		t.Error("o segmento Exif sobreviveu à recodificação")
	}
}

// TestSaidaEhSempreImagemValida garante que o que gravamos é legível.
func TestSaidaEhSempreImagemValida(t *testing.T) {
	entradas := map[string][]byte{
		"jpeg":             jpegDeTeste(t, 1500, 1000),
		"png opaco":        pngDeTeste(t, 600, 400, color.RGBA{10, 20, 30, 255}),
		"png transparente": pngDeTeste(t, 600, 400, color.RGBA{10, 20, 30, 60}),
	}

	for nome, dados := range entradas {
		for _, uso := range []Uso{UsoProduto, UsoLogo} {
			r, err := Processar(bytes.NewReader(dados), uso)
			if err != nil {
				t.Errorf("%s (uso %d): %v", nome, uso, err)
				continue
			}
			cfg, formato, err := image.DecodeConfig(bytes.NewReader(r.Dados))
			if err != nil {
				t.Errorf("%s (uso %d): saída não é uma imagem legível: %v", nome, uso, err)
				continue
			}
			if cfg.Width != r.Largura || cfg.Height != r.Altura {
				t.Errorf("%s: dimensões reportadas %dx%d não correspondem à imagem %dx%d",
					nome, r.Largura, r.Altura, cfg.Width, cfg.Height)
			}
			if !strings.Contains(r.MimeType, formato) {
				t.Errorf("%s: mime %q não corresponde ao formato %q", nome, r.MimeType, formato)
			}
		}
	}
}
