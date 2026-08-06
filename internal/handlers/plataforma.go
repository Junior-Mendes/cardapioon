package handlers

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"cardapio-online/internal/auth"
	"cardapio-online/internal/dinheiro"
	"cardapio-online/internal/models"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// Painel do dono do SaaS: ver e gerir os restaurantes clientes.
//
// Estas rotas são as únicas da aplicação que leem várias contas de restaurantes ao mesmo
// tempo. É por isso que vivem atrás de RequirePlataforma e nunca de RequireAuth: as rotas
// /api/admin/* têm a garantia de que toda a leitura passa por middleware.TenantScope, e
// abrir uma excepção lá — uma role que ignorasse o escopo — tornaria essa garantia
// condicional. Aqui não há escopo de tenant porque não há tenant nenhum no contexto.
//
// Minimização de dados: o painel mostra contagens, volumes e estados, mas não o nome nem o
// telefone dos consumidores finais. Quem opera a plataforma não precisa deles para gerir
// subscrições, e o restaurante é o responsável pelo tratamento desses dados.

// janelaRecente é o período usado nas métricas "recentes" do painel.
const janelaRecente = 30 * 24 * time.Hour

// diasSerie é o comprimento da série diária mostrada nos gráficos do painel.
const diasSerie = 14

// PlataformaResumo devolve os indicadores agregados de toda a plataforma.
func (h *Handler) PlataformaResumo(c *gin.Context) {
	agora := time.Now()
	inicioHoje := time.Date(agora.Year(), agora.Month(), agora.Day(), 0, 0, 0, 0, agora.Location())
	inicio30d := agora.Add(-janelaRecente)
	inicio7d := agora.Add(-7 * 24 * time.Hour)

	// Subconsultas em vez de JOINs: com JOINs, contar utilizadores e encomendas na mesma
	// query multiplica as linhas umas pelas outras e inflaciona os dois números.
	var resumo struct {
		Restaurantes        int64          `gorm:"column:restaurantes"`
		RestaurantesActivos int64          `gorm:"column:restaurantes_activos"`
		Novos30d            int64          `gorm:"column:novos_30d"`
		ComDominio          int64          `gorm:"column:com_dominio"`
		Utilizadores        int64          `gorm:"column:utilizadores"`
		Produtos            int64          `gorm:"column:produtos"`
		ComVendas7d         int64          `gorm:"column:com_vendas_7d"`
		EncomendasHoje      int64          `gorm:"column:encomendas_hoje"`
		VolumeHojeCents     dinheiro.Cents `gorm:"column:volume_hoje_cents"`
		Encomendas30d       int64          `gorm:"column:encomendas_30d"`
		Volume30dCents      dinheiro.Cents `gorm:"column:volume_30d_cents"`
		IVA30dCents         dinheiro.Cents `gorm:"column:iva_30d_cents"`
		EncomendasTotal     int64          `gorm:"column:encomendas_total"`
	}

	// Encomendas canceladas não contam para volume: não houve venda.
	const sqlResumo = `SELECT
		(SELECT COUNT(*) FROM tenants) AS restaurantes,
		(SELECT COUNT(*) FROM tenants WHERE ativo = 1) AS restaurantes_activos,
		(SELECT COUNT(*) FROM tenants WHERE created_at >= ?) AS novos_30d,
		(SELECT COUNT(*) FROM tenants WHERE domain_status = 'verified') AS com_dominio,
		(SELECT COUNT(*) FROM usuarios) AS utilizadores,
		(SELECT COUNT(*) FROM menu_items) AS produtos,
		(SELECT COUNT(DISTINCT tenant_id) FROM pedidos
		   WHERE status <> 'cancelado' AND created_at >= ?) AS com_vendas_7d,
		(SELECT COUNT(*) FROM pedidos
		   WHERE status <> 'cancelado' AND created_at >= ?) AS encomendas_hoje,
		(SELECT COALESCE(SUM(valor_total_cents), 0) FROM pedidos
		   WHERE status <> 'cancelado' AND created_at >= ?) AS volume_hoje_cents,
		(SELECT COUNT(*) FROM pedidos
		   WHERE status <> 'cancelado' AND created_at >= ?) AS encomendas_30d,
		(SELECT COALESCE(SUM(valor_total_cents), 0) FROM pedidos
		   WHERE status <> 'cancelado' AND created_at >= ?) AS volume_30d_cents,
		(SELECT COALESCE(SUM(iva_cents), 0) FROM pedidos
		   WHERE status <> 'cancelado' AND created_at >= ?) AS iva_30d_cents,
		(SELECT COUNT(*) FROM pedidos WHERE status <> 'cancelado') AS encomendas_total`

	if err := h.DB.Raw(sqlResumo,
		inicio30d, inicio7d, inicioHoje, inicioHoje, inicio30d, inicio30d, inicio30d,
	).Scan(&resumo).Error; err != nil {
		h.erroInterno(c, "calcular resumo da plataforma", err)
		return
	}

	serie, err := h.seriePorDia(nil, agora)
	if err != nil {
		h.erroInterno(c, "calcular série diária da plataforma", err)
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"restaurantes":           resumo.Restaurantes,
		"restaurantes_activos":   resumo.RestaurantesActivos,
		"restaurantes_inactivos": resumo.Restaurantes - resumo.RestaurantesActivos,
		"novos_30d":              resumo.Novos30d,
		"com_dominio_proprio":    resumo.ComDominio,
		"com_vendas_7d":          resumo.ComVendas7d,
		"utilizadores":           resumo.Utilizadores,
		"produtos":               resumo.Produtos,
		"encomendas_hoje":        resumo.EncomendasHoje,
		"volume_hoje_cents":      resumo.VolumeHojeCents,
		"encomendas_30d":         resumo.Encomendas30d,
		"volume_30d_cents":       resumo.Volume30dCents,
		"iva_30d_cents":          resumo.IVA30dCents,
		"encomendas_total":       resumo.EncomendasTotal,
		"serie":                  serie,
		"dias_serie":             diasSerie,
		"main_domain":            h.Cfg.MainDomain,
	})
}

