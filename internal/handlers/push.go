package handlers

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"

	"cardapio-online/internal/middleware"
	"cardapio-online/internal/models"

	"github.com/SherClockHolmes/webpush-go"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// SubscreverPushRequest representa a estrutura da subscrição vinda do frontend
type SubscreverPushRequest struct {
	Endpoint string `json:"endpoint" binding:"required"`
	Keys     struct {
		P256dh string `json:"p256dh" binding:"required"`
		Auth   string `json:"auth" binding:"required"`
	} `json:"keys" binding:"required"`
}

// CancelarPushRequest representa a estrutura do cancelamento
type CancelarPushRequest struct {
	Endpoint string `json:"endpoint" binding:"required"`
}

// GetPushPublicKey devolve a chave VAPID pública se o Web Push estiver configurado.
func (h *Handler) GetPushPublicKey(c *gin.Context) {
	publicKey := h.Cfg.VAPIDPublicKey
	if publicKey == "" {
		erroCliente(c, http.StatusNotFound, "Web Push não configurado no servidor")
		return
	}
	c.JSON(http.StatusOK, gin.H{"public_key": publicKey})
}

// SubscreverPush regista ou atualiza uma subscrição na base de dados.
func (h *Handler) SubscreverPush(c *gin.Context) {
	tenantID := middleware.GetTenantID(c)
	userID := middleware.GetUserID(c)

	if !h.Cfg.WebPushConfigured() {
		erroCliente(c, http.StatusServiceUnavailable, "Web Push não configurado")
		return
	}

	var req SubscreverPushRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		erroCliente(c, http.StatusBadRequest, "Dados de subscrição inválidos")
		return
	}

	var sub models.PushSubscription
	err := h.DB.Where("endpoint = ?", req.Endpoint).First(&sub).Error
	if err == gorm.ErrRecordNotFound {
		sub = models.PushSubscription{
			TenantID:  tenantID,
			UsuarioID: userID,
			Endpoint:  req.Endpoint,
			P256dh:    req.Keys.P256dh,
			Auth:      req.Keys.Auth,
		}
		if err := h.DB.Create(&sub).Error; err != nil {
			h.erroInterno(c, "gravar subscrição de push", err)
			return
		}
	} else if err == nil {
		// Atualizar os vínculos se já existia
		sub.TenantID = tenantID
		sub.UsuarioID = userID
		sub.P256dh = req.Keys.P256dh
		sub.Auth = req.Keys.Auth
		if err := h.DB.Save(&sub).Error; err != nil {
			h.erroInterno(c, "atualizar subscrição de push", err)
			return
		}
	} else {
		h.erroInterno(c, "procurar subscrição existente", err)
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

// CancelarPush remove a subscrição com base no endpoint.
func (h *Handler) CancelarPush(c *gin.Context) {
	var req CancelarPushRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		erroCliente(c, http.StatusBadRequest, "Dados inválidos")
		return
	}

	if err := h.DB.Where("endpoint = ?", req.Endpoint).Delete(&models.PushSubscription{}).Error; err != nil {
		h.erroInterno(c, "eliminar subscrição de push", err)
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

// EnviarPushNovaEncomenda procura todas as subscrições do restaurante e envia as notificações Web Push.
// Corre numa goroutine para não bloquear o fluxo da requisição HTTP do cliente.
func (h *Handler) EnviarPushNovaEncomenda(tenantID uint, pedido *models.Pedido) {
	slog.Info("EnviarPushNovaEncomenda: início", "tenant_id", tenantID, "pedido_id", pedido.ID)

	if !h.Cfg.WebPushConfigured() {
		slog.Warn("EnviarPushNovaEncomenda: Web Push não está configurado", "public_key_len", len(h.Cfg.VAPIDPublicKey), "private_key_len", len(h.Cfg.VAPIDPrivateKey))
		return
	}

	var subs []models.PushSubscription
	if err := h.DB.Where("tenant_id = ?", tenantID).Find(&subs).Error; err != nil {
		slog.Error("buscar subscrições de push para notificação", "tenant_id", tenantID, "erro", err)
		return
	}

	slog.Info("EnviarPushNovaEncomenda: subscrições encontradas", "tenant_id", tenantID, "quantidade", len(subs))

	if len(subs) == 0 {
		return
	}

	payload := map[string]interface{}{
		"title": "Nova Encomenda!",
		"body":  fmt.Sprintf("Encomenda #%d por %s\nTotal: %s", pedido.ID, pedido.ClienteNome, pedido.ValorTotalCents.String()),
		"tag":   fmt.Sprintf("encomenda_%d", pedido.ID),
		"data": map[string]interface{}{
			"url": "/admin",
		},
	}

	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		slog.Error("serializar payload do push", "erro", err)
		return
	}

	for _, sub := range subs {
		// Executa cada envio individualmente para isolar falhas de ligação de cada endpoint
		go func(s models.PushSubscription) {
			slog.Info("A enviar Web Push...", "usuario_id", s.UsuarioID, "endpoint", s.Endpoint)
			resp, err := webpush.SendNotification(payloadBytes, &webpush.Subscription{
				Endpoint: s.Endpoint,
				Keys: webpush.Keys{
					P256dh: s.P256dh,
					Auth:   s.Auth,
				},
			}, &webpush.Options{
				Subscriber:      "https://" + h.Cfg.MainDomain,
				VAPIDPublicKey:  h.Cfg.VAPIDPublicKey,
				VAPIDPrivateKey: h.Cfg.VAPIDPrivateKey,
				TTL:             86400, // 24 horas
			})

			if err != nil {
				slog.Warn("falha ao enviar Web Push (erro de transporte)", "usuario_id", s.UsuarioID, "erro", err)
				if resp != nil {
					resp.Body.Close()
				}
				return
			}
			defer resp.Body.Close()

			if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
				bodyBytes, _ := io.ReadAll(resp.Body)
				slog.Warn("falha no envio de Web Push (status HTTP)", 
					"usuario_id", s.UsuarioID, 
					"status_code", resp.StatusCode, 
					"resposta", string(bodyBytes))

				// Limpa automaticamente se o endpoint não existir mais no serviço de push
				if resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusGone {
					slog.Info("a remover subscrição Web Push inativa", "usuario_id", s.UsuarioID, "endpoint", s.Endpoint)
					h.DB.Delete(&s)
				}
				return
			}

			slog.Info("Web Push enviado com sucesso!", "usuario_id", s.UsuarioID, "status_code", resp.StatusCode)
		}(sub)
	}
}
