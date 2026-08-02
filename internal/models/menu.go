package models

import (
	"time"

	"cardapio-online/internal/dinheiro"
)

type MenuItem struct {
	ID        uint   `gorm:"primaryKey;autoIncrement" json:"id"`
	TenantID  uint   `gorm:"not null;index" json:"tenant_id"`
	Tenant    Tenant `gorm:"foreignKey:TenantID" json:"-"`
	Nome      string `gorm:"type:varchar(150);not null" json:"nome"`
	Descricao string `gorm:"type:text" json:"descricao"`

	// Preços em cêntimos. O preço é sempre o valor final ao consumidor, com IVA incluído,
	// como a lei portuguesa exige que seja afixado.
	//
	// As colunas float64 antigas (Preco, PrecoDesconto) continuam a ser escritas para
	// permitir um rollback do binário; deixam de ser lidas nos cálculos.
	PrecoCents         dinheiro.Cents `gorm:"column:preco_cents;not null" json:"preco_cents"`
	PrecoDescontoCents dinheiro.Cents `gorm:"column:preco_desconto_cents;not null" json:"preco_desconto_cents"`

	// TaxaIVABP é a taxa de IVA deste produto, em pontos base (2300 = 23%).
	//
	// É uma escolha do estabelecimento, produto a produto: um prato e uma cerveja não têm
	// a mesma taxa. O software não a decide.
	//
	// SEM `default:` na etiqueta, de propósito. Com um default, o GORM omite o campo do
	// INSERT quando o valor é o zero da linguagem e deixa a base aplicar o seu default —
	// o que fazia com que "Isento" (0) fosse gravado como 13%. Um erro de taxa silencioso.
	TaxaIVABP dinheiro.TaxaBP `gorm:"column:taxa_iva_bp;not null" json:"taxa_iva_bp"`

	// Colunas legadas, mantidas para compatibilidade de rollback.
	Preco         float64 `gorm:"type:decimal(10,2);not null" json:"-"`
	PrecoDesconto float64 `gorm:"type:decimal(10,2);default:0.00" json:"-"`

	DescontoAtivo bool      `gorm:"default:false" json:"desconto_ativo"`
	Categoria     string    `gorm:"type:varchar(50);not null;index" json:"categoria"`
	ImagemURL     string    `gorm:"type:text" json:"imagem_url"`
	Disponivel    bool      `gorm:"default:true" json:"disponivel"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

func (MenuItem) TableName() string { return "menu_items" }

// PrecoEfetivoCents devolve o preço a cobrar, considerando o desconto quando activo.
//
// Centraliza a regra num sítio: estava duplicada entre o cálculo da encomenda e a
// apresentação, e as duas cópias podiam divergir.
func (m *MenuItem) PrecoEfetivoCents() dinheiro.Cents {
	if m.DescontoAtivo && m.PrecoDescontoCents > 0 && m.PrecoDescontoCents < m.PrecoCents {
		return m.PrecoDescontoCents
	}
	return m.PrecoCents
}

// SincronizarLegado mantém as colunas decimal antigas alinhadas com os cêntimos.
func (m *MenuItem) SincronizarLegado() {
	m.Preco = m.PrecoCents.Euros()
	m.PrecoDesconto = m.PrecoDescontoCents.Euros()
}