// pontoSerie é um dia da série de encomendas.
type pontoSerie struct {
	Dia         string         `gorm:"column:dia" json:"dia"`
	Encomendas  int64          `gorm:"column:encomendas" json:"encomendas"`
	VolumeCents dinheiro.Cents `gorm:"column:volume_cents" json:"volume_cents"`
}

// seriePorDia devolve as encomendas por dia dos últimos diasSerie dias, para a plataforma
// inteira (tenantID nil) ou para um restaurante.
//
// Os dias sem encomendas são preenchidos com zero: um gráfico que salta os dias vazios
// comprime o eixo do tempo e sugere uma regularidade que não existe.
func (h *Handler) seriePorDia(tenantID *uint, agora time.Time) ([]pontoSerie, error) {
	inicio := time.Date(agora.Year(), agora.Month(), agora.Day(), 0, 0, 0, 0, agora.Location()).
		AddDate(0, 0, -(diasSerie - 1))

	// DATE_FORMAT em vez de DATE(): a coluna formatada chega como string, sem depender de
	// como o driver converte um DATE para time.Time.
	sql := `SELECT DATE_FORMAT(created_at, '%Y-%m-%d') AS dia,
	               COUNT(*) AS encomendas,
	               COALESCE(SUM(valor_total_cents), 0) AS volume_cents
	          FROM pedidos
	         WHERE status <> 'cancelado' AND created_at >= ?`
	args := []any{inicio}
	if tenantID != nil {
		sql += " AND tenant_id = ?"
		args = append(args, *tenantID)
	}
	sql += " GROUP BY dia ORDER BY dia"

	var linhas []pontoSerie
	if err := h.DB.Raw(sql, args...).Scan(&linhas).Error; err != nil {
		return nil, err
	}

	porDia := make(map[string]pontoSerie, len(linhas))
	for _, l := range linhas {
		porDia[l.Dia] = l
	}

	serie := make([]pontoSerie, 0, diasSerie)
	for i := 0; i < diasSerie; i++ {
		dia := inicio.AddDate(0, 0, i).Format("2006-01-02")
		if p, existe := porDia[dia]; existe {
			serie = append(serie, p)
			continue
		}
		serie = append(serie, pontoSerie{Dia: dia})
	}
	return serie, nil
}

