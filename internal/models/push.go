package models

import "time"

// PushSubscription representa os dados de registo de subscrição de um navegador
// para envio de Web Push notifications.
type PushSubscription struct {
	ID        uint      `gorm:"primaryKey;autoIncrement" json:"id"`
	TenantID  uint      `gorm:"not null;index" json:"tenant_id"`
	UsuarioID uint      `gorm:"not null;index" json:"usuario_id"`
	Endpoint  string    `gorm:"type:varchar(512);uniqueIndex;not null" json:"endpoint"`
	P256dh    string    `gorm:"type:varchar(256);not null" json:"p256dh"`
	Auth      string    `gorm:"type:varchar(256);not null" json:"auth"`
	CreatedAt time.Time `json:"created_at"`
}

func (PushSubscription) TableName() string { return "push_subscriptions" }
