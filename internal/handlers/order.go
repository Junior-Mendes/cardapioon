package handlers

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"cardapio-online/internal/db"
	"cardapio-online/internal/middleware"
	"cardapio-online/internal/models"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type OrderHandler struct{}

func NewOrderHandler() *OrderHandler {
	return &OrderHandler{}
}

type OrderItemInput struct {
	MenuItemID uint `json:"menu_item_id" binding:"required"`
	Quantidade int  `json:"quantidade" binding:"required,gt=0"`
}

type CreateOrderInput struct {
	ClienteNome          string           `json:"cliente_nome" binding:"required"`
	ClienteTelefone      string           `json:"cliente_telefone" binding:"required"`
	FormaPagamento       string           `json:"forma_pagamento" binding:"required"` // pix, cartao_credito, retirada_dinheiro, retirada_cartao
	TrocoPara            float64          `json:"troco_para"`
	CartaoUltimosDigitos string           `json:"cartao_ultimos_digitos"`
	Itens                []OrderItemInput `json:"itens" binding:"required,gt=0"`
}

// CreateOrder cria um novo pedido para retirada
func (h *OrderHandler) CreateOrder(c *gin.Context) {
	tenant, err := ResolveTenant(c)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Restaurante não localizado"})
		return
	}

	var input CreateOrderInput
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Dados do pedido inválidos: " + err.Error()})
		return
	}

	// Valida se o método de pagamento selecionado está ativo no lojista
	switch input.FormaPagamento {
	case "pix":
		if !tenant.PixAtivo {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Pagamento por PIX não está disponível neste estabelecimento"})
			return
		}
	case "cartao_credito":
		if !tenant.CartaoCreditoAtivo {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Pagamento online por Cartão de Crédito não disponível"})
			return
		}
	case "retirada_dinheiro":
		if !tenant.DinheiroAtivo {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Pagamento em dinheiro na retirada não disponível"})
			return
		}
	case "retirada_cartao":
		if !tenant.CartaoDebitoAtivo {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Pagamento em cartão na maquininha na retirada não disponível"})
			return
		}
	default:
		c.JSON(http.StatusBadRequest, gin.H{"error": "Método de pagamento inválido"})
		return
	}

	// Inicia transação de banco de dados
	tx := db.DB.Begin()

	var valorTotal float64 = 0
	var orderItems []models.OrderItem

	// Busca os itens no cardápio para calcular o preço real e validar se estão disponíveis
	for _, itemInput := range input.Itens {
		var menuItem models.MenuItem
		if err := tx.Where("id = ? AND tenant_id = ? AND disponivel = ?", itemInput.MenuItemID, tenant.ID, true).First(&menuItem).Error; err != nil {
			tx.Rollback()
			c.JSON(http.StatusBadRequest, gin.H{"error": "Item do cardápio inválido ou indisponível: ID " + strconv.Itoa(int(itemInput.MenuItemID))})
			return
		}

		// Determina o preço unitário considerando se há desconto ativo
		precoUnitario := menuItem.Preco
		if menuItem.DescontoAtivo && menuItem.PrecoDesconto > 0 {
			precoUnitario = menuItem.PrecoDesconto
		}

		valorTotal += precoUnitario * float64(itemInput.Quantidade)

		orderItems = append(orderItems, models.OrderItem{
			Nome:          menuItem.Nome,
			Quantidade:    itemInput.Quantidade,
			PrecoUnitario: precoUnitario,
			CreatedAt:     time.Now(),
		})
	}

	// Cria a struct do Pedido
	pedido := models.Pedido{
		TenantID:             tenant.ID,
		ClienteNome:          input.ClienteNome,
		ClienteTelefone:      input.ClienteTelefone,
		Status:               models.StatusPendente,
		ValorTotal:           valorTotal,
		FormaPagamento:       input.FormaPagamento,
		TrocoPara:            input.TrocoPara,
		PixPago:              input.FormaPagamento == "pix", // Simulado: Pix é marcado como pago se selecionado
		CartaoUltimosDigitos: input.CartaoUltimosDigitos,
		CreatedAt:            time.Now(),
		UpdatedAt:            time.Now(),
	}

	if err := tx.Create(&pedido).Error; err != nil {
		tx.Rollback()
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Falha ao criar registro do pedido: " + err.Error()})
		return
	}

	// Insere os itens do pedido associando com o ID recém-criado
	for i := range orderItems {
		orderItems[i].PedidoID = pedido.ID
		if err := tx.Create(&orderItems[i]).Error; err != nil {
			tx.Rollback()
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Falha ao registrar itens do pedido: " + err.Error()})
			return
		}
	}

	if err := tx.Commit().Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Erro ao confirmar transação do banco"})
		return
	}

	// Retorna o pedido criado com sucesso
	c.JSON(http.StatusCreated, gin.H{
		"message": "Pedido criado com sucesso!",
		"order": gin.H{
			"id":         pedido.ID,
			"valor_total": pedido.ValorTotal,
			"status":     pedido.Status,
		},
	})
}