// linhaRestaurante é uma linha da listagem de clientes.
type linhaRestaurante struct {
	ID              uint           `gorm:"column:id" json:"id"`
	Nome            string         `gorm:"column:nome" json:"nome"`
	Slug            string         `gorm:"column:slug" json:"slug"`
	NIF             string         `gorm:"column:nif" json:"nif"`
	Domain          *string        `gorm:"column:domain" json:"domain"`
	DomainStatus    string         `gorm:"column:domain_status" json:"domain_status"`
	Ativo           bool           `gorm:"column:ativo" json:"ativo"`
	CreatedAt       time.Time      `gorm:"column:created_at" json:"created_at"`
	Utilizadores    int64          `gorm:"column:utilizadores" json:"utilizadores"`
	Produtos        int64          `gorm:"column:produtos" json:"produtos"`
	Encomendas      int64          `gorm:"column:encomendas" json:"encomendas"`
	EncomendasRec   int64          `gorm:"column:encomendas_rec" json:"encomendas_30d"`
	VolumeCents     dinheiro.Cents `gorm:"column:volume_cents" json:"volume_cents"`
	VolumeRecCents  dinheiro.Cents `gorm:"column:volume_rec_cents" json:"volume_30d_cents"`
	UltimaEncomenda *time.Time     `gorm:"column:ultima_encomenda" json:"ultima_encomenda"`
}

// ordensListagem é a lista branca de ordenações aceitas.
//
// Lista branca, e não interpolação do parâmetro: a cláusula ORDER BY não aceita parâmetros
// preparados, pelo que qualquer valor vindo do cliente que entrasse aqui directamente
// seria injecção de SQL.
var ordensListagem = map[string]string{
	"recentes":   "t.created_at DESC",
	"nome":       "t.nome ASC",
	"volume":     "volume_rec_cents DESC, t.nome ASC",
	"encomendas": "encomendas_rec DESC, t.nome ASC",
	"actividade": "ultima_encomenda IS NULL, ultima_encomenda DESC",
}

