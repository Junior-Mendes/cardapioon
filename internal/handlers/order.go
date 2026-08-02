package handlers

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"cardapio-online/internal/middleware"
	"cardapio-online/internal/models"
	"cardapio-online/internal/validate"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

// Métodos de pagamento suportados. O Pix foi removido: não existe em Portugal e era
// marcado como pago sem qualquer verificação, o que permitia fechar encomendas sem pagar.
const (
	PagamentoDinheiroEntrega = "dinheiro"
	PagamentoTPAEntrega      = "tpa"
	PagamentoCartaoOnline    = "cartao"
)

const maxItensPorEncomenda = 100

type orderItemInput struct {
	MenuItemID uint `json:"menu_item_id" binding:"required"`
	Quantidade int  `json:"quantidade" binding:"required,gt=0,lte=99"`
}

type createOrderInput struct {
	ClienteNome     string           `json:"cliente_nome" binding:"required,min=2,max=150"`
	ClienteTelefone string           `json:"cliente_telefone" binding:"required"`
	FormaPagamento  string           `json:"forma_pagamento" binding:"required"`
	TrocoPara       float64          `json:"troco_para" binding:"gte=0"`
	Itens           []orderItemInput `json:"itens" binding:"required,min=1"`
}

// CreateOrder cria uma encomenda.
func (h *Handler) CreateOrder(c *gin.Context) {
	t, err := h.tenantDoContexto(c)
	if err != nil {
		erroCliente(c, http.StatusNotFound, "Restaurante não encontrado")
		return
	}

	var in createOrderInput
	if err := c.ShouldBindJSON(&in); err != nil {
		erroCliente(c, http.StatusBadRequest, "Dados da encomenda inválidos")
		return
	}
	if len(in.Itens) > maxItensPorEncomenda {
		erroCliente(c, http.StatusBadRequest, "Demasiados itens na encomenda")
		return
	}

	telefone, err := validate.TelefonePortugues(in.ClienteTelefone)
	if err != nil {
		erroCliente(c, http.StatusBadRequest, err.Error())
		return
	}

	if err := h.validarMetodoPagamento(t, in.FormaPagamento); err != nil {
		erroCliente(c, http.StatusBadRequest, err.Error())
		return
	}

	// Idempotência: um duplo-toque no botão de finalizar criava duas encomendas.
	chaveIdem := strings.TrimSpace(c.GetHeader("Idempotency-Key"))
	if chaveIdem != "" {
		if len(chaveIdem) > 64 {
			erroCliente(c, http.StatusBadRequest, "Idempotency-Key demasiado longa")
			return
		}
		var existente models.Pedido
		err := h.DB.Where("tenant_id = ? AND idempotency_key = ?", t.ID, chaveIdem).
			Preload("Itens").First(&existente).Error
		if err == nil {
			c.JSON(http.StatusOK, respostaEncomendaCriada(&existente))
			return
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			h.erroInterno(c, "verificar idempotência da encomenda", err)
			return
		}
	}

	// Agrupa quantidades por produto para que o mesmo item repetido no payload não
	// produza várias linhas na encomenda.
	quantidades := map[uint]int{}
	var ordem []uint
	for _, item := range in.Itens {
		if _, visto := quantidades[item.MenuItemID]; !visto {
			ordem = append(ordem, item.MenuItemID)
		}
		quantidades[item.MenuItemID] += item.Quantidade
	}

	agora := time.Now()
	pedido := models.Pedido{
		TenantID:        t.ID,
		PublicToken:     uuid.NewString(),
		ClienteNome:     limparLinha(in.ClienteNome, 150),
		ClienteTelefone: telefone,
		Status:          models.StatusPendente,
		FormaPagamento:  in.FormaPagamento,
		TrocoPara:       arredondarCentimos(in.TrocoPara),
		CreatedAt:       agora,
		UpdatedAt:       agora,
	}
	if chaveIdem != "" {
		pedido.IdempotencyKey = &chaveIdem
	}

	err = h.DB.Transaction(func(tx *gorm.DB) error {
		var total float64
		var itens []models.OrderItem

		for _, menuItemID := range ordem {
			qtd := quantidades[menuItemID]

			// O preço vem sempre da base de dados, com o tenant no filtro: o cliente não
			// pode indicar preços nem comprar produtos de outro restaurante.
			var mi models.MenuItem
			if err := tx.Where("id = ? AND tenant_id = ? AND disponivel = ?",
				menuItemID, t.ID, true).First(&mi).Error; err != nil {
				if errors.Is(err, gorm.ErrRecordNotFound) {
					return fmt.Errorf("%w: %d", errItemIndisponivel, menuItemID)
				}
				return err
			}

			preco := mi.Preco
			if mi.DescontoAtivo && mi.PrecoDesconto > 0 && mi.PrecoDesconto < mi.Preco {
				preco = mi.PrecoDesconto
			}

			total += preco * float64(qtd)
			itens = append(itens, models.OrderItem{
				Nome:          mi.Nome,
				Quantidade:    qtd,
				PrecoUnitario: preco,
				CreatedAt:     agora,
			})
		}

		pedido.ValorTotal = arredondarCentimos(total)

		if pedido.FormaPagamento == PagamentoDinheiroEntrega &&
			pedido.TrocoPara > 0 && pedido.TrocoPara < pedido.ValorTotal {
			return errTrocoInsuficiente
		}

		if err := tx.Create(&pedido).Error; err != nil {
			return err
		}
		for i := range itens {
			itens[i].PedidoID = pedido.ID
		}
		// Insert em lote: a versão anterior fazia uma query por item.
		if err := tx.Create(&itens).Error; err != nil {
			return err
		}
		pedido.Itens = itens
		return nil
	})

	switch {
	case errors.Is(err, errItemIndisponivel):
		erroCliente(c, http.StatusBadRequest, "Um dos produtos já não está disponível. Actualize o cardápio.")
		return
	case errors.Is(err, errTrocoInsuficiente):
		erroCliente(c, http.StatusBadRequest, "O valor indicado para troco é inferior ao total da encomenda")
		return
	case err != nil:
		// Uma colisão na chave de idempotência significa que outro pedido concorrente
		// ganhou a corrida; devolvemos a encomenda que ele criou.
		if chaveIdem != "" && strings.Contains(strings.ToLower(err.Error()), "duplicate") {
			var existente models.Pedido
			if e := h.DB.Where("tenant_id = ? AND idempotency_key = ?", t.ID, chaveIdem).
				Preload("Itens").First(&existente).Error; e == nil {
				c.JSON(http.StatusOK, respostaEncomendaCriada(&existente))
				return
			}
		}
		h.erroInterno(c, "criar encomenda", err)
		return
	}

	c.JSON(http.StatusCreated, respostaEncomendaCriada(&pedido))
}

