package models

import "time"

// PlataformaAdmin é uma conta de quem opera o SaaS — o dono da plataforma e quem o ajuda
// no suporte. Não pertence a nenhum restaurante.
//
// Deliberadamente sem TenantID: é a ausência desse campo que garante que uma conta destas
// nunca pode ser confundida com um lojista. Ver a migração 0007 para o porquê de não ser
// uma role nova em `usuarios`.
type PlataformaAdmin struct {
	ID    uint   `gorm:"primaryKey;autoIncrement" json:"id"`
	Nome  string `gorm:"type:varchar(150);not null" json:"nome"`
	Email string `gorm:"type:varchar(150);uniqueIndex;not null" json:"email"`

	SenhaHash string `gorm:"type:varchar(255);not null" json:"-"`
	// Sem `default:` na etiqueta: com um default, desactivar a conta (false, que é o zero
	// de bool) seria omitido do INSERT e a base voltaria a pô-la a true.
	Ativo bool `gorm:"not null" json:"ativo"`

	PasswordChangedAt *time.Time `json:"-"`
	LastLoginAt       *time.Time `json:"last_login_at"`
	CreatedAt         time.Time  `json:"created_at"`
	UpdatedAt         time.Time  `json:"updated_at"`
}

func (PlataformaAdmin) TableName() string { return "plataforma_admins" }

// PlataformaRefreshToken é a sessão de um administrador da plataforma.
//
// Separado de RefreshToken porque essa tabela tem chave estrangeira para `usuarios`, e
// porque misturar as duas sessões permitiria que a revogação de uma afectasse a outra.
type PlataformaRefreshToken struct {
	ID         uint      `gorm:"primaryKey;autoIncrement"`
	AdminID    uint      `gorm:"not null;index"`
	TokenHash  string    `gorm:"type:char(64);uniqueIndex;not null"`
	ExpiresAt  time.Time `gorm:"not null;index"`
	RevokedAt  *time.Time
	ReplacedBy *string   `gorm:"type:char(64)"`
	UserAgent  string    `gorm:"type:varchar(255)"`
	IP         string    `gorm:"type:varchar(45)"`
	CreatedAt  time.Time `gorm:"not null"`
}

func (PlataformaRefreshToken) TableName() string { return "plataforma_refresh_tokens" }

func (r *PlataformaRefreshToken) Valido(agora time.Time) bool {
	return r.RevokedAt == nil && agora.Before(r.ExpiresAt)
}