// PlataformaListarRestaurantes lista os clientes com as métricas de cada um.
func (h *Handler) PlataformaListarRestaurantes(c *gin.Context) {
	pagina, porPagina := paginacao(c, 20, 100)
	inicioRec := time.Now().Add(-janelaRecente)

	condicoes := []string{"1 = 1"}
	var argsFiltro []any

	if q := strings.TrimSpace(c.Query("q")); q != "" {
		if len(q) > 100 {
			q = q[:100]
		}
		padrao := "%" + escaparLike(q) + "%"
		condicoes = append(condicoes,
			"(t.nome LIKE ? OR t.slug LIKE ? OR t.domain LIKE ? OR t.nif LIKE ?)")
		argsFiltro = append(argsFiltro, padrao, padrao, padrao, padrao)
	}

	switch c.Query("estado") {
	case "activos":
		condicoes = append(condicoes, "t.ativo = 1")
	case "suspensos":
		condicoes = append(condicoes, "t.ativo = 0")
	case "sem_vendas":
		condicoes = append(condicoes,
			"NOT EXISTS (SELECT 1 FROM pedidos p WHERE p.tenant_id = t.id AND p.status <> 'cancelado')")
	}

	where := strings.Join(condicoes, " AND ")

	var total int64
	if err := h.DB.Raw("SELECT COUNT(*) FROM tenants t WHERE "+where, argsFiltro...).
		Scan(&total).Error; err != nil {
		h.erroInterno(c, "contar restaurantes", err)
		return
	}

	ordem, conhecida := ordensListagem[c.Query("ordem")]
	if !conhecida {
		ordem = ordensListagem["recentes"]
	}

	// Subconsultas correlacionadas: à escala desta listagem (dezenas a centenas de
	// restaurantes por página) são mais baratas do que agregar as tabelas todas, e não
	// sofrem da multiplicação de linhas que vários JOINs provocariam.
	sql := `SELECT t.id, t.nome, t.slug, COALESCE(t.nif, '') AS nif, t.domain, t.domain_status,
	               t.ativo, t.created_at,
	          (SELECT COUNT(*) FROM usuarios u WHERE u.tenant_id = t.id) AS utilizadores,
	          (SELECT COUNT(*) FROM menu_items m WHERE m.tenant_id = t.id) AS produtos,
	          (SELECT COUNT(*) FROM pedidos p
	            WHERE p.tenant_id = t.id AND p.status <> 'cancelado') AS encomendas,
	          (SELECT COUNT(*) FROM pedidos p
	            WHERE p.tenant_id = t.id AND p.status <> 'cancelado'
	              AND p.created_at >= ?) AS encomendas_rec,
	          (SELECT COALESCE(SUM(p.valor_total_cents), 0) FROM pedidos p
	            WHERE p.tenant_id = t.id AND p.status <> 'cancelado') AS volume_cents,
	          (SELECT COALESCE(SUM(p.valor_total_cents), 0) FROM pedidos p
	            WHERE p.tenant_id = t.id AND p.status <> 'cancelado'
	              AND p.created_at >= ?) AS volume_rec_cents,
	          (SELECT MAX(p.created_at) FROM pedidos p
	            WHERE p.tenant_id = t.id AND p.status <> 'cancelado') AS ultima_encomenda
	          FROM tenants t
	         WHERE ` + where + `
	         ORDER BY ` + ordem + `
	         LIMIT ? OFFSET ?`

	args := append([]any{inicioRec, inicioRec}, argsFiltro...)
	args = append(args, porPagina, (pagina-1)*porPagina)

	var linhas []linhaRestaurante
	if err := h.DB.Raw(sql, args...).Scan(&linhas).Error; err != nil {
		h.erroInterno(c, "listar restaurantes da plataforma", err)
		return
	}
	if linhas == nil {
		linhas = []linhaRestaurante{}
	}

	c.JSON(http.StatusOK, gin.H{
		"restaurantes": linhas,
		"total":        total,
		"pagina":       pagina,
		"por_pagina":   porPagina,
		"main_domain":  h.Cfg.MainDomain,
	})
}

