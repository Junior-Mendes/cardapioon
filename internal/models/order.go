package models

import "time"

type PedidoStatus string

const (
	StatusPendente   PedidoStatus = "pendente"
	StatusPreparando PedidoStatus = "preparando"
	StatusPronto     PedidoStatus = "pronto" // Pronto para Retirada
	StatusFinalizado PedidoStatus = "finalizado"
	StatusCancelado  PedidoStatus = "cancelado"
)

type Pedido struct {
	ID                  uint         `gorm:"primaryKey;autoIncrement" json:"id"`
	TenantID            uint         `gorm:"not null;index" json:"tenant_id"`
	Tenant              Tenant       `gorm:"foreignKey:TenantID" json:"-"`
	ClienteNome         string       `gorm:"type:varchar(150);not null" json:"cliente_nome"`
	ClienteTelefone     string       `gorm:"type:varchar(20);not null" json:"cliente_telefone"`
	Status              PedidoStatus `gorm:"type:varchar(50);default:'pendente';index" json:"status"`
	ValorTotal          float64      `gorm:"type:decimal(10,2);not null" json:"valor_total"`
	FormaPagamento      string       `gorm:"type:varchar(50);not null" json:"forma_pagamento"` // pix, cartao_credito, retirada_dinheiro, retirada_cartao
	TrocoPara           float64      `gorm:"type:decimal(10,2);default:0.00" json:"troco_para"`
	PixPago             bool         `gorm:"default:false" json:"pix_pago"`
	CartaoUltimosDigitos string       `gorm:"type:varchar(4)" json:"cartao_ultimos_digitos"`
	CreatedAt           time.Time    `gorm:"index" json:"created_at"`
	UpdatedAt           time.Time    `json:"updated_at"`
	Itens               []OrderItem  `gorm:"foreignKey:PedidoID" json:"itens"`
}

type OrderItem struct {
	ID            uint      `gorm:"primaryKey;autoIncrement" json:"id"`
	PedidoID      uint      `gorm:"not null;index" json:"pedido_id"`
	Nome          string    `gorm:"type:varchar(150);not null" json:"nome"`
	Quantidade    int       `gorm:"not null" json:"quantidade"`
	PrecoUnitario float64   `gorm:"type:decimal(10,2);not null" json:"preco_unitario"`
	CreatedAt     time.Time `json:"created_at"`
}

func (Pedido) TableName() string {
	return "pedidos"
}

func (OrderItem) TableName() string {
	return "itens_pedido"
}
