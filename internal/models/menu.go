package models

import "time"

type MenuItem struct {
	ID            uint      `gorm:"primaryKey;autoIncrement" json:"id"`
	TenantID      uint      `gorm:"not null;index" json:"tenant_id"`
	Tenant        Tenant    `gorm:"foreignKey:TenantID" json:"-"`
	Nome          string    `gorm:"type:varchar(150);not null" json:"nome"`
	Descricao     string    `gorm:"type:text" json:"descricao"`
	Preco         float64   `gorm:"type:decimal(10,2);not null" json:"preco"`
	PrecoDesconto float64   `gorm:"type:decimal(10,2);default:0.00" json:"preco_desconto"`
	DescontoAtivo bool      `gorm:"default:false" json:"desconto_ativo"`
	Categoria     string    `gorm:"type:varchar(50);not null;index" json:"categoria"`
	ImagemURL     string    `gorm:"type:text" json:"imagem_url"`
	Disponivel    bool      `gorm:"default:true" json:"disponivel"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

func (MenuItem) TableName() string {
	return "menu_items"
}