// PlataformaVerRestaurante devolve o detalhe de um cliente.
func (h *Handler) PlataformaVerRestaurante(c *gin.Context) {
	id, ok := idDoParametro(c)
	if !ok {
		return
	}

	var t models.Tenant
	if err := h.DB.First(&t, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			erroCliente(c, http.StatusNotFound, "Restaurante não encontrado")
			return
		}
		h.erroInterno(c, "carregar restaurante", err)
		return
	}

	var utilizadores []models.Usuario
	if err := h.DB.Where("tenant_id = ?", t.ID).Order("id asc").Find(&utilizadores).Error; err != nil {
		h.erroInterno(c, "listar utilizadores do restaurante", err)
		return
	}
	// Construído à mão em vez de serializar models.Usuario: aqui só interessa quem tem
	// acesso à conta, não os campos internos do modelo.
	equipa := make([]gin.H, 0, len(utilizadores))
	for _, u := range utilizadores {
		equipa = append(equipa, gin.H{
			"id": u.ID, "nome": u.Nome, "email": u.Email, "role": u.Role,
			"ativo": u.Ativo, "last_login_at": u.LastLoginAt,
			"email_verificado": u.EmailVerifiedAt != nil,
			"created_at":       u.CreatedAt,
		})
	}

	agora := time.Now()
	inicioRec := agora.Add(-janelaRecente)

	var metricas struct {
		Produtos            int64          `gorm:"column:produtos"`
		ProdutosDisponiveis int64          `gorm:"column:produtos_disponiveis"`
		Encomendas          int64          `gorm:"column:encomendas"`
		EncomendasRec       int64          `gorm:"column:encomendas_rec"`
		Pendentes           int64          `gorm:"column:pendentes"`
		Canceladas          int64          `gorm:"column:canceladas"`
		VolumeCents         dinheiro.Cents `gorm:"column:volume_cents"`
		VolumeRecCents      dinheiro.Cents `gorm:"column:volume_rec_cents"`
		IVARecCents         dinheiro.Cents `gorm:"column:iva_rec_cents"`
		TicketMedioCents    dinheiro.Cents `gorm:"column:ticket_medio_cents"`
		UltimaEncomenda     *time.Time     `gorm:"column:ultima_encomenda"`
	}

	const sqlMetricas = `SELECT
		(SELECT COUNT(*) FROM menu_items WHERE tenant_id = ?) AS produtos,
		(SELECT COUNT(*) FROM menu_items WHERE tenant_id = ? AND disponivel = 1) AS produtos_disponiveis,
		(SELECT COUNT(*) FROM pedidos WHERE tenant_id = ? AND status <> 'cancelado') AS encomendas,
		(SELECT COUNT(*) FROM pedidos WHERE tenant_id = ? AND status <> 'cancelado'
		   AND created_at >= ?) AS encomendas_rec,
		(SELECT COUNT(*) FROM pedidos WHERE tenant_id = ? AND status = 'pendente') AS pendentes,
		(SELECT COUNT(*) FROM pedidos WHERE tenant_id = ? AND status = 'cancelado') AS canceladas,
		(SELECT COALESCE(SUM(valor_total_cents), 0) FROM pedidos WHERE tenant_id = ?
		   AND status <> 'cancelado') AS volume_cents,
		(SELECT COALESCE(SUM(valor_total_cents), 0) FROM pedidos WHERE tenant_id = ?
		   AND status <> 'cancelado' AND created_at >= ?) AS volume_rec_cents,
		(SELECT COALESCE(SUM(iva_cents), 0) FROM pedidos WHERE tenant_id = ?
		   AND status <> 'cancelado' AND created_at >= ?) AS iva_rec_cents,
		(SELECT COALESCE(ROUND(AVG(valor_total_cents)), 0) FROM pedidos WHERE tenant_id = ?
		   AND status <> 'cancelado') AS ticket_medio_cents,
		(SELECT MAX(created_at) FROM pedidos WHERE tenant_id = ?
		   AND status <> 'cancelado') AS ultima_encomenda`

	if err := h.DB.Raw(sqlMetricas,
		t.ID, t.ID, t.ID, t.ID, inicioRec, t.ID, t.ID, t.ID,
		t.ID, inicioRec, t.ID, inicioRec, t.ID, t.ID,
	).Scan(&metricas).Error; err != nil {
		h.erroInterno(c, "calcular métricas do restaurante", err)
		return
	}

	serie, err := h.seriePorDia(&t.ID, agora)
	if err != nil {
		h.erroInterno(c, "calcular série do restaurante", err)
		return
	}

	// Encomendas recentes sem qualquer dado do consumidor: só o que serve para avaliar a
	// actividade da conta. Quem opera a plataforma não precisa de saber quem encomendou.
	var recentes []struct {
		ID         uint           `gorm:"column:id" json:"id"`
		Status     string         `gorm:"column:status" json:"status"`
		ValorCents dinheiro.Cents `gorm:"column:valor_total_cents" json:"valor_total_cents"`
		Pagamento  string         `gorm:"column:forma_pagamento" json:"forma_pagamento"`
		CreatedAt  time.Time      `gorm:"column:created_at" json:"created_at"`
	}
	if err := h.DB.Raw(`SELECT id, status, valor_total_cents, forma_pagamento, created_at
	                      FROM pedidos WHERE tenant_id = ? ORDER BY created_at DESC LIMIT 15`,
		t.ID).Scan(&recentes).Error; err != nil {
		h.erroInterno(c, "listar encomendas recentes do restaurante", err)
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"restaurante": gin.H{
			"id": t.ID, "nome": t.Nome, "slug": t.Slug, "nif": t.NIF,
			"ativo": t.Ativo, "created_at": t.CreatedAt,
			"domain": t.Domain, "domain_status": t.DomainStatus,
			"domain_verified_at":       t.DomainVerifiedAt,
			"dinheiro_ativo":           t.DinheiroAtivo,
			"cartao_ativo":             t.CartaoDebitoAtivo,
			"taxa_iva_omissao_bp":      t.TaxaIVAOmissaoBP,
			"logo_url":                 t.LogoURL,
			"descricao_curta":          t.DescricaoCurta,
			"mostrar_marca_plataforma": t.MostrarMarcaPlataforma,
			"storefront_url":           fmt.Sprintf("https://%s.%s/menu", t.Slug, h.Cfg.MainDomain),
		},
		"equipa": equipa,
		"metricas": gin.H{
			"produtos":             metricas.Produtos,
			"produtos_disponiveis": metricas.ProdutosDisponiveis,
			"encomendas":           metricas.Encomendas,
			"encomendas_30d":       metricas.EncomendasRec,
			"pendentes":            metricas.Pendentes,
			"canceladas":           metricas.Canceladas,
			"volume_cents":         metricas.VolumeCents,
			"volume_30d_cents":     metricas.VolumeRecCents,
			"iva_30d_cents":        metricas.IVARecCents,
			"ticket_medio_cents":   metricas.TicketMedioCents,
			"ultima_encomenda":     metricas.UltimaEncomenda,
		},
		"serie":      serie,
		"encomendas": recentes,
	})
}

