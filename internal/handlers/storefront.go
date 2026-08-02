package handlers

import (
	"crypto/tls"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"cardapio-online/internal/middleware"
	"cardapio-online/internal/models"

	"github.com/gin-gonic/gin"
)

// Estados possíveis do endereço público de um restaurante.
const (
	StorefrontPronto       = "pronto"
	StorefrontRotaPendente = "rota_pendente"
	StorefrontTLSPendente  = "tls_pendente"
	StorefrontErro         = "erro"
)

// cacheEstadoStorefront evita repetir o handshake TLS a cada sondagem do painel.
//
// O painel pergunta a cada dois segundos enquanto espera; sem cache, cada pergunta abria
// uma ligação nova ao Traefik.
var cacheEstadoStorefront = struct {
	sync.Mutex
	entradas map[uint]entradaEstado
}{entradas: map[uint]entradaEstado{}}

type entradaEstado struct {
	estado  string
	detalhe string
	expira  time.Time
}

// StorefrontStatus informa se o endereço público do restaurante já responde.
//
// Existe porque há uma janela de alguns segundos entre o registo e o endereço ficar
// utilizável: o backend grava o ficheiro de rota, o Traefik tem de o detectar, e no
// primeiro acesso por HTTPS o certificado ainda é pedido ao Let's Encrypt.
//
// Nessa janela não é possível servir uma página de espera no próprio subdomínio: sem rota,
// o pedido não chega à aplicação, e sem certificado o browser falha no TLS antes de haver
// HTTP. A espera tem de viver no painel, que é servido pelo domínio principal e responde
// sempre.
func (h *Handler) StorefrontStatus(c *gin.Context) {
	var t models.Tenant
	if err := h.DB.First(&t, middleware.GetTenantID(c)).Error; err != nil {
		erroCliente(c, http.StatusNotFound, "Restaurante não encontrado")
		return
	}

	host := fmt.Sprintf("%s.%s", t.Slug, h.Cfg.MainDomain)
	estado, detalhe := h.estadoDoStorefront(t, host)

	c.JSON(http.StatusOK, gin.H{
		"estado":  estado,
		"pronto":  estado == StorefrontPronto,
		"detalhe": detalhe,
		"url":     "https://" + host + "/menu",
		"host":    host,
	})
}

func (h *Handler) estadoDoStorefront(t models.Tenant, host string) (string, string) {
	cacheEstadoStorefront.Lock()
	if e, ok := cacheEstadoStorefront.entradas[t.ID]; ok && time.Now().Before(e.expira) {
		cacheEstadoStorefront.Unlock()
		return e.estado, e.detalhe
	}
	cacheEstadoStorefront.Unlock()

	estado, detalhe := h.verificarStorefront(t, host)

	// Um estado pronto é estável e pode ser cacheado por mais tempo; um pendente muda a
	// qualquer momento e merece uma janela curta.
	ttl := 2 * time.Second
	if estado == StorefrontPronto {
		ttl = 60 * time.Second
	}

	cacheEstadoStorefront.Lock()
	cacheEstadoStorefront.entradas[t.ID] = entradaEstado{
		estado: estado, detalhe: detalhe, expira: time.Now().Add(ttl),
	}
	cacheEstadoStorefront.Unlock()

	return estado, detalhe
}

func (h *Handler) verificarStorefront(t models.Tenant, host string) (string, string) {
	// 1. O ficheiro de rota é a única parte que controlamos directamente.
	caminho := filepath.Join(h.Cfg.TraefikDynamicDir, t.Slug+".yml")
	if _, err := os.Stat(caminho); err != nil {
		return StorefrontRotaPendente, "A publicar o endereço no encaminhador."
	}

	// 2. Handshake TLS através do Traefik, com o nome do subdomínio no SNI.
	//
	// A ligação é feita ao contentor do Traefik pela rede interna, e não ao endereço
	// público: assim o teste não depende de DNS externo nem de o servidor conseguir
	// alcançar-se a si próprio (hairpin NAT), que falha em muitas redes.
	if err := h.handshakeTraefik(host); err != nil {
		// O erro real é registado: sem isto, um estado permanentemente pendente é
		// indistinguível de um problema de configuração nosso.
		slog.Warn("verificação do endereço do restaurante falhou",
			"host", host, "traefik", h.Cfg.TraefikInternalAddr, "erro", err)

		msg := err.Error()
		switch {
		case strings.Contains(msg, "certificate"), strings.Contains(msg, "tls:"):
			return StorefrontTLSPendente,
				"O certificado de segurança está a ser emitido. Costuma levar menos de um minuto."
		case strings.Contains(msg, "connection refused"), strings.Contains(msg, "no such host"):
			// O encaminhador não é alcançável a partir daqui. Não conseguimos concluir
			// nada sobre o estado, e dizer que está pronto seria mentir.
			return StorefrontErro, "Não foi possível confirmar o estado do endereço."
		default:
			return StorefrontTLSPendente, "A preparar o endereço."
		}
	}

	return StorefrontPronto, "O seu endereço está activo."
}

// handshakeTraefik tenta um handshake TLS válido para o host indicado.
func (h *Handler) handshakeTraefik(host string) error {
	endereco := h.Cfg.TraefikInternalAddr
	if endereco == "" {
		return fmt.Errorf("endereço interno do Traefik não configurado")
	}

	dialer := &net.Dialer{Timeout: 3 * time.Second}
	conn, err := tls.DialWithDialer(dialer, "tcp", endereco, &tls.Config{
		// ServerName é o que faz o Traefik escolher a rota e o certificado.
		ServerName: host,
		// A verificação fica activa de propósito: é exactamente isto que queremos saber,
		// se já existe um certificado válido para este subdomínio.
		MinVersion: tls.VersionTLS12,
	})
	if err != nil {
		return err
	}
	return conn.Close()
}
