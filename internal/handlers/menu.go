package handlers

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"cardapio-online/internal/middleware"
	"cardapio-online/internal/models"
	"cardapio-online/internal/validate"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// GetMenu devolve o cardápio completo para o painel do lojista.
func (h *Handler) GetMenu(c *gin.Context) {
	var itens []models.MenuItem
	if err := h.DB.Scopes(middleware.TenantScope(c)).
		Order("categoria asc, nome asc").Find(&itens).Error; err != nil {
		h.erroInterno(c, "listar cardápio", err)
		return
	}
	c.JSON(http.StatusOK, itens)
}

// GetPublicMenu devolve o cardápio público de um restaurante.
func (h *Handler) GetPublicMenu(c *gin.Context) {
	t, err := h.tenantDoContexto(c)
	if err != nil {
		erroCliente(c, http.StatusNotFound, "Restaurante não encontrado")
		return
	}

	var itens []models.MenuItem
	if err := h.DB.Where("tenant_id = ? AND disponivel = ?", t.ID, true).
		Order("categoria asc, nome asc").Find(&itens).Error; err != nil {
		h.erroInterno(c, "listar cardápio público", err)
		return
	}

	restaurante := gin.H{
		"nome": t.Nome,
		"slug": t.Slug,
		// Métodos aceites na caixa. No MVP o serviço é sempre levantamento ao balcão
		// com pagamento no local.
		"dinheiro_ativo": t.DinheiroAtivo,
		"cartao_ativo":   t.CartaoDebitoAtivo,
		"tipo_servico":   "levantamento",

		// Identidade visual: é isto que faz o storefront parecer do restaurante e não
		// da plataforma.
		"logo_url":                 t.LogoURL,
		"descricao_curta":          t.DescricaoCurta,
		"mostrar_marca_plataforma": t.MostrarMarcaPlataforma,
		"iniciais":                 iniciaisDe(t.Nome),
	}

	if t.CorPrimaria != "" {
		restaurante["cor_primaria"] = t.CorPrimaria
		// A cor de texto é calculada no servidor a partir da luminância: o lojista escolhe
		// a cor da marca sem pensar em contraste, e uma cor clara com texto branco por
		// cima tornaria o botão de encomendar ilegível.
		restaurante["cor_texto_sobre_primaria"] = validate.TextoSobreCor(t.CorPrimaria)
	}
	if t.CorSecundaria != "" {
		restaurante["cor_secundaria"] = t.CorSecundaria
	}

	c.JSON(http.StatusOK, gin.H{
		"restaurante": restaurante,
		"itens":       itens,
	})
}

type menuItemInput struct {
	Nome          string  `json:"nome" binding:"required,min=1,max=150"`
	Descricao     string  `json:"descricao" binding:"max=2000"`
	Preco         float64 `json:"preco" binding:"required,gt=0"`
	PrecoDesconto float64 `json:"preco_desconto" binding:"gte=0"`
	DescontoAtivo bool    `json:"desconto_ativo"`
	Categoria     string  `json:"categoria" binding:"required,min=1,max=50"`
	ImagemURL     string  `json:"imagem_url" binding:"max=1000"`
	Disponivel    *bool   `json:"disponivel"`
}

// validar aplica as regras que o binding não cobre.
func (in *menuItemInput) validar() error {
	if in.Preco > 100000 {
		return errors.New("preço acima do limite permitido")
	}
	// A versão anterior aceitava um desconto superior ao preço, o que produzia encomendas
	// com valor negativo.
	if in.DescontoAtivo {
		if in.PrecoDesconto <= 0 {
			return errors.New("indique o preço com desconto")
		}
		if in.PrecoDesconto >= in.Preco {
			return errors.New("o preço com desconto tem de ser inferior ao preço normal")
		}
	}
	if in.ImagemURL != "" && !urlImagemAceitavel(in.ImagemURL) {
		return errors.New("o endereço da imagem tem de começar por https:// ou ser um ficheiro carregado")
	}
	return nil
}

// urlImagemAceitavel restringe as imagens a HTTPS ou a uploads locais.
//
// Impede javascript: e data: em atributos src, e evita conteúdo misto (imagem em HTTP
// numa página HTTPS), que os browsers bloqueiam.
func urlImagemAceitavel(u string) bool {
	l := strings.ToLower(strings.TrimSpace(u))
	return strings.HasPrefix(l, "https://") || strings.HasPrefix(l, "/static/uploads/")
}

func (in *menuItemInput) aplicar(item *models.MenuItem) {
	item.Nome = limparLinha(in.Nome, 150)
	item.Descricao = limparTexto(in.Descricao, 2000)
	item.Preco = arredondarCentimos(in.Preco)
	item.PrecoDesconto = arredondarCentimos(in.PrecoDesconto)
	item.DescontoAtivo = in.DescontoAtivo
	item.Categoria = limparLinha(in.Categoria, 50)
	item.ImagemURL = strings.TrimSpace(in.ImagemURL)
	if in.Disponivel != nil {
		item.Disponivel = *in.Disponivel
	}
}