type estadoRestauranteInput struct {
	// Ponteiro para distinguir "não enviado" de "enviado false".
	Ativo *bool `json:"ativo"`
	// Motivo é livre e fica no registo de auditoria: dentro de seis meses ninguém se lembra
	// por que razão uma conta foi suspensa.
	Motivo string `json:"motivo"`
}

// PlataformaDefinirEstado suspende ou reactiva um restaurante.
//
// Suspender é a operação de cobrança da plataforma, e é reversível de propósito: apagar
// dados de um cliente por causa de uma factura em atraso destrói o histórico de IVA que ele
// é obrigado a conservar.
func (h *Handler) PlataformaDefinirEstado(c *gin.Context) {
	id, ok := idDoParametro(c)
	if !ok {
		return
	}

	var in estadoRestauranteInput
	if err := c.ShouldBindJSON(&in); err != nil || in.Ativo == nil {
		erroCliente(c, http.StatusBadRequest, "Indique se o restaurante fica activo ou suspenso")
		return
	}

	var t models.Tenant
	if err := h.DB.First(&t, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			erroCliente(c, http.StatusNotFound, "Restaurante não encontrado")
			return
		}
		h.erroInterno(c, "carregar restaurante para alterar estado", err)
		return
	}

	if t.Ativo == *in.Ativo {
		c.JSON(http.StatusOK, gin.H{
			"message": "O restaurante já estava neste estado", "ativo": t.Ativo,
		})
		return
	}

	if err := h.DB.Model(&t).Update("ativo", *in.Ativo).Error; err != nil {
		h.erroInterno(c, "alterar estado do restaurante", err)
		return
	}
	t.Ativo = *in.Ativo

	if t.Ativo {
		// Repor o encaminhamento: o storefront volta a responder no endereço do cliente.
		h.sincronizarRotaTenant(&t)
	} else {
		// Sem remover o ficheiro de rota, o Traefik continuaria a encaminhar o subdomínio
		// para a aplicação; o menu daria 404, mas o certificado continuaria a ser renovado
		// e o endereço a parecer vivo.
		if err := h.Traefik.DeleteTenant(t.Slug); err != nil {
			// A suspensão em si já está gravada, e o storefront já recusa servir um tenant
			// inactivo. Não desfazer a operação por causa do encaminhador.
			slog.Error("falha ao remover rota do Traefik ao suspender",
				"slug", t.Slug, "erro", err)
		}
		// Terminar as sessões abertas do lojista. O access token já emitido continua válido
		// até expirar (no máximo o AccessTTL), mas sem refresh não é renovado.
		if err := h.DB.Model(&models.RefreshToken{}).
			Where("tenant_id = ? AND revoked_at IS NULL", t.ID).
			Update("revoked_at", time.Now()).Error; err != nil {
			slog.Error("falha ao revogar sessões do restaurante suspenso",
				"slug", t.Slug, "erro", err)
		}
	}

	acao := "plataforma_restaurante_suspenso"
	mensagem := "Restaurante suspenso. O menu deixa de responder e a equipa não consegue entrar."
	if t.Ativo {
		acao = "plataforma_restaurante_reactivado"
		mensagem = "Restaurante reactivado. O endereço público pode levar alguns segundos a responder."
	}

	tenantID := t.ID
	h.auditarPlataforma(c, acao, "tenant", fmt.Sprint(t.ID),
		limparLinha(in.Motivo, 200), &tenantID)

	c.JSON(http.StatusOK, gin.H{"message": mensagem, "ativo": t.Ativo})
}

