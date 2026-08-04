package models

import (
	"time"

	"cardapio-online/internal/dinheiro"
)

type PedidoStatus string

const (
	StatusPendente   PedidoStatus = "pendente"
	StatusPreparando PedidoStatus = "preparando"
	StatusPronto     PedidoStatus = "pronto"
	StatusFinalizado PedidoStatus = "finalizado"
	StatusCancelado  PedidoStatus = "cancelado"
)

// StatusValidos evita repetir a lista de comparações em cada handler.
var StatusValidos = map[PedidoStatus]bool{
	StatusPendente:   true,
	StatusPreparando: true,
	StatusPronto:     true,
	StatusFinalizado: true,
	StatusCancelado:  true,
}

type Pedido struct {
	ID       uint   `gorm:"primaryKey;autoIncrement" json:"id"`
	TenantID uint   `gorm:"not null;index" json:"tenant_id"`
	Tenant   Tenant `gorm:"foreignKey:TenantID" json:"-"`

	// PublicToken é o identificador usado no URL de rastreio.
	//
	// O rastreio usava o ID sequencial numa rota pública sem escopo de tenant, pelo que
	// iterar de 1 a N extraía nome, telefone e forma de pagamento de todas as encomendas
	// da plataforma. Um token opaco elimina a enumeração.
	PublicToken string `gorm:"type:char(36);uniqueIndex;not null" json:"public_token"`

	// IdempotencyKey impede que um duplo-toque no checkout crie duas encomendas.
	IdempotencyKey *string `gorm:"type:varchar(64)" json:"-"`

	ClienteNome     string       `gorm:"type:varchar(150);not null" json:"cliente_nome"`
	ClienteTelefone string       `gorm:"type:varchar(20);not null" json:"cliente_telefone"`
	Status          PedidoStatus `gorm:"type:varchar(50);default:'pendente';index" json:"status"`

	// Valores em cêntimos. ValorTotalCents é o que o cliente paga; Base e IVA são a
	// decomposição, guardada no momento da encomenda porque as taxas mudam por Orçamento
	// do Estado e uma encomenda antiga tem de continuar a reproduzir o imposto que teve.
	//
	// Invariante garantida pelo cálculo: BaseCents + IVACents == ValorTotalCents.
	ValorTotalCents dinheiro.Cents `gorm:"column:valor_total_cents;not null" json:"valor_total_cents"`
	BaseCents       dinheiro.Cents `gorm:"column:base_cents;not null" json:"base_cents"`
	IVACents        dinheiro.Cents `gorm:"column:iva_cents;not null" json:"iva_cents"`
	TrocoParaCents  dinheiro.Cents `gorm:"column:troco_para_cents;not null" json:"troco_para_cents"`

	// Colunas legadas, mantidas para permitir rollback do binário.
	ValorTotal float64 `gorm:"type:decimal(10,2);not null" json:"-"`
	TrocoPara  float64 `gorm:"type:decimal(10,2);default:0.00" json:"-"`

	FormaPagamento string `gorm:"type:varchar(50);not null" json:"forma_pagamento"`
	// PixPago é legado e deixa de ser escrito: o Pix não existe no mercado português e
	// era marcado como pago sem qualquer verificação.
	PixPago              bool        `gorm:"default:false" json:"-"`
	CartaoUltimosDigitos string      `gorm:"type:varchar(4)" json:"cartao_ultimos_digitos"`
	CreatedAt            time.Time   `gorm:"index" json:"created_at"`
	UpdatedAt            time.Time   `json:"updated_at"`
	Itens                []OrderItem `gorm:"foreignKey:PedidoID" json:"itens"`
	// LinhasIVA é a decomposição por taxa, para o restaurante reconciliar com o software
	// de facturação que já usa.
	LinhasIVA []PedidoIVA `gorm:"foreignKey:PedidoID" json:"linhas_iva"`
}

