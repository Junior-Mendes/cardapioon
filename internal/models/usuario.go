package models

import "time"

type Usuario struct {
	ID        uint      `gorm:"primaryKey;autoIncrement" json:"id"`
	TenantID  uint      `gorm:"not null;index" json:"tenant_id"`
	Tenant    Tenant    `gorm:"foreignKey:TenantID" json:"-"`
	Nome      string    `gorm:"type:varchar(150);not null" json:"nome"`
	Email     string    `gorm:"type:varchar(150);uniqueIndex;not null" json:"email"`
	SenhaHash string    `gorm:"type:varchar(255);not null" json:"-"`
	Role      string    `gorm:"type:varchar(50);default:'admin'" json:"role"` // 'owner', 'admin', 'gerente', 'funcionario'
	Ativo     bool      `gorm:"default:true" json:"ativo"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

func (Usuario) TableName() string {
	return "usuarios"
}
