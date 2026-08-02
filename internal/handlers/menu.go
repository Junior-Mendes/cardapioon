package handlers

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"cardapio-online/internal/middleware"
	"cardapio-online/internal/models"

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

	c.JSON(http.StatusOK, gin.H{
		"restaurante": gin.H{
			"nome":                 t.Nome,
			"slug":                 t.Slug,
			"cartao_credito_ativo": t.CartaoCreditoAtivo,
			"cartao_debito_ativo":  t.CartaoDebitoAtivo,
			"dinheiro_ativo":       t.DinheiroAtivo,
		},
		"itens": itens,
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