var (
	errItemIndisponivel  = errors.New("item indisponível")
	errTrocoInsuficiente = errors.New("troco insuficiente")
)

// validarMetodoPagamento confirma que o método está activo no restaurante.
func (h *Handler) validarMetodoPagamento(t *models.Tenant, metodo string) error {
	switch metodo {
	case PagamentoDinheiroEntrega:
		if !t.DinheiroAtivo {
			return errors.New("pagamento em dinheiro não disponível neste restaurante")
		}
	case PagamentoTPAEntrega:
		if !t.CartaoDebitoAtivo {
			return errors.New("pagamento por multibanco na entrega não disponível neste restaurante")
		}
	case PagamentoCartaoOnline:
		if !t.CartaoCreditoAtivo {
			return errors.New("pagamento online por cartão não disponível neste restaurante")
		}
	default:
		return errors.New("método de pagamento inválido")
	}
	return nil
}

// respostaEncomendaCriada devolve o payload de confirmação, incluindo o token público de
// rastreio (o ID sequencial não é utilizável para consultar a encomenda).
func respostaEncomendaCriada(p *models.Pedido) gin.H {
	return gin.H{
		"message": "Encomenda registada",
		"encomenda": gin.H{
			"numero":       p.ID,
			"public_token": p.PublicToken,
			"valor_total":  p.ValorTotal,
			"status":       p.Status,
			"tracking_url": "/pedido?t=" + p.PublicToken,
		},
	}
}

// GetOrderPublico devolve o estado de uma encomenda a partir do token opaco.
//
// A rota anterior aceitava o ID sequencial sem qualquer escopo, pelo que iterar de 1 a N
// extraía nome, telefone e forma de pagamento de todas as encomendas da plataforma.
func (h *Handler) GetOrderPublico(c *gin.Context) {
	token := strings.TrimSpace(c.Param("token"))
	if token == "" {
		token = strings.TrimSpace(c.Query("t"))
	}
	// Um token válido é um UUID; validar o formato evita percorrer a tabela com input
	// arbitrário.
	if _, err := uuid.Parse(token); err != nil {
		erroCliente(c, http.StatusNotFound, "Encomenda não encontrada")
		return
	}

	var p models.Pedido
	if err := h.DB.Preload("Itens").Where("public_token = ?", token).First(&p).Error; err != nil {
		erroCliente(c, http.StatusNotFound, "Encomenda não encontrada")
		return
	}

	var t models.Tenant
	if err := h.DB.Select("nome", "slug").First(&t, p.TenantID).Error; err != nil {
		h.erroInterno(c, "carregar restaurante da encomenda", err)
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"numero":           p.ID,
		"public_token":     p.PublicToken,
		"restaurante_nome": t.Nome,
		"restaurante_slug": t.Slug,
		"cliente_nome":     p.ClienteNome,
		// Telefone mascarado: quem tem o link confirma que é a sua encomenda sem que o
		// número completo fique exposto.
		"cliente_telefone": p.TelefoneMascarado(),
		"status":           p.Status,
		"valor_total":      p.ValorTotal,
		"forma_pagamento":  p.FormaPagamento,
		"troco_para":       p.TrocoPara,
		"created_at":       p.CreatedAt,
		"itens":            p.Itens,
	})
}

