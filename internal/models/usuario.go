package models

import "time"

// Papéis de utilizador dentro de um tenant, do mais para o menos privilegiado.
// A hierarquia é aplicada por middleware.RequireRole.
const (
	RoleOwner       = "owner"
	RoleAdmin       = "admin"
	RoleGerente     = "gerente"
	RoleFuncionario = "funcionario"
)

// RolesValidos é usado na validação da criação de utilizadores.
var RolesValidos = map[string]bool{
	RoleOwner:       true,
	RoleAdmin:       true,
	RoleGerente:     true,
	RoleFuncionario: true,
}

type Usuario struct {
	ID       uint   `gorm:"primaryKey;autoIncrement" json:"id"`
	TenantID uint   `gorm:"not null;index" json:"tenant_id"`
	Tenant   Tenant `gorm:"foreignKey:TenantID" json:"-"`
	Nome     string `gorm:"type:varchar(150);not null" json:"nome"`
	Email    string `gorm:"type:varchar(150);uniqueIndex;not null" json:"email"`

	EmailVerifiedAt *time.Time `json:"email_verified_at"`

	SenhaHash         string     `gorm:"type:varchar(255);not null" json:"-"`
	PasswordChangedAt *time.Time `json:"-"`
	LastLoginAt       *time.Time `json:"last_login_at"`

	Role      string    `gorm:"type:varchar(50);default:'admin'" json:"role"`
	Ativo     bool      `gorm:"default:true" json:"ativo"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

func (Usuario) TableName() string { return "usuarios" }

// PasswordReset guarda apenas o hash do token de reset: uma fuga da base não permite
// tomar contas.
type PasswordReset struct {
	ID        uint      `gorm:"primaryKey;autoIncrement"`
	UsuarioID uint      `gorm:"not null;index"`
	TokenHash string    `gorm:"type:char(64);uniqueIndex;not null"`
	ExpiresAt time.Time `gorm:"not null"`
	UsedAt    *time.Time
	CreatedIP string    `gorm:"type:varchar(45)"`
	CreatedAt time.Time `gorm:"not null"`
}

func (PasswordReset) TableName() string { return "password_resets" }

// Valido indica se o token ainda pode ser usado.
func (p *PasswordReset) Valido(agora time.Time) bool {
	return p.UsedAt == nil && agora.Before(p.ExpiresAt)
}

// RefreshToken permite renovar o access token sem repetir o login.
//
// A rotação é obrigatória: cada uso emite um novo token e marca o anterior como
// substituído, de modo a que a reutilização de um token já gasto seja detectável e
// indique roubo de sessão.
type RefreshToken struct {
	ID         uint      `gorm:"primaryKey;autoIncrement"`
	UsuarioID  uint      `gorm:"not null;index"`
	TenantID   uint      `gorm:"not null"`
	TokenHash  string    `gorm:"type:char(64);uniqueIndex;not null"`
	ExpiresAt  time.Time `gorm:"not null;index"`
	RevokedAt  *time.Time
	ReplacedBy *string   `gorm:"type:char(64)"`
	UserAgent  string    `gorm:"type:varchar(255)"`
	IP         string    `gorm:"type:varchar(45)"`
	CreatedAt  time.Time `gorm:"not null"`
}

func (RefreshToken) TableName() string { return "refresh_tokens" }

func (r *RefreshToken) Valido(agora time.Time) bool {
	return r.RevokedAt == nil && agora.Before(r.ExpiresAt)
}

// AuditLog registra acções administrativas sensíveis.
type AuditLog struct {
	ID        uint  `gorm:"primaryKey;autoIncrement"`
	TenantID  *uint `gorm:"index"`
	UsuarioID *uint
	Acao      string    `gorm:"type:varchar(80);not null;index"`
	Recurso   string    `gorm:"type:varchar(80)"`
	RecursoID string    `gorm:"type:varchar(64)"`
	Detalhe   string    `gorm:"type:text"`
	IP        string    `gorm:"type:varchar(45)"`
	CreatedAt time.Time `gorm:"not null"`
}

func (AuditLog) TableName() string { return "audit_logs" }
