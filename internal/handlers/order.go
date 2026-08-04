package handlers

import (
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"cardapio-online/internal/dinheiro"
	"cardapio-online/internal/eventos"
	"cardapio-online/internal/middleware"
	"cardapio-online/internal/models"
	"cardapio-online/internal/validate"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

// Métodos de pagamento suportados no MVP.
//
// O MVP é apenas levantamento ao balcão com pagamento na caixa: não há entrega nem
// pagamento na aplicação. Isto elimina toda a superfície de fraude de pagamento — não
// existe estado de "pago" para falsificar, ao contrário do Pix da versão anterior, que era
// marcado como pago sem qualquer verificação.
//
// O valor gravado indica apenas a intenção declarada pelo cliente, para o balcão preparar
// o troco ou o terminal. A confirmação do pagamento acontece fisicamente na caixa.
const (
	// PagamentoBalcaoDinheiro: paga em dinheiro na caixa ao levantar.
	PagamentoBalcaoDinheiro = "dinheiro"
	// PagamentoBalcaoCartao: paga com cartão no terminal da caixa ao levantar.
	PagamentoBalcaoCartao = "cartao"
)

const maxItensPorEncomenda = 100

type orderItemInput struct {
	MenuItemID uint `json:"menu_item_id" binding:"required"`
	Quantidade int  `json:"quantidade" binding:"required,gt=0,lte=99"`
	// Observacoes é o pedido especial do cliente para esta linha: "sem cebola".
	Observacoes string `json:"observacoes" binding:"max=280"`
}

// chaveLinha identifica uma linha da encomenda.
//
// Inclui as observações de propósito: o mesmo prato pedido duas vezes, uma "sem cebola" e
// outra normal, são duas linhas distintas. Agrupar só pelo produto juntaria os dois e a
// cozinha perderia a instrução.
type chaveLinha struct {
	menuItemID  uint
	observacoes string
}

type createOrderInput struct {
	ClienteNome     string `json:"cliente_nome" binding:"required,min=2,max=150"`
	ClienteTelefone string `json:"cliente_telefone" binding:"required"`
	FormaPagamento  string `json:"forma_pagamento" binding:"required"`

	// Troco aceite como texto ou número, convertido para cêntimos sem passar por float
	// nos cálculos.
	TrocoParaTexto string           `json:"troco_para_texto"`
	TrocoPara      float64          `json:"troco_para" binding:"gte=0"`
	Itens          []orderItemInput `json:"itens" binding:"required,min=1"`
}

// trocoCents resolve o valor do troco a partir de qualquer das duas formas de entrada.
func (in *createOrderInput) trocoCents() (dinheiro.Cents, error) {
	if strings.TrimSpace(in.TrocoParaTexto) != "" {
		return dinheiro.Parse(in.TrocoParaTexto)
	}
	return dinheiro.DeEuros(in.TrocoPara), nil
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

	// Agrupa por produto E observações, para que o mesmo item repetido no payload com a
	// mesma instrução não produza duas linhas, mas com instruções diferentes produza.
	quantidades := map[chaveLinha]int{}
	var ordem []chaveLinha
	for _, item := range in.Itens {
		chave := chaveLinha{
			menuItemID:  item.MenuItemID,
			observacoes: limparLinha(item.Observacoes, 280),
		}
		if _, visto := quantidades[chave]; !visto {
			ordem = append(ordem, chave)
		}
		quantidades[chave] += item.Quantidade
	}

	troco, err := in.trocoCents()
	if err != nil {
		erroCliente(c, http.StatusBadRequest, "Valor de troco inválido")
		return
	}

	agora := time.Now()
	pedido := models.Pedido{
		TenantID:        t.ID,
		PublicToken:     uuid.NewString(),
		ClienteNome:     limparLinha(in.ClienteNome, 150),
		ClienteTelefone: telefone,
		Status:          models.StatusPendente,
		FormaPagamento:  in.FormaPagamento,
		TrocoParaCents:  troco,
		CreatedAt:       agora,
		UpdatedAt:       agora,
	}
	if chaveIdem != "" {
		pedido.IdempotencyKey = &chaveIdem
	}

	err = h.DB.Transaction(func(tx *gorm.DB) error {
		var total dinheiro.Cents
		var itens []models.OrderItem
		// Valor bruto agrupado por taxa, para extrair o IVA de cada grupo no fim.
		brutoPorTaxa := map[dinheiro.TaxaBP]dinheiro.Cents{}

		// Os produtos são lidos uma vez por id, mesmo que apareçam em várias linhas com
		// observações diferentes.
		produtos := map[uint]models.MenuItem{}

		for _, chave := range ordem {
			qtd := quantidades[chave]

			mi, jaLido := produtos[chave.menuItemID]
			if !jaLido {
				// O preço vem sempre da base de dados, com o tenant no filtro: o cliente
				// não pode indicar preços nem comprar produtos de outro restaurante.
				if err := tx.Where("id = ? AND tenant_id = ? AND disponivel = ?",
					chave.menuItemID, t.ID, true).First(&mi).Error; err != nil {
					if errors.Is(err, gorm.ErrRecordNotFound) {
						return fmt.Errorf("%w: %d", errItemIndisponivel, chave.menuItemID)
					}
					return err
				}
				produtos[chave.menuItemID] = mi
			}

			preco := mi.PrecoEfetivoCents()
			// Multiplicação inteira: exacta, sem acumular erro como acontecia com float64.
			totalLinha := preco * dinheiro.Cents(qtd)

			total += totalLinha
			brutoPorTaxa[mi.TaxaIVABP] += totalLinha

			menuItemID := mi.ID
			item := models.OrderItem{
				MenuItemID:  &menuItemID,
				Nome:        mi.Nome,
				Observacoes: chave.observacoes,
				Quantidade:  qtd,
				// Snapshot da taxa: o produto pode mudar de taxa depois desta encomenda.
				TaxaIVABP:          mi.TaxaIVABP,
				PrecoUnitarioCents: preco,
				TotalLinhaCents:    totalLinha,
				CreatedAt:          agora,
			}
			item.SincronizarLegado()
			itens = append(itens, item)
		}

		pedido.ValorTotalCents = total

		// O IVA é extraído do total de cada taxa, não linha a linha: extrair por linha e
		// somar acumularia até meio cêntimo por linha, e a decomposição deixaria de fechar
		// com o que o cliente paga.
		linhasIVA := dinheiro.ResumirIVA(brutoPorTaxa)
		var somaBase, somaIVA dinheiro.Cents
		registosIVA := make([]models.PedidoIVA, 0, len(linhasIVA))
		for _, l := range linhasIVA {
			somaBase += l.Base
			somaIVA += l.IVA
			registosIVA = append(registosIVA, models.PedidoIVA{
				TaxaIVABP:  l.Taxa,
				BrutoCents: l.Bruto,
				BaseCents:  l.Base,
				IVACents:   l.IVA,
			})
		}
		pedido.BaseCents = somaBase
		pedido.IVACents = somaIVA

		// Invariante de conformidade: o que se mostra ao cliente é o total, e a
		// decomposição nunca pode perder nem inventar um cêntimo. Se falhar, é um bug de
		// cálculo e a encomenda não deve ser gravada.
		if somaBase+somaIVA != total {
			return fmt.Errorf("%w: base %d + IVA %d != total %d",
				errDecomposicaoIVA, somaBase, somaIVA, total)
		}

		// O troco só é relevante para quem paga em dinheiro; um valor inferior ao total
		// não faz sentido e seria uma surpresa no balcão.
		if pedido.FormaPagamento == PagamentoBalcaoDinheiro &&
			pedido.TrocoParaCents > 0 && pedido.TrocoParaCents < total {
			return errTrocoInsuficiente
		}

		pedido.SincronizarLegado()

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
		for i := range registosIVA {
			registosIVA[i].PedidoID = pedido.ID
		}
		if len(registosIVA) > 0 {
			if err := tx.Create(&registosIVA).Error; err != nil {
				return err
			}
		}
		pedido.Itens = itens
		pedido.LinhasIVA = registosIVA
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

	// Notifica os painéis abertos. Depois da transacção: só se anuncia o que ficou
	// gravado.
	h.Eventos_.Publicar(t.ID, eventos.TipoEncomendaNova, gin.H{
		"numero":            pedido.ID,
		"cliente_nome":      pedido.ClienteNome,
		"valor_total_cents": pedido.ValorTotalCents,
		"valor_total_texto": pedido.ValorTotalCents.String(),
		"itens":             len(pedido.Itens),
		"forma_pagamento":   pedido.FormaPagamento,
	})

	c.JSON(http.StatusCreated, respostaEncomendaCriada(&pedido))
}

var (
	errItemIndisponivel  = errors.New("item indisponível")
	errTrocoInsuficiente = errors.New("troco insuficiente")
	errDecomposicaoIVA   = errors.New("decomposição de IVA não fecha com o total")
)

// validarMetodoPagamento confirma que o método está activo no restaurante.
//
// Ambos os métodos são pagos na caixa, ao levantar. Não há pagamento na aplicação no MVP.
func (h *Handler) validarMetodoPagamento(t *models.Tenant, metodo string) error {
	switch metodo {
	case PagamentoBalcaoDinheiro:
		if !t.DinheiroAtivo {
			return errors.New("este restaurante não aceita pagamento em dinheiro")
		}
	case PagamentoBalcaoCartao:
		if !t.CartaoDebitoAtivo {
			return errors.New("este restaurante não aceita pagamento com cartão")
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
			"status":       p.Status,
			"tracking_url": "/pedido?t=" + p.PublicToken,

			// Valores em cêntimos, mais uma versão formatada para apresentação directa.
			// A decomposição vai junta: é o "valor exato" que o cliente e o balcão veem.
			"valor_total_cents": p.ValorTotalCents,
			"valor_total_texto": p.ValorTotalCents.String(),
			"base_cents":        p.BaseCents,
			"iva_cents":         p.IVACents,
			"iva_texto":         p.IVACents.String(),
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
	if err := h.DB.Preload("Itens").Preload("LinhasIVA").
		Where("public_token = ?", token).First(&p).Error; err != nil {
		erroCliente(c, http.StatusNotFound, "Encomenda não encontrada")
		return
	}

	var t models.Tenant
	if err := h.DB.First(&t, p.TenantID).Error; err != nil {
		h.erroInterno(c, "carregar restaurante da encomenda", err)
		return
	}

	corTexto := ""
	if t.CorPrimaria != "" {
		corTexto = validate.TextoSobreCor(t.CorPrimaria)
	}

	c.JSON(http.StatusOK, gin.H{
		"numero":           p.ID,
		"public_token":     p.PublicToken,
		"restaurante_nome": t.Nome,
		"restaurante_slug": t.Slug,

		// Identidade visual: a página de acompanhamento é do restaurante, não da plataforma.
		"restaurante_logo_url":     t.LogoURL,
		"restaurante_cor_primaria": t.CorPrimaria,
		"restaurante_cor_texto":    corTexto,
		"restaurante_iniciais":     iniciaisDe(t.Nome),
		"mostrar_marca_plataforma": t.MostrarMarcaPlataforma,
		"cliente_nome":             p.ClienteNome,
		// Telefone mascarado: quem tem o link confirma que é a sua encomenda sem que o
		// número completo fique exposto.
		"cliente_telefone": p.TelefoneMascarado(),
		"status":           p.Status,
		"forma_pagamento":  p.FormaPagamento,
		"created_at":       p.CreatedAt,

		"valor_total_cents": p.ValorTotalCents,
		"valor_total_texto": p.ValorTotalCents.String(),
		"base_cents":        p.BaseCents,
		"iva_cents":         p.IVACents,
		"troco_para_cents":  p.TrocoParaCents,
		"iva_incluido":      true,
		"nota_iva":          "Valores com IVA incluído à taxa legal em vigor",

		"itens":      itensPublicos(p.Itens),
		"linhas_iva": linhasIVAPublicas(p.LinhasIVA),
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
		Preload("LinhasIVA").
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

	// O estado anterior é guardado ANTES da escrita.
	//
	// O GORM, com Updates(map), também altera o campo da struct em memória. Ler p.Status
	// depois da escrita devolveria o valor novo, e tanto o registo de auditoria como o
	// evento diriam "preparando -> preparando".
	anterior := p.Status

	if err := h.DB.Model(&p).Updates(map[string]any{
		"status":     novo,
		"updated_at": time.Now(),
	}).Error; err != nil {
		h.erroInterno(c, "actualizar estado da encomenda", err)
		return
	}

	h.auditar(c, "encomenda_estado_alterado", "pedido", fmt.Sprint(p.ID),
		fmt.Sprintf("%s -> %s", anterior, novo))

	// Mantém sincronizados os outros painéis do mesmo restaurante: com dois postos ao
	// balcão, um aceitar a encomenda deve reflectir-se no outro sem esperar.
	h.Eventos_.Publicar(p.TenantID, eventos.TipoEncomendaEstado, gin.H{
		"numero":   p.ID,
		"status":   novo,
		"anterior": anterior,
	})

	c.JSON(http.StatusOK, gin.H{
		"message": "Estado actualizado",
		"numero":  p.ID,
		"status":  novo,
	})
}

// itensPublicos formata as linhas da encomenda com os valores em cêntimos e a taxa que
// lhes foi aplicada no momento da compra.
func itensPublicos(itens []models.OrderItem) []gin.H {
	out := make([]gin.H, 0, len(itens))
	for i := range itens {
		it := &itens[i]
		out = append(out, gin.H{
			"nome":                 it.Nome,
			"observacoes":          it.Observacoes,
			"quantidade":           it.Quantidade,
			"preco_unitario_cents": it.PrecoUnitarioCents,
			"preco_unitario_texto": it.PrecoUnitarioCents.String(),
			"total_linha_cents":    it.TotalLinhaCents,
			"total_linha_texto":    it.TotalLinhaCents.String(),
			"taxa_iva_bp":          it.TaxaIVABP,
			"taxa_iva_texto":       it.TaxaIVABP.Percentagem(),
		})
	}
	return out
}

// linhasIVAPublicas devolve a decomposição por taxa, ordenada como num talão.
//
// A ordenação é explícita e não depende da ordem de inserção nem da ordem que o MySQL
// devolve: sem isto, a mesma encomenda podia mostrar as taxas em ordens diferentes.
func linhasIVAPublicas(linhas []models.PedidoIVA) []gin.H {
	ordenadas := make([]models.PedidoIVA, len(linhas))
	copy(ordenadas, linhas)
	sort.Slice(ordenadas, func(i, j int) bool {
		return ordenadas[i].TaxaIVABP > ordenadas[j].TaxaIVABP
	})
	linhas = ordenadas

	out := make([]gin.H, 0, len(linhas))
	for i := range linhas {
		l := &linhas[i]
		out = append(out, gin.H{
			"taxa_iva_bp":    l.TaxaIVABP,
			"taxa_iva_texto": l.TaxaIVABP.Percentagem(),
			"bruto_cents":    l.BrutoCents,
			"bruto_texto":    l.BrutoCents.String(),
			"base_cents":     l.BaseCents,
			"base_texto":     l.BaseCents.String(),
			"iva_cents":      l.IVACents,
			"iva_texto":      l.IVACents.String(),
		})
	}
	return out
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
