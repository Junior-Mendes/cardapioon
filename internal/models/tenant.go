package models

import "time"

type Tenant struct {
	ID                 uint      `gorm:"primaryKey;autoIncrement" json:"id"`
	Nome               string    `gorm:"type:varchar(150);not null" json:"nome"`
	Slug               string    `gorm:"type:varchar(50);uniqueIndex;not null" json:"slug"`
	Domain             *string   `gorm:"type:varchar(255);uniqueIndex;default:null" json:"domain"`
	Ativo              bool      `gorm:"default:true" json:"ativo"`
	SenhaHash          string    `gorm:"type:varchar(255);not null" json:"-"`
	PixAtivo           bool      `gorm:"default:false" json:"pix_ativo"`
	PixChave           string    `gorm:"type:varchar(100)" json:"pix_chave"`
	CartaoCreditoAtivo bool      `gorm:"default:false" json:"cartao_credito_ativo"`
	CartaoDebitoAtivo  bool      `gorm:"default:false" json:"cartao_debito_ativo"`
	DinheiroAtivo      bool      `gorm:"default:false" json:"dinheiro_ativo"`
	CreatedAt          time.Time `json:"created_at"`
	UpdatedAt          time.Time `json:"updated_at"`
}

func (Tenant) TableName() string {
	return "tenants"
}
