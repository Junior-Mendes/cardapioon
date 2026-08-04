package handlers

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"cardapio-online/internal/middleware"

	"github.com/gin-gonic/gin"
)

// intervaloPing é o espaçamento dos comentários de manutenção da ligação.
//
// Proxies e balanceadores fecham ligações sem tráfego, tipicamente ao minuto. Vinte
// segundos dá margem confortável sem gerar tráfego relevante.
const intervaloPing = 20 * time.Second

// Eventos mantém uma ligação aberta e envia acontecimentos do restaurante à medida que
// ocorrem, no formato Server-Sent Events.
//
// Substitui a sondagem de quinze segundos do painel. Escolhido SSE e não WebSocket porque o
// fluxo é num só sentido — servidor para painel — e o SSE reconecta sozinho, atravessa
// proxies como HTTP normal e não precisa de biblioteca.
//
// A autenticação usa o cabeçalho Authorization, como as restantes rotas administrativas.
// Isso obriga o cliente a usar fetch com ReadableStream em vez de EventSource, porque a API
// EventSource do browser não permite definir cabeçalhos. A alternativa seria passar o token
// na query string, o que o deixaria em registos de acesso de qualquer intermediário.
func (h *Handler) Eventos(c *gin.Context) {
	tenantID := middleware.GetTenantID(c)
	if tenantID == 0 {
		erroCliente(c, http.StatusUnauthorized, "Sessão inválida")
		return
	}

	// O servidor tem WriteTimeout de 60 segundos, o que é correcto para pedidos normais e
	// fatal para uma ligação de longa duração: mataria o stream ao minuto. O
	// ResponseController remove o prazo apenas nesta ligação, sem afectar as outras.
	rc := http.NewResponseController(c.Writer)
	if err := rc.SetWriteDeadline(time.Time{}); err != nil {
		slog.Warn("não foi possível remover o prazo de escrita; o stream vai fechar ao minuto",
			"erro", err)
	}

	c.Writer.Header().Set("Content-Type", "text/event-stream")
	c.Writer.Header().Set("Cache-Control", "no-cache, no-transform")
	c.Writer.Header().Set("Connection", "keep-alive")
	// Instrui proxies do tipo nginx a não acumularem a resposta antes de a entregar, o que
	// anularia o tempo real.
	c.Writer.Header().Set("X-Accel-Buffering", "no")
	c.Writer.WriteHeader(http.StatusOK)

	canal, sair := h.Eventos_.Subscrever(tenantID)
	defer sair()

	// Um comentário inicial confirma ao cliente que o stream abriu, mesmo que não haja
	// eventos durante minutos.
	if !escreverSSE(c, rc, ": ligado\n\n") {
		return
	}

	ticker := time.NewTicker(intervaloPing)
	defer ticker.Stop()

	// O contexto do pedido fecha quando o cliente desliga: é assim que o painel a fechar
	// liberta a subscrição.
	ctx := c.Request.Context()

	for {
		select {
		case <-ctx.Done():
			return

		case ev, aberto := <-canal:
			if !aberto {
				return
			}
			corpo, err := json.Marshal(ev)
			if err != nil {
				slog.Error("serializar evento", "tipo", ev.Tipo, "erro", err)
				continue
			}
			if !escreverSSE(c, rc, fmt.Sprintf("event: %s\ndata: %s\n\n", ev.Tipo, corpo)) {
				return
			}

		case <-ticker.C:
			// Comentário SSE (linha iniciada por dois pontos): mantém a ligação viva sem
			// que o cliente tenha de o interpretar.
			if !escreverSSE(c, rc, ": ping\n\n") {
				return
			}
		}
	}
}

// escreverSSE escreve e entrega imediatamente. Devolve false quando o cliente desapareceu.
func escreverSSE(c *gin.Context, rc *http.ResponseController, texto string) bool {
	if _, err := c.Writer.WriteString(texto); err != nil {
		return false
	}
	// Sem flush o conteúdo fica no buffer e o tempo real desaparece.
	return rc.Flush() == nil
}