type OrderItem struct {
	ID       uint `gorm:"primaryKey;autoIncrement" json:"id"`
	PedidoID uint `gorm:"not null;index" json:"pedido_id"`

	// MenuItemID liga a linha ao produto de origem. Serve para calcular os destaques do
	// menu ("os mais pedidos"); é nullable porque as linhas antigas guardavam só o nome.
	MenuItemID *uint `gorm:"index" json:"menu_item_id"`

	Nome string `gorm:"type:varchar(150);not null" json:"nome"`

	// Observacoes é o pedido especial do cliente para esta linha: "sem cebola",
	// "bem passado". Pertence à linha da encomenda e não ao produto, porque é uma escolha
	// daquela compra.
	Observacoes string `gorm:"type:varchar(280)" json:"observacoes"`

	Quantidade int `gorm:"not null" json:"quantidade"`

	// Preço unitário e total da linha em cêntimos, com IVA incluído.
	PrecoUnitarioCents dinheiro.Cents `gorm:"column:preco_unitario_cents;not null" json:"preco_unitario_cents"`
	TotalLinhaCents    dinheiro.Cents `gorm:"column:total_linha_cents;not null" json:"total_linha_cents"`
	// TaxaIVABP é o snapshot da taxa aplicada. Não é lida do produto ao apresentar a
	// encomenda: o produto pode ter mudado de taxa desde então.
	TaxaIVABP dinheiro.TaxaBP `gorm:"column:taxa_iva_bp;not null" json:"taxa_iva_bp"`

	// Coluna legada.
	PrecoUnitario float64 `gorm:"type:decimal(10,2);not null" json:"-"`

	CreatedAt time.Time `json:"created_at"`
}

// PedidoIVA é o resumo de uma taxa de IVA numa encomenda.
type PedidoIVA struct {
	ID         uint            `gorm:"primaryKey;autoIncrement" json:"-"`
	PedidoID   uint            `gorm:"not null;index" json:"-"`
	TaxaIVABP  dinheiro.TaxaBP `gorm:"column:taxa_iva_bp;not null" json:"taxa_iva_bp"`
	BrutoCents dinheiro.Cents  `gorm:"column:bruto_cents;not null" json:"bruto_cents"`
	BaseCents  dinheiro.Cents  `gorm:"column:base_cents;not null" json:"base_cents"`
	IVACents   dinheiro.Cents  `gorm:"column:iva_cents;not null" json:"iva_cents"`
}

func (Pedido) TableName() string    { return "pedidos" }
func (OrderItem) TableName() string { return "itens_pedido" }
func (PedidoIVA) TableName() string { return "pedido_iva" }

// SincronizarLegado mantém as colunas decimal antigas alinhadas com os cêntimos.
func (p *Pedido) SincronizarLegado() {
	p.ValorTotal = p.ValorTotalCents.Euros()
	p.TrocoPara = p.TrocoParaCents.Euros()
}

func (o *OrderItem) SincronizarLegado() {
	o.PrecoUnitario = o.PrecoUnitarioCents.Euros()
}

// TelefoneMascarado devolve o telefone com os dígitos do meio ocultos.
//
// Usado na resposta pública de rastreio: quem tem o link consegue confirmar que é a sua
// encomenda sem que o número completo fique exposto a quem obtenha o URL.
func (p *Pedido) TelefoneMascarado() string {
	t := []rune(p.ClienteTelefone)
	if len(t) <= 4 {
		return "****"
	}
	visiveisFim := 3
	prefixo := 0
	if len(t) > 9 {
		prefixo = len(t) - 9 // mantém o indicativo internacional, ex. +351
	}

	out := make([]rune, 0, len(t))
	out = append(out, t[:prefixo]...)
	for i := prefixo; i < len(t)-visiveisFim; i++ {
		if t[i] == ' ' || t[i] == '+' || t[i] == '-' {
			out = append(out, t[i])
			continue
		}
		out = append(out, '*')
	}
	out = append(out, t[len(t)-visiveisFim:]...)
	return string(out)
}