// GetAdminOrders lista as encomendas do restaurante, com paginação e filtros.
//
// A versão anterior devolvia todas as encomendas com os respectivos itens, e o painel
// pedia essa lista a cada dez segundos.
func (h *Handler) GetAdminOrders(c *gin.Context) {
	pagina := maxInt(1, atoiOmissao(c.Query("pagina"), 1))
	porPagina := clamp(atoiOmissao(c.Query("por_pagina"), 30), 1, 100)

	consulta := h.DB.Model(&models.Pedido{}).Scopes(middleware.TenantScope(c))

	if s := c.Query("status"); s != "" {
		if !models.StatusValidos[models.PedidoStatus(s)] {
			erroCliente(c, http.StatusBadRequest, "Estado inválido")
			return
		}
		consulta = consulta.Where("status = ?", s)
	}
	if desde := c.Query("desde"); desde != "" {
		d, err := time.Parse("2006-01-02", desde)
		if err != nil {
			erroCliente(c, http.StatusBadRequest, "Data inicial inválida (use AAAA-MM-DD)")
			return
		}
		consulta = consulta.Where("created_at >= ?", d)
	}

	var total int64
	if err := consulta.Count(&total).Error; err != nil {
		h.erroInterno(c, "contar encomendas", err)
		return
	}

	var pedidos []models.Pedido
	if err := consulta.
		Preload("Itens").
		Order("id desc").
		Limit(porPagina).
		Offset((pagina - 1) * porPagina).
		Find(&pedidos).Error; err != nil {
		h.erroInterno(c, "listar encomendas", err)
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"encomendas": pedidos,
		"paginacao": gin.H{
			"pagina":     pagina,
			"por_pagina": porPagina,
			"total":      total,
		},
	})
}

type updateStatusInput struct {
	Status string `json:"status" binding:"required"`
}

// transicoesPermitidas define a máquina de estados das encomendas.
//
// A versão anterior aceitava qualquer estado a partir de qualquer outro, permitindo
// reabrir uma encomenda já finalizada ou cancelada.
var transicoesPermitidas = map[models.PedidoStatus][]models.PedidoStatus{
	models.StatusPendente:   {models.StatusPreparando, models.StatusCancelado},
	models.StatusPreparando: {models.StatusPronto, models.StatusCancelado},
	models.StatusPronto:     {models.StatusFinalizado, models.StatusCancelado},
	models.StatusFinalizado: {},
	models.StatusCancelado:  {},
}

func transicaoValida(de, para models.PedidoStatus) bool {
	for _, p := range transicoesPermitidas[de] {
		if p == para {
			return true
		}
	}
	return false
}

// UpdateOrderStatus altera o estado de uma encomenda.
func (h *Handler) UpdateOrderStatus(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		erroCliente(c, http.StatusBadRequest, "Identificador inválido")
		return
	}

	var in updateStatusInput
	if err := c.ShouldBindJSON(&in); err != nil {
		erroCliente(c, http.StatusBadRequest, "Dados inválidos")
		return
	}
	novo := models.PedidoStatus(in.Status)
	if !models.StatusValidos[novo] {
		erroCliente(c, http.StatusBadRequest, "Estado inválido")
		return
	}

	var p models.Pedido
	if err := h.DB.Scopes(middleware.TenantScope(c)).Where("id = ?", id).First(&p).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			erroCliente(c, http.StatusNotFound, "Encomenda não encontrada")
			return
		}
		h.erroInterno(c, "procurar encomenda", err)
		return
	}

	if p.Status == novo {
		c.JSON(http.StatusOK, gin.H{"message": "A encomenda já está neste estado", "status": novo})
		return
	}
	if !transicaoValida(p.Status, novo) {
		erroCliente(c, http.StatusConflict, fmt.Sprintf(
			"Não é possível mudar de '%s' para '%s'", p.Status, novo))
		return
	}

	if err := h.DB.Model(&p).Updates(map[string]any{
		"status":     novo,
		"updated_at": time.Now(),
	}).Error; err != nil {
		h.erroInterno(c, "actualizar estado da encomenda", err)
		return
	}

	h.auditar(c, "encomenda_estado_alterado", "pedido", fmt.Sprint(p.ID),
		fmt.Sprintf("%s -> %s", p.Status, novo))

	c.JSON(http.StatusOK, gin.H{
		"message": "Estado actualizado",
		"numero":  p.ID,
		"status":  novo,
	})
}

func atoiOmissao(s string, omissao int) int {
	if n, err := strconv.Atoi(s); err == nil {
		return n
	}
	return omissao
}

func clamp(v, minimo, maximo int) int {
	if v < minimo {
		return minimo
	}
	if v > maximo {
		return maximo
	}
	return v
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
