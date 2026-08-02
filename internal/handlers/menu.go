package handlers

import (
	"net/http"
	"strconv"

	"cardapio-online/internal/db"
	"cardapio-online/internal/middleware"
	"cardapio-online/internal/models"

	"github.com/gin-gonic/gin"
)

type MenuHandler struct{}

func NewMenuHandler() *MenuHandler {
	return &MenuHandler{}
}

// GetMenu retorna todos os itens do cardápio para a administração (todos os itens)
func (h *MenuHandler) GetMenu(c *gin.Context) {
	var items []models.MenuItem
	if err := db.DB.Scopes(middleware.TenantScope(c)).Order("categoria asc, nome asc").Find(&items).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Erro ao buscar cardápio: " + err.Error()})
		return
	}
	c.JSON(http.StatusOK, items)
}

// GetPublicMenu retorna dados públicos do restaurante e itens ativados
func (h *MenuHandler) GetPublicMenu(c *gin.Context) {
	tenant, err := ResolveTenant(c)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Restaurante não localizado"})
		return
	}

	var items []models.MenuItem
	if err := db.DB.Where("tenant_id = ? AND disponivel = ?", tenant.ID, true).Order("categoria asc, nome asc").Find(&items).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Erro ao buscar itens do cardápio"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"restaurante": gin.H{
			"nome":                 tenant.Nome,
			"slug":                 tenant.Slug,
			"domain":               tenant.Domain,
			"pix_ativo":            tenant.PixAtivo,
			"pix_chave":            tenant.PixChave,
			"cartao_credito_ativo": tenant.CartaoCreditoAtivo,
			"cartao_debito_ativo":  tenant.CartaoDebitoAtivo,
			"dinheiro_ativo":      tenant.DinheiroAtivo,
		},
		"itens": items,
	})
}

// CreateMenuItem cria um item no cardápio
func (h *MenuHandler) CreateMenuItem(c *gin.Context) {
	var item models.MenuItem
	if err := c.ShouldBindJSON(&item); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Dados inválidos: " + err.Error()})
		return
	}

	item.TenantID = middleware.GetTenantID(c)

	if err := db.DB.Create(&item).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Erro ao salvar item do cardápio: " + err.Error()})
		return
	}
	c.JSON(http.StatusCreated, item)
}

// UpdateMenuItem edita um item existente
func (h *MenuHandler) UpdateMenuItem(c *gin.Context) {
	idStr := c.Param("id")
	id, err := strconv.ParseUint(idStr, 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "ID de item inválido"})
		return
	}

	var existing models.MenuItem
	if err := db.DB.Scopes(middleware.TenantScope(c)).First(&existing, id).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Item do cardápio não localizado"})
		return
	}

	var updateData models.MenuItem
	if err := c.ShouldBindJSON(&updateData); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Dados inválidos: " + err.Error()})
		return
	}

	existing.Nome = updateData.Nome
	existing.Descricao = updateData.Descricao
	existing.Preco = updateData.Preco
	existing.PrecoDesconto = updateData.PrecoDesconto
	existing.DescontoAtivo = updateData.DescontoAtivo
	existing.Categoria = updateData.Categoria
	existing.ImagemURL = updateData.ImagemURL
	existing.Disponivel = updateData.Disponivel

	if err := db.DB.Save(&existing).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Erro ao atualizar item do cardápio: " + err.Error()})
		return
	}
	c.JSON(http.StatusOK, existing)
}

// DeleteMenuItem remove um item do cardápio
func (h *MenuHandler) DeleteMenuItem(c *gin.Context) {
	idStr := c.Param("id")
	id, err := strconv.ParseUint(idStr, 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "ID de item inválido"})
		return
	}

	if err := db.DB.Scopes(middleware.TenantScope(c)).Where("id = ?", id).Delete(&models.MenuItem{}).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Erro ao deletar item: " + err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "Item excluído com sucesso"})
}