// PlataformaEnviarRecuperacao envia ao proprietário de um restaurante um link para
// redefinir a senha.
//
// É a ferramenta de suporte para o caso mais comum: o lojista perdeu o acesso e o email de
// recuperação não chegou. O link vai para o email do proprietário, nunca para quem o pediu,
// pelo que não é uma via para o operador da plataforma entrar na conta do cliente.
func (h *Handler) PlataformaEnviarRecuperacao(c *gin.Context) {
	id, ok := idDoParametro(c)
	if !ok {
		return
	}

	var t models.Tenant
	if err := h.DB.First(&t, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			erroCliente(c, http.StatusNotFound, "Restaurante não encontrado")
			return
		}
		h.erroInterno(c, "carregar restaurante para recuperação", err)
		return
	}

	var proprietario models.Usuario
	err := h.DB.Where("tenant_id = ? AND ativo = ? AND role = ?", t.ID, true, models.RoleOwner).
		Order("id asc").First(&proprietario).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		// Conta antiga sem owner explícito: qualquer administrador serve.
		err = h.DB.Where("tenant_id = ? AND ativo = ? AND role = ?", t.ID, true, models.RoleAdmin).
			Order("id asc").First(&proprietario).Error
	}
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			erroCliente(c, http.StatusNotFound,
				"Este restaurante não tem nenhuma conta activa de proprietário ou administrador")
			return
		}
		h.erroInterno(c, "procurar proprietário do restaurante", err)
		return
	}

	token := auth.NewOpaqueToken(32)
	registo := models.PasswordReset{
		UsuarioID: proprietario.ID,
		TokenHash: auth.HashToken(token),
		ExpiresAt: time.Now().Add(validadeResetSenha),
		CreatedIP: c.ClientIP(),
		CreatedAt: time.Now(),
	}
	if err := h.DB.Create(&registo).Error; err != nil {
		h.erroInterno(c, "criar pedido de recuperação a partir da plataforma", err)
		return
	}

	go h.enviarEmailReset(proprietario, token)

	tenantID := t.ID
	h.auditarPlataforma(c, "plataforma_recuperacao_enviada", "usuario",
		fmt.Sprint(proprietario.ID), proprietario.Email, &tenantID)

	c.JSON(http.StatusOK, gin.H{
		"message": fmt.Sprintf(
			"Link de recuperação enviado para %s. É válido durante 30 minutos.",
			mascararEmail(proprietario.Email)),
	})
}

