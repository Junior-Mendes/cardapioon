package models

import "time"

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
	ValorTotal      float64      `gorm:"type:decimal(10,2);not null" json:"valor_total"`
	FormaPagamento  string       `gorm:"type:varchar(50);not null" json:"forma_pagamento"`
	TrocoPara       float64      `gorm:"type:decimal(10,2);default:0.00" json:"troco_para"`
	// PixPago é legado e deixa de ser escrito: o Pix não existe no mercado português e
	// era marcado como pago sem qualquer verificação. Removido em migração da Fase 1.
	PixPago              bool        `gorm:"default:false" json:"-"`
	CartaoUltimosDigitos string      `gorm:"type:varchar(4)" json:"cartao_ultimos_digitos"`
	CreatedAt            time.Time   `gorm:"index" json:"created_at"`
	UpdatedAt            time.Time   `json:"updated_at"`
	Itens                []OrderItem `gorm:"foreignKey:PedidoID" json:"itens"`
}

type OrderItem struct {
	ID            uint      `gorm:"primaryKey;autoIncrement" json:"id"`
	PedidoID      uint      `gorm:"not null;index" json:"pedido_id"`
	Nome          string    `gorm:"type:varchar(150);not null" json:"nome"`
	Quantidade    int       `gorm:"not null" json:"quantidade"`
	PrecoUnitario float64   `gorm:"type:decimal(10,2);not null" json:"preco_unitario"`
	CreatedAt     time.Time `json:"created_at"`
}

func (Pedido) TableName() string    { return "pedidos" }
func (OrderItem) TableName() string { return "itens_pedido" }

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
