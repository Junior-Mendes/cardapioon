// Package eventos distribui acontecimentos em tempo real para os painéis abertos.
//
// Substitui a sondagem de quinze segundos do painel: uma encomenda nova chega ao ecrã do
// lojista no momento em que é criada, e não até quinze segundos depois.
//
// LIMITE IMPORTANTE: o registo de subscritores vive em memória, neste processo. Com uma só
// instância isso é correcto e simples. Ao passar a várias réplicas, um painel ligado à
// réplica A não recebe eventos publicados na réplica B, e é preciso um canal partilhado
// (Redis pub/sub, NATS). O mesmo problema do rate limiter, e pela mesma razão.
package eventos

import (
	"log/slog"
	"sync"
	"time"
)

// Tipos de acontecimento. O nome viaja no campo `event` do SSE.
const (
	TipoEncomendaNova   = "encomenda_nova"
	TipoEncomendaEstado = "encomenda_estado"
	// TipoPing mantém a ligação viva através de proxies que fecham ligações inactivas.
	TipoPing = "ping"
)

// Evento é o que é entregue a um painel.
type Evento struct {
	Tipo  string `json:"tipo"`
	Dados any    `json:"dados,omitempty"`
	// Em milissegundos desde a época, para o cliente poder ignorar eventos antigos após
	// uma reconexão.
	Em int64 `json:"em"`
}

// tamanhoBuffer é a folga de cada subscritor.
//
// Um painel lento não deve bloquear a criação de encomendas. Se o buffer enche, o evento é
// descartado para esse subscritor: o painel recarrega a lista completa na reconexão, pelo
// que perder uma notificação atrasa o aviso mas nunca perde a encomenda.
const tamanhoBuffer = 16

type subscritor struct {
	canal    chan Evento
	tenantID uint
}

// Broker encaminha eventos para os subscritores do respectivo restaurante.
type Broker struct {
	mu sync.RWMutex
	// Subscritores agrupados por tenant: um evento nunca pode chegar ao painel de outro
	// restaurante, e agrupar por tenant torna isso estrutural em vez de depender de um
	// filtro que alguém se pode esquecer de aplicar.
	porTenant map[uint]map[*subscritor]struct{}
}

func NewBroker() *Broker {
	return &Broker{porTenant: map[uint]map[*subscritor]struct{}{}}
}

// Subscrever registra um painel e devolve o canal de leitura e a função de saída.
func (b *Broker) Subscrever(tenantID uint) (<-chan Evento, func()) {
	s := &subscritor{canal: make(chan Evento, tamanhoBuffer), tenantID: tenantID}

	b.mu.Lock()
	if b.porTenant[tenantID] == nil {
		b.porTenant[tenantID] = map[*subscritor]struct{}{}
	}
	b.porTenant[tenantID][s] = struct{}{}
	total := len(b.porTenant[tenantID])
	b.mu.Unlock()

	slog.Debug("painel subscrito a eventos", "tenant_id", tenantID, "painéis", total)

	sair := func() {
		b.mu.Lock()
		if conjunto, existe := b.porTenant[tenantID]; existe {
			delete(conjunto, s)
			if len(conjunto) == 0 {
				// Sem subscritores, o tenant sai do mapa: caso contrário o mapa cresce
				// indefinidamente com restaurantes que já ninguém está a ver.
				delete(b.porTenant, tenantID)
			}
		}
		b.mu.Unlock()
		close(s.canal)
	}

	return s.canal, sair
}

// Publicar envia um evento a todos os painéis de um restaurante.
//
// Nunca bloqueia: um painel com o buffer cheio perde este evento em vez de travar a
// operação que o originou.
func (b *Broker) Publicar(tenantID uint, tipo string, dados any) {
	ev := Evento{Tipo: tipo, Dados: dados, Em: time.Now().UnixMilli()}

	b.mu.RLock()
	defer b.mu.RUnlock()

	descartados := 0
	for s := range b.porTenant[tenantID] {
		select {
		case s.canal <- ev:
		default:
			descartados++
		}
	}

	if descartados > 0 {
		slog.Warn("eventos descartados por painéis lentos",
			"tenant_id", tenantID, "tipo", tipo, "descartados", descartados)
	}
}

// Subscritores devolve quantos painéis estão ligados a um restaurante. Usado no
// diagnóstico e nos testes.
func (b *Broker) Subscritores(tenantID uint) int {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return len(b.porTenant[tenantID])
}