// GetOrder consulta o pedido pelo ID (endpoint público de rastreamento)
func (h *OrderHandler) GetOrder(c *gin.Context) {
	idStr := c.Param("id")
	id, err := strconv.ParseUint(idStr, 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "ID de pedido inválido"})
		return
	}

	var pedido models.Pedido
	// Carrega o pedido junto com os itens dele
	if err := db.DB.Preload("Itens").First(&pedido, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "Pedido não encontrado"})
		} else {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Erro ao consultar pedido"})
		}
		return
	}

	// Busca o nome do restaurante
	var tenant models.Tenant
	db.DB.Select("nome, slug").First(&tenant, pedido.TenantID)

	c.JSON(http.StatusOK, gin.H{
		"id":                     pedido.ID,
		"restaurante_nome":       tenant.Nome,
		"restaurante_slug":       tenant.Slug,
		"cliente_nome":           pedido.ClienteNome,
		"cliente_telefone":       pedido.ClienteTelefone,
		"status":                 pedido.Status,
		"valor_total":            pedido.ValorTotal,
		"forma_pagamento":        pedido.FormaPagamento,
		"troco_para":             pedido.TrocoPara,
		"pix_pago":               pedido.PixPago,
		"cartao_ultimos_digitos": pedido.CartaoUltimosDigitos,
		"created_at":             pedido.CreatedAt,
		"itens":                  pedido.Itens,
	})
}

// GetAdminOrders lista todos os pedidos do tenant (painel do lojista)
func (h *OrderHandler) GetAdminOrders(c *gin.Context) {
	var pedidos []models.Pedido
	if err := db.DB.Scopes(middleware.TenantScope(c)).Preload("Itens").Order("id desc").Find(&pedidos).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Erro ao buscar pedidos: " + err.Error()})
		return
	}
	c.JSON(http.StatusOK, pedidos)
}

type UpdateStatusInput struct {
	Status string `json:"status" binding:"required"`
}

// UpdateOrderStatus altera o status do pedido (painel do lojista)
func (h *OrderHandler) UpdateOrderStatus(c *gin.Context) {
	idStr := c.Param("id")
	id, err := strconv.ParseUint(idStr, 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "ID de pedido inválido"})
		return
	}

	var pedido models.Pedido
	if err := db.DB.Scopes(middleware.TenantScope(c)).First(&pedido, id).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Pedido não localizado"})
		return
	}

	var input UpdateStatusInput
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Dados inválidos: " + err.Error()})
		return
	}

	// Validação de transição de status simples
	status := models.PedidoStatus(input.Status)
	if status != models.StatusPendente && status != models.StatusPreparando && status != models.StatusPronto && status != models.StatusFinalizado && status != models.StatusCancelado {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Status informado é inválido"})
		return
	}

	pedido.Status = status
	pedido.UpdatedAt = time.Now()

	if err := db.DB.Save(&pedido).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Erro ao atualizar status do pedido"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message": "Status do pedido atualizado com sucesso!",
		"id":      pedido.ID,
		"status":  pedido.Status,
	})
}
