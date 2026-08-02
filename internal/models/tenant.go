package models

import "time"

// Estados de verificação de um domínio personalizado.
//
// A rota no Traefik só é criada quando o estado é DomainVerificado: gerá-la antes disso
// permitia a um tenant reclamar o domínio de terceiros, fazendo o edge router encaminhar
// esse tráfego e pedir um certificado Let's Encrypt em nome dele.
const (
	DomainNenhum     = "none"
	DomainPendente   = "pending"
	DomainVerificado = "verified"
)

type Tenant struct {
	ID   uint   `gorm:"primaryKey;autoIncrement" json:"id"`
	Nome string `gorm:"type:varchar(150);not null" json:"nome"`
	// NIF do estabelecimento. Necessário para a facturação da subscrição e para os
	// documentos que o restaurante emite.
	NIF  string `gorm:"type:varchar(20)" json:"nif"`
	Slug string `gorm:"type:varchar(50);uniqueIndex;not null" json:"slug"`

	Domain            *string    `gorm:"type:varchar(255);uniqueIndex;default:null" json:"domain"`
	DomainStatus      string     `gorm:"type:varchar(20);not null;default:'none'" json:"domain_status"`
	DomainVerifyToken string     `gorm:"type:varchar(64)" json:"-"`
	DomainVerifiedAt  *time.Time `json:"domain_verified_at"`

	Ativo bool `gorm:"default:true" json:"ativo"`
	// SenhaHash é legado: a autenticação passou a ser feita pela tabela usuarios.
	// Mantido apenas para não perder dados de contas antigas ainda não migradas.
	SenhaHash string `gorm:"type:varchar(255);not null" json:"-"`

	// Métodos de pagamento. Pix foi removido do produto (não existe em Portugal) mas as
	// colunas permanecem na base até a migração de dados da Fase 1 as eliminar.
	CartaoCreditoAtivo bool `gorm:"default:false" json:"cartao_credito_ativo"`
	CartaoDebitoAtivo  bool `gorm:"default:false" json:"cartao_debito_ativo"`
	DinheiroAtivo      bool `gorm:"default:false" json:"dinheiro_ativo"`

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

func (Tenant) TableName() string { return "tenants" }

// DomainAtivo indica se o domínio personalizado pode ser encaminhado.
func (t *Tenant) DomainAtivo() bool {
	return t.Domain != nil && *t.Domain != "" && t.DomainStatus == DomainVerificado
}

// DomainValue devolve o domínio ou string vazia, evitando desreferenciar nil nos chamadores.
func (t *Tenant) DomainValue() string {
	if t.Domain == nil {
		return ""
	}
	return *t.Domain
}
