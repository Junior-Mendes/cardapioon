package handlers

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"cardapio-online/internal/auth"
	"cardapio-online/internal/imagens"
	"cardapio-online/internal/middleware"

	"github.com/gin-gonic/gin"
)

// nomeCampoFicheiro é o nome do campo esperado no formulário multipart.
const nomeCampoFicheiro = "ficheiro"

// UploadImagem recebe uma imagem do computador ou telemóvel do lojista, processa-a e
// devolve o endereço público.
//
// Antes disto o lojista só podia colar um URL externo, o que tinha três problemas: exigia
// que ele soubesse alojar imagens em algum sítio, deixava o storefront dependente de um
// domínio de terceiros que pode desaparecer, e permitia conteúdo misto se o URL fosse HTTP.
func (h *Handler) UploadImagem(c *gin.Context) {
	tenantID := middleware.GetTenantID(c)
	if tenantID == 0 {
		erroCliente(c, http.StatusUnauthorized, "Sessão inválida")
		return
	}

	uso := imagens.UsoProduto
	if c.Query("uso") == "logo" {
		uso = imagens.UsoLogo
	}

	// Rejeitar cedo pelo Content-Length evita ler megabytes antes de saber que o ficheiro
	// é grande demais. Não é fiável por si (pode ser omitido ou mentir), por isso o
	// processamento volta a limitar a leitura.
	if c.Request.ContentLength > imagens.MaxBytesEntrada+(1<<20) {
		erroCliente(c, http.StatusRequestEntityTooLarge, imagens.ErrDemasiadoGrande.Error())
		return
	}

	cabecalho, err := c.FormFile(nomeCampoFicheiro)
	if err != nil {
		erroCliente(c, http.StatusBadRequest,
			fmt.Sprintf("Nenhum ficheiro recebido no campo %q", nomeCampoFicheiro))
		return
	}
	if cabecalho.Size > imagens.MaxBytesEntrada {
		erroCliente(c, http.StatusRequestEntityTooLarge, imagens.ErrDemasiadoGrande.Error())
		return
	}

	ficheiro, err := cabecalho.Open()
	if err != nil {
		h.erroInterno(c, "abrir ficheiro carregado", err)
		return
	}
	defer ficheiro.Close()

	// O tipo declarado pelo cliente não é considerado: o formato real é determinado pelo
	// conteúdo, ao descodificar.
	resultado, err := imagens.Processar(ficheiro, uso)
	if err != nil {
		switch {
		case errors.Is(err, imagens.ErrFormatoNaoSuportado),
			errors.Is(err, imagens.ErrImagemInvalida):
			erroCliente(c, http.StatusUnsupportedMediaType, err.Error())
		case errors.Is(err, imagens.ErrDemasiadoGrande),
			errors.Is(err, imagens.ErrDimensoesExcessivas):
			erroCliente(c, http.StatusRequestEntityTooLarge, err.Error())
		default:
			h.erroInterno(c, "processar imagem carregada", err)
		}
		return
	}

	caminhoPublico, err := h.gravarImagem(tenantID, resultado)
	if err != nil {
		h.erroInterno(c, "gravar imagem carregada", err)
		return
	}

	h.auditar(c, "imagem_carregada", "upload", caminhoPublico,
		fmt.Sprintf("%dx%d, %d KB", resultado.Largura, resultado.Altura, len(resultado.Dados)/1024))

	c.JSON(http.StatusCreated, gin.H{
		"url":     caminhoPublico,
		"largura": resultado.Largura,
		"altura":  resultado.Altura,
		"bytes":   len(resultado.Dados),
	})
}

// gravarImagem escreve o ficheiro processado e devolve o caminho público.
//
// O ficheiro vai para um subdirectório por tenant e recebe um nome aleatório. O nome
// original nunca é usado: além de poder conter caracteres que escapam do directório,
// revelaria informação do computador do lojista.
func (h *Handler) gravarImagem(tenantID uint, r *imagens.Resultado) (string, error) {
	subdir := fmt.Sprintf("t%d", tenantID)
	dir := filepath.Join(h.Cfg.UploadDir, subdir)

	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("criar directório %s: %w", dir, err)
	}

	// Nome aleatório: torna os endereços não enumeráveis e evita colisões sem consultar
	// o sistema de ficheiros.
	nome := strings.ToLower(auth.NewOpaqueToken(16)) + r.Extensao
	// NewOpaqueToken usa base64 URL-safe, que inclui '-' e '_'; ambos são seguros num
	// nome de ficheiro, mas confirmamos que nada mais entrou.
	if strings.ContainsAny(nome, "/\\.:") && !strings.HasSuffix(nome, r.Extensao) {
		return "", errors.New("nome de ficheiro gerado inválido")
	}

	destino := filepath.Join(dir, nome)

	// Escrita atómica: um pedido interrompido não deixa um ficheiro truncado a ser servido.
	temporario, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return "", fmt.Errorf("criar ficheiro temporário: %w", err)
	}
	nomeTemporario := temporario.Name()
	defer os.Remove(nomeTemporario)

	if _, err := temporario.Write(r.Dados); err != nil {
		temporario.Close()
		return "", fmt.Errorf("escrever imagem: %w", err)
	}
	if err := temporario.Sync(); err != nil {
		temporario.Close()
		return "", fmt.Errorf("sync: %w", err)
	}
	if err := temporario.Close(); err != nil {
		return "", fmt.Errorf("fechar ficheiro temporário: %w", err)
	}
	if err := os.Chmod(nomeTemporario, 0o644); err != nil {
		return "", fmt.Errorf("chmod: %w", err)
	}
	if err := os.Rename(nomeTemporario, destino); err != nil {
		return "", fmt.Errorf("rename: %w", err)
	}

	slog.Info("imagem carregada",
		"tenant_id", tenantID, "ficheiro", nome,
		"largura", r.Largura, "altura", r.Altura, "bytes", len(r.Dados))

	// O caminho devolvido é o que o frontend grava em imagem_url / logo_url, e é aceite
	// pela validação de URL (prefixo /static/uploads/).
	return fmt.Sprintf("/static/uploads/%s/%s", subdir, nome), nil
}
