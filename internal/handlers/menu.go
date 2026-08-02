package handlers

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"cardapio-online/internal/dinheiro"
	"cardapio-online/internal/middleware"
	"cardapio-online/internal/models"
	"cardapio-online/internal/validate"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// GetMenu devolve o cardápio completo para o painel do lojista.
func (h *Handler) GetMenu(c *gin.Context) {
	var itens []models.MenuItem
	if err := h.DB.Scopes(middleware.TenantScope(c)).
		Order("categoria asc, nome asc").Find(&itens).Error; err != nil {
		h.erroInterno(c, "listar cardápio", err)
		return
	}
	c.JSON(http.StatusOK, itens)
}

// GetPublicMenu devolve o cardápio público de um restaurante.
func (h *Handler) GetPublicMenu(c *gin.Context) {
	t, err := h.tenantDoContexto(c)
	if err != nil {
		erroCliente(c, http.StatusNotFound, "Restaurante não encontrado")
		return
	}

	var itens []models.MenuItem
	if err := h.DB.Where("tenant_id = ? AND disponivel = ?", t.ID, true).
		Order("categoria asc, nome asc").Find(&itens).Error; err != nil {
		h.erroInterno(c, "listar cardápio público", err)
		return
	}

	restaurante := gin.H{
		"nome": t.Nome,
		"slug": t.Slug,
		// Métodos aceites na caixa. No MVP o serviço é sempre levantamento ao balcão
		// com pagamento no local.
		"dinheiro_ativo": t.DinheiroAtivo,
		"cartao_ativo":   t.CartaoDebitoAtivo,
		"tipo_servico":   "levantamento",

		// Identidade visual: é isto que faz o storefront parecer do restaurante e não
		// da plataforma.
		"logo_url":                 t.LogoURL,
		"descricao_curta":          t.DescricaoCurta,
		"mostrar_marca_plataforma": t.MostrarMarcaPlataforma,
		"iniciais":                 iniciaisDe(t.Nome),
	}

	if t.CorPrimaria != "" {
		restaurante["cor_primaria"] = t.CorPrimaria
		// A cor de texto é calculada no servidor a partir da luminância: o lojista escolhe
		// a cor da marca sem pensar em contraste, e uma cor clara com texto branco por
		// cima tornaria o botão de encomendar ilegível.
		restaurante["cor_texto_sobre_primaria"] = validate.TextoSobreCor(t.CorPrimaria)
	}
	if t.CorSecundaria != "" {
		restaurante["cor_secundaria"] = t.CorSecundaria
	}

	// Nota de IVA. Em Portugal os preços afixados ao consumidor final incluem imposto;
	// dizê-lo explicitamente é a prática habitual e evita dúvidas na caixa.
	restaurante["iva_incluido"] = true
	restaurante["nota_iva"] = "Preços com IVA incluído à taxa legal em vigor"

	// Cada item leva o preço a cobrar já resolvido (com desconto, se activo) e a
	// decomposição de IVA, para que o cliente veja o valor exacto sem o frontend ter de
	// repetir o cálculo.
	itensPublicos := make([]gin.H, 0, len(itens))
	for i := range itens {
		it := &itens[i]
		efetivo := it.PrecoEfetivoCents()
		iva := dinheiro.IVAIncluido(efetivo, it.TaxaIVABP)

		itensPublicos = append(itensPublicos, gin.H{
			"id":                   it.ID,
			"nome":                 it.Nome,
			"descricao":            it.Descricao,
			"categoria":            it.Categoria,
			"imagem_url":           it.ImagemURL,
			"disponivel":           it.Disponivel,
			"preco_cents":          it.PrecoCents,
			"preco_desconto_cents": it.PrecoDescontoCents,
			"desconto_ativo":       it.DescontoAtivo,
			"preco_efetivo_cents":  efetivo,
			"taxa_iva_bp":          it.TaxaIVABP,
			"taxa_iva_texto":       it.TaxaIVABP.Percentagem(),
			"iva_cents":            iva,
			"base_cents":           efetivo - iva,
		})
	}

	c.JSON(http.StatusOK, gin.H{
		"restaurante": restaurante,
		"itens":       itensPublicos,
	})
}