// PlataformaAuditoria devolve o registo de acções administrativas de toda a plataforma.
//
// É a única leitura transversal do registo de auditoria. Serve para responder à pergunta
// "quem mudou isto?" quando um cliente reporta uma alteração que não reconhece.
func (h *Handler) PlataformaAuditoria(c *gin.Context) {
	pagina, porPagina := paginacao(c, 50, 200)

	condicoes := []string{"1 = 1"}
	var argsFiltro []any

	if v := strings.TrimSpace(c.Query("tenant_id")); v != "" {
		tenantID, err := strconv.ParseUint(v, 10, 32)
		if err != nil {
			erroCliente(c, http.StatusBadRequest, "Identificador de restaurante inválido")
			return
		}
		condicoes = append(condicoes, "a.tenant_id = ?")
		argsFiltro = append(argsFiltro, tenantID)
	}
	if v := strings.TrimSpace(c.Query("acao")); v != "" {
		if len(v) > 80 {
			v = v[:80]
		}
		condicoes = append(condicoes, "a.acao LIKE ?")
		argsFiltro = append(argsFiltro, escaparLike(v)+"%")
	}

	where := strings.Join(condicoes, " AND ")

	var total int64
	if err := h.DB.Raw("SELECT COUNT(*) FROM audit_logs a WHERE "+where, argsFiltro...).
		Scan(&total).Error; err != nil {
		h.erroInterno(c, "contar registos de auditoria", err)
		return
	}

	var registos []struct {
		ID              uint      `gorm:"column:id" json:"id"`
		TenantID        *uint     `gorm:"column:tenant_id" json:"tenant_id"`
		RestauranteNome string    `gorm:"column:restaurante_nome" json:"restaurante_nome"`
		UsuarioID       *uint     `gorm:"column:usuario_id" json:"usuario_id"`
		UsuarioEmail    string    `gorm:"column:usuario_email" json:"usuario_email"`
		Acao            string    `gorm:"column:acao" json:"acao"`
		Recurso         string    `gorm:"column:recurso" json:"recurso"`
		RecursoID       string    `gorm:"column:recurso_id" json:"recurso_id"`
		Detalhe         string    `gorm:"column:detalhe" json:"detalhe"`
		IP              string    `gorm:"column:ip" json:"ip"`
		CreatedAt       time.Time `gorm:"column:created_at" json:"created_at"`
	}

	sql := `SELECT a.id, a.tenant_id, COALESCE(t.nome, '') AS restaurante_nome,
	               a.usuario_id, COALESCE(u.email, '') AS usuario_email,
	               a.acao, COALESCE(a.recurso, '') AS recurso,
	               COALESCE(a.recurso_id, '') AS recurso_id,
	               COALESCE(a.detalhe, '') AS detalhe, COALESCE(a.ip, '') AS ip,
	               a.created_at
	          FROM audit_logs a
	          LEFT JOIN tenants t ON t.id = a.tenant_id
	          LEFT JOIN usuarios u ON u.id = a.usuario_id
	         WHERE ` + where + `
	         ORDER BY a.id DESC
	         LIMIT ? OFFSET ?`

	args := append([]any{}, argsFiltro...)
	args = append(args, porPagina, (pagina-1)*porPagina)

	if err := h.DB.Raw(sql, args...).Scan(&registos).Error; err != nil {
		h.erroInterno(c, "listar registos de auditoria", err)
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"registos": registos, "total": total,
		"pagina": pagina, "por_pagina": porPagina,
	})
}

// --- Auxiliares ---

// idDoParametro lê o :id da rota e responde 400 se for inválido.
func idDoParametro(c *gin.Context) (uint, bool) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil || id == 0 {
		erroCliente(c, http.StatusBadRequest, "Identificador inválido")
		return 0, false
	}
	return uint(id), true
}

// paginacao lê ?pagina e ?por_pagina, com limite máximo para que um pedido não possa
// exigir a tabela inteira numa resposta.
func paginacao(c *gin.Context, omissao, maximo int) (pagina, porPagina int) {
	pagina = 1
	if v, err := strconv.Atoi(c.Query("pagina")); err == nil && v > 1 {
		pagina = v
	}
	porPagina = omissao
	if v, err := strconv.Atoi(c.Query("por_pagina")); err == nil && v > 0 {
		porPagina = v
	}
	if porPagina > maximo {
		porPagina = maximo
	}
	return pagina, porPagina
}

// escaparLike neutraliza os caracteres especiais de um padrão LIKE.
//
// Sem isto, procurar "%" listaria tudo e "_" corresponderia a qualquer caractere — não é
// uma falha de segurança (o valor vai como parâmetro preparado), mas é uma pesquisa que
// devolve resultados errados.
func escaparLike(s string) string {
	s = strings.ReplaceAll(s, "\\", "\\\\")
	s = strings.ReplaceAll(s, "%", "\\%")
	s = strings.ReplaceAll(s, "_", "\\_")
	return s
}

// mascararEmail mostra o suficiente para o operador confirmar o destinatário sem que o
// endereço completo do cliente fique no ecrã e nos registos do browser.
func mascararEmail(email string) string {
	i := strings.LastIndex(email, "@")
	if i <= 0 {
		return "***"
	}
	local, dominio := email[:i], email[i:]
	if len(local) <= 2 {
		return local[:1] + "***" + dominio
	}
	return local[:2] + strings.Repeat("*", len(local)-2) + dominio
}