// arredondarCentimos limita os valores a duas casas decimais.
//
// Mitigação temporária: os montantes continuam em float64 e a migração para inteiros em
// cêntimos está planeada para a Fase 1. Sem este arredondamento, um preço enviado com
// mais casas decimais era truncado pelo MySQL de forma inconsistente com o total
// calculado em Go.
func arredondarCentimos(v float64) float64 {
	return float64(int64(v*100+0.5)) / 100
}

// iniciaisDe devolve até duas iniciais do nome, usadas como logótipo de recurso quando o
// restaurante ainda não carregou um. Melhor do que a letra "C" da plataforma, que era o
// que aparecia antes.
func iniciaisDe(nome string) string {
	palavras := strings.Fields(nome)
	var iniciais []rune

	for _, p := range palavras {
		r := []rune(p)
		if len(r) == 0 {
			continue
		}
		// Ignora ligações comuns em nomes portugueses: "Tasca do Bairro" -> "TB".
		minuscula := strings.ToLower(p)
		if len(iniciais) > 0 && (minuscula == "do" || minuscula == "da" || minuscula == "de" ||
			minuscula == "dos" || minuscula == "das" || minuscula == "e" || minuscula == "&") {
			continue
		}
		iniciais = append(iniciais, []rune(strings.ToUpper(string(r[0])))[0])
		if len(iniciais) == 2 {
			break
		}
	}

	if len(iniciais) == 0 {
		return "?"
	}
	return string(iniciais)
}

// CreateMenuItem cria um item de cardápio.
func (h *Handler) CreateMenuItem(c *gin.Context) {
	var in menuItemInput
	if err := c.ShouldBindJSON(&in); err != nil {
		erroCliente(c, http.StatusBadRequest, "Dados do produto inválidos")
		return
	}
	if err := in.validar(); err != nil {
		erroCliente(c, http.StatusBadRequest, err.Error())
		return
	}

	item := models.MenuItem{TenantID: middleware.GetTenantID(c), Disponivel: true}
	in.aplicar(&item)
	// O tenant vem sempre do token: um tenant_id enviado no corpo é ignorado.
	item.TenantID = middleware.GetTenantID(c)

	if err := h.DB.Create(&item).Error; err != nil {
		h.erroInterno(c, "criar item de cardápio", err)
		return
	}

	h.auditar(c, "produto_criado", "menu_item", fmt.Sprint(item.ID), item.Nome)
	c.JSON(http.StatusCreated, item)
}

// UpdateMenuItem edita um item existente.
func (h *Handler) UpdateMenuItem(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		erroCliente(c, http.StatusBadRequest, "Identificador inválido")
		return
	}

	// O escopo de tenant na leitura é o que impede editar o produto de outro restaurante.
	var item models.MenuItem
	if err := h.DB.Scopes(middleware.TenantScope(c)).Where("id = ?", id).First(&item).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			erroCliente(c, http.StatusNotFound, "Produto não encontrado")
			return
		}
		h.erroInterno(c, "procurar item de cardápio", err)
		return
	}

	var in menuItemInput
	if err := c.ShouldBindJSON(&in); err != nil {
		erroCliente(c, http.StatusBadRequest, "Dados do produto inválidos")
		return
	}
	if err := in.validar(); err != nil {
		erroCliente(c, http.StatusBadRequest, err.Error())
		return
	}

	in.aplicar(&item)

	if err := h.DB.Save(&item).Error; err != nil {
		h.erroInterno(c, "actualizar item de cardápio", err)
		return
	}

	h.auditar(c, "produto_actualizado", "menu_item", fmt.Sprint(item.ID), item.Nome)
	c.JSON(http.StatusOK, item)
}

// DeleteMenuItem remove um item do cardápio.
func (h *Handler) DeleteMenuItem(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		erroCliente(c, http.StatusBadRequest, "Identificador inválido")
		return
	}

	res := h.DB.Scopes(middleware.TenantScope(c)).Where("id = ?", id).Delete(&models.MenuItem{})
	if res.Error != nil {
		h.erroInterno(c, "remover item de cardápio", res.Error)
		return
	}
	// A versão anterior respondia sempre com sucesso, mesmo quando nada era apagado —
	// incluindo quando o ID pertencia a outro restaurante.
	if res.RowsAffected == 0 {
		erroCliente(c, http.StatusNotFound, "Produto não encontrado")
		return
	}

	h.auditar(c, "produto_removido", "menu_item", fmt.Sprint(id), "")
	c.JSON(http.StatusOK, gin.H{"message": "Produto removido"})
}