type menuItemInput struct {
	Nome      string `json:"nome" binding:"required,min=1,max=150"`
	Descricao string `json:"descricao" binding:"max=2000"`

	// O preço é aceite como texto para não passar por float em nenhum ponto: "12,50",
	// "12.50" e "12,5" são todos válidos e convertidos directamente para cêntimos.
	// Os campos float são aceites por compatibilidade com clientes antigos.
	PrecoTexto         string  `json:"preco_texto"`
	PrecoDescontoTexto string  `json:"preco_desconto_texto"`
	Preco              float64 `json:"preco"`
	PrecoDesconto      float64 `json:"preco_desconto"`

	// TaxaIVABP é escolhida pelo estabelecimento para este produto. Ausente significa
	// usar a taxa por omissão do restaurante.
	TaxaIVABP *int32 `json:"taxa_iva_bp"`

	DescontoAtivo bool   `json:"desconto_ativo"`
	Categoria     string `json:"categoria" binding:"required,min=1,max=50"`
	ImagemURL     string `json:"imagem_url" binding:"max=1000"`
	Disponivel    *bool  `json:"disponivel"`

	// Preenchidos por resolverValores.
	precoCents         dinheiro.Cents
	precoDescontoCents dinheiro.Cents
	taxa               dinheiro.TaxaBP
}

// resolverValores converte os campos de entrada em cêntimos e resolve a taxa de IVA.
func (in *menuItemInput) resolverValores(taxaOmissao dinheiro.TaxaBP) error {
	var err error

	if strings.TrimSpace(in.PrecoTexto) != "" {
		if in.precoCents, err = dinheiro.Parse(in.PrecoTexto); err != nil {
			return fmt.Errorf("preço: %w", err)
		}
	} else {
		in.precoCents = dinheiro.DeEuros(in.Preco)
	}

	if strings.TrimSpace(in.PrecoDescontoTexto) != "" {
		if in.precoDescontoCents, err = dinheiro.Parse(in.PrecoDescontoTexto); err != nil {
			return fmt.Errorf("preço com desconto: %w", err)
		}
	} else {
		in.precoDescontoCents = dinheiro.DeEuros(in.PrecoDesconto)
	}

	in.taxa = taxaOmissao
	if in.TaxaIVABP != nil {
		in.taxa = dinheiro.TaxaBP(*in.TaxaIVABP)
	}
	if !in.taxa.Valida() {
		return errors.New("taxa de IVA inválida")
	}
	return nil
}

// validar aplica as regras que o binding não cobre. Corre depois de resolverValores.
func (in *menuItemInput) validar() error {
	if in.precoCents <= 0 {
		return errors.New("indique um preço maior que zero")
	}
	if in.precoCents > dinheiro.MaxCents {
		return errors.New("preço acima do limite permitido")
	}
	// A versão anterior aceitava um desconto superior ao preço, o que produzia encomendas
	// com valor negativo.
	if in.DescontoAtivo {
		if in.precoDescontoCents <= 0 {
			return errors.New("indique o preço com desconto")
		}
		if in.precoDescontoCents >= in.precoCents {
			return errors.New("o preço com desconto tem de ser inferior ao preço normal")
		}
	}
	if in.ImagemURL != "" && !urlImagemAceitavel(in.ImagemURL) {
		return errors.New("o endereço da imagem tem de começar por https:// ou ser um ficheiro carregado")
	}
	return nil
}

// urlImagemAceitavel restringe as imagens a HTTPS ou a uploads locais.
//
// Impede javascript: e data: em atributos src, e evita conteúdo misto (imagem em HTTP
// numa página HTTPS), que os browsers bloqueiam.
func urlImagemAceitavel(u string) bool {
	l := strings.ToLower(strings.TrimSpace(u))
	return strings.HasPrefix(l, "https://") || strings.HasPrefix(l, "/static/uploads/")
}

func (in *menuItemInput) aplicar(item *models.MenuItem) {
	item.Nome = limparLinha(in.Nome, 150)
	item.Descricao = limparTexto(in.Descricao, 2000)
	item.PrecoCents = in.precoCents
	item.PrecoDescontoCents = in.precoDescontoCents
	item.TaxaIVABP = in.taxa
	item.DescontoAtivo = in.DescontoAtivo
	item.Categoria = limparLinha(in.Categoria, 50)
	item.ImagemURL = strings.TrimSpace(in.ImagemURL)
	if in.Disponivel != nil {
		item.Disponivel = *in.Disponivel
	}
	// Mantém as colunas decimal antigas alinhadas, para permitir rollback do binário.
	item.SincronizarLegado()
}

// iniciaisDe devolve até duas iniciais do nome, usadas como logótipo de recurso quando o
// restaurante ainda não carregou um. Melhor do que a letra "C" da plataforma, que era o
// que aparecia antes.
func iniciaisDe(nome string) string {
	palavras := strings.Fields(nome)
	var iniciais []rune

	for _, p := range palavras {
		r := []rune(p)
		if len(r) == 0 {
			continue
		}
		// Ignora ligações comuns em nomes portugueses: "Tasca do Bairro" -> "TB".
		minuscula := strings.ToLower(p)
		if len(iniciais) > 0 && (minuscula == "do" || minuscula == "da" || minuscula == "de" ||
			minuscula == "dos" || minuscula == "das" || minuscula == "e" || minuscula == "&") {
			continue
		}
		iniciais = append(iniciais, []rune(strings.ToUpper(string(r[0])))[0])
		if len(iniciais) == 2 {
			break
		}
	}

	if len(iniciais) == 0 {
		return "?"
	}
	return string(iniciais)
}

// CreateMenuItem cria um item de cardápio.
func (h *Handler) CreateMenuItem(c *gin.Context) {
	var in menuItemInput
	if err := c.ShouldBindJSON(&in); err != nil {
		erroCliente(c, http.StatusBadRequest, "Dados do produto inválidos")
		return
	}

	t, err := h.tenantDoContexto(c)
	if err != nil {
		erroCliente(c, http.StatusNotFound, "Restaurante não encontrado")
		return
	}
	if err := in.resolverValores(t.TaxaIVAOmissaoBP); err != nil {
		erroCliente(c, http.StatusBadRequest, err.Error())
		return
	}
	if err := in.validar(); err != nil {
		erroCliente(c, http.StatusBadRequest, err.Error())
		return
	}

	item := models.MenuItem{TenantID: middleware.GetTenantID(c), Disponivel: true}
	in.aplicar(&item)
	// O tenant vem sempre do token: um tenant_id enviado no corpo é ignorado.
	item.TenantID = middleware.GetTenantID(c)

	if err := h.DB.Create(&item).Error; err != nil {
		h.erroInterno(c, "criar item de cardápio", err)
		return
	}

	h.auditar(c, "produto_criado", "menu_item", fmt.Sprint(item.ID), item.Nome)
	c.JSON(http.StatusCreated, item)
}

// UpdateMenuItem edita um item existente.
func (h *Handler) UpdateMenuItem(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		erroCliente(c, http.StatusBadRequest, "Identificador inválido")
		return
	}

	// O escopo de tenant na leitura é o que impede editar o produto de outro restaurante.
	var item models.MenuItem
	if err := h.DB.Scopes(middleware.TenantScope(c)).Where("id = ?", id).First(&item).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			erroCliente(c, http.StatusNotFound, "Produto não encontrado")
			return
		}
		h.erroInterno(c, "procurar item de cardápio", err)
		return
	}

	var in menuItemInput
	if err := c.ShouldBindJSON(&in); err != nil {
		erroCliente(c, http.StatusBadRequest, "Dados do produto inválidos")
		return
	}

	// A taxa por omissão só se aplica quando o cliente não indica nenhuma; manter a taxa
	// actual do produto seria mais surpreendente do que usar a do restaurante.
	taxaOmissao := item.TaxaIVABP
	if err := in.resolverValores(taxaOmissao); err != nil {
		erroCliente(c, http.StatusBadRequest, err.Error())
		return
	}
	if err := in.validar(); err != nil {
		erroCliente(c, http.StatusBadRequest, err.Error())
		return
	}

	in.aplicar(&item)

	if err := h.DB.Save(&item).Error; err != nil {
		h.erroInterno(c, "actualizar item de cardápio", err)
		return
	}

	h.auditar(c, "produto_actualizado", "menu_item", fmt.Sprint(item.ID), item.Nome)
	c.JSON(http.StatusOK, item)
}

// DeleteMenuItem remove um item do cardápio.
func (h *Handler) DeleteMenuItem(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		erroCliente(c, http.StatusBadRequest, "Identificador inválido")
		return
	}

	res := h.DB.Scopes(middleware.TenantScope(c)).Where("id = ?", id).Delete(&models.MenuItem{})
	if res.Error != nil {
		h.erroInterno(c, "remover item de cardápio", res.Error)
		return
	}
	// A versão anterior respondia sempre com sucesso, mesmo quando nada era apagado —
	// incluindo quando o ID pertencia a outro restaurante.
	if res.RowsAffected == 0 {
		erroCliente(c, http.StatusNotFound, "Produto não encontrado")
		return
	}

	h.auditar(c, "produto_removido", "menu_item", fmt.Sprint(id), "")
	c.JSON(http.StatusOK, gin.H{"message": "Produto removido"})
}
