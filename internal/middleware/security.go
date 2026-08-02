package middleware

import (
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// CORSConfig controla a política de origens permitidas.
type CORSConfig struct {
	MainDomain string
	// PermitirLocalhost deve ser true apenas em desenvolvimento.
	PermitirLocalhost bool
	DB                *gorm.DB
}

// CORS responde com uma allowlist em vez de "*".
//
// A versão anterior enviava Access-Control-Allow-Origin: * junto com
// Allow-Credentials: true — combinação que os browsers rejeitam, e que em qualquer caso
// autorizava qualquer site a chamar a API.
//
// A allowlist é dinâmica porque os domínios dos clientes mudam em runtime: aceitamos o
// domínio principal, qualquer subdomínio dele, e os domínios personalizados já
// verificados.
func CORS(cfg CORSConfig) gin.HandlerFunc {
	cache := novoCacheDominios(time.Minute)

	return func(c *gin.Context) {
		origin := c.GetHeader("Origin")

		if origin != "" && origemPermitida(cfg, cache, origin) {
			c.Header("Access-Control-Allow-Origin", origin)
			c.Header("Access-Control-Allow-Credentials", "true")
			c.Header("Vary", "Origin")
			c.Header("Access-Control-Allow-Headers",
				"Content-Type, Authorization, X-Requested-With, Idempotency-Key")
			c.Header("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
			c.Header("Access-Control-Max-Age", "600")
		}

		if c.Request.Method == http.MethodOptions {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}
		c.Next()
	}
}

func origemPermitida(cfg CORSConfig, cache *cacheDominios, origin string) bool {
	host := hostDeOrigem(origin)
	if host == "" {
		return false
	}
	if cfg.PermitirLocalhost && hostLocal(host) {
		return true
	}
	if cfg.MainDomain != "" {
		if host == cfg.MainDomain || strings.HasSuffix(host, "."+cfg.MainDomain) {
			return true
		}
	}
	return cache.contem(cfg.DB, host)
}

func hostDeOrigem(origin string) string {
	// Uma Origin é "esquema://host[:porta]"; qualquer outra coisa é rejeitada.
	i := strings.Index(origin, "://")
	if i < 0 {
		return ""
	}
	esquema := origin[:i]
	if esquema != "http" && esquema != "https" {
		return ""
	}
	resto := origin[i+3:]
	if strings.ContainsAny(resto, "/?#") {
		return ""
	}
	return HostSemPorta(resto)
}

// cacheDominios evita uma query por pedido para validar a Origin.
type cacheDominios struct {
	mu       sync.RWMutex
	dominios map[string]bool
	expira   time.Time
	ttl      time.Duration
}

func novoCacheDominios(ttl time.Duration) *cacheDominios {
	return &cacheDominios{dominios: map[string]bool{}, ttl: ttl}
}

func (c *cacheDominios) contem(db *gorm.DB, host string) bool {
	if db == nil {
		return false
	}

	c.mu.RLock()
	valido := time.Now().Before(c.expira)
	if valido {
		ok := c.dominios[host]
		c.mu.RUnlock()
		return ok
	}
	c.mu.RUnlock()

	c.mu.Lock()
	defer c.mu.Unlock()
	// Outra goroutine pode ter recarregado enquanto esperávamos pelo lock.
	if time.Now().Before(c.expira) {
		return c.dominios[host]
	}

	var dominios []string
	if err := db.Model(&struct{}{}).
		Table("tenants").
		Where("domain IS NOT NULL AND domain <> '' AND ativo = 1 AND domain_status = 'verified'").
		Pluck("domain", &dominios).Error; err != nil {
		// Em caso de erro mantemos o cache anterior e tentamos outra vez em breve,
		// em vez de negar tudo (o que deixaria os storefronts sem CORS).
		c.expira = time.Now().Add(5 * time.Second)
		return c.dominios[host]
	}

	novo := make(map[string]bool, len(dominios)*2)
	for _, d := range dominios {
		novo[d] = true
		novo["www."+d] = true
	}
	c.dominios = novo
	c.expira = time.Now().Add(c.ttl)
	return c.dominios[host]
}

// SecurityHeaders aplica os cabeçalhos de segurança do navegador.
//
// A CSP é a segunda linha de defesa contra o XSS armazenado: mesmo que um payload passe
// pelo escape do frontend, sem 'unsafe-inline' em script-src ele não executa.
func SecurityHeaders(devMode bool) gin.HandlerFunc {
	csp := strings.Join([]string{
		"default-src 'self'",
		"script-src 'self'",
		// Os estilos inline vêm de atributos style= no HTML existente; remover isso é
		// trabalho de frontend e está registado como dívida técnica.
		"style-src 'self' 'unsafe-inline'",
		"img-src 'self' data: https:",
		"font-src 'self' data:",
		"connect-src 'self'",
		"frame-ancestors 'none'",
		"base-uri 'self'",
		"form-action 'self'",
		"object-src 'none'",
	}, "; ")

	return func(c *gin.Context) {
		c.Header("X-Content-Type-Options", "nosniff")
		c.Header("X-Frame-Options", "DENY")
		c.Header("Referrer-Policy", "strict-origin-when-cross-origin")
		c.Header("Permissions-Policy", "geolocation=(self), camera=(), microphone=()")
		c.Header("Content-Security-Policy", csp)
		if !devMode {
			c.Header("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		}
		c.Next()
	}
}

// CacheControl define a política de cache por tipo de rota.
//
// A versão anterior aplicava no-store a *todas* as respostas, incluindo CSS, JS e imagens.
// Num storefront público isso é o oposto do desejado: degrada o LCP, aumenta a factura de
// tráfego e prejudica o SEO local, que é canal de aquisição gratuito.
func CacheControl() gin.HandlerFunc {
	return func(c *gin.Context) {
		caminho := c.Request.URL.Path

		switch {
		case strings.HasPrefix(caminho, "/api/"):
			// Respostas de API contêm dados por tenant e por utilizador: nunca cachear.
			c.Header("Cache-Control", "no-store, no-cache, must-revalidate, private")
			c.Header("Pragma", "no-cache")
		case strings.HasPrefix(caminho, "/static/uploads/"):
			// Imagens de produtos: imutáveis na prática, servidas por URL única.
			c.Header("Cache-Control", "public, max-age=604800")
		case strings.HasPrefix(caminho, "/static/"):
			// Assets da aplicação. max-age curto com revalidação até existir hash no
			// nome dos ficheiros (registado como dívida técnica).
			c.Header("Cache-Control", "public, max-age=300, must-revalidate")
		default:
			// Documentos HTML: revalidar sempre para que um deploy seja visível de imediato.
			c.Header("Cache-Control", "no-cache")
		}
		c.Next()
	}
}

// --- Rate limiting ---

// RateLimiter é um token bucket por chave, em memória.
//
// Suficiente para uma única instância. Ao passar a várias réplicas isto tem de migrar
// para um contador partilhado (Redis), caso contrário o limite efectivo multiplica-se
// pelo número de instâncias.
type RateLimiter struct {
	mu      sync.Mutex
	buckets map[string]*bucket
	rate    float64 // tokens por segundo
	burst   float64
	ttl     time.Duration
}

type bucket struct {
	tokens    float64
	ultimaVez time.Time
}

func NewRateLimiter(porMinuto int, burst int) *RateLimiter {
	if porMinuto <= 0 {
		porMinuto = 60
	}
	if burst <= 0 {
		burst = porMinuto
	}
	rl := &RateLimiter{
		buckets: make(map[string]*bucket),
		rate:    float64(porMinuto) / 60.0,
		burst:   float64(burst),
		ttl:     10 * time.Minute,
	}
	go rl.limpezaPeriodica()
	return rl
}

// permitir consome um token e indica se o pedido pode prosseguir.
func (rl *RateLimiter) permitir(chave string) (bool, time.Duration) {
	agora := time.Now()

	rl.mu.Lock()
	defer rl.mu.Unlock()

	b, existe := rl.buckets[chave]
	if !existe {
		rl.buckets[chave] = &bucket{tokens: rl.burst - 1, ultimaVez: agora}
		return true, 0
	}

	// Reposição proporcional ao tempo decorrido.
	decorrido := agora.Sub(b.ultimaVez).Seconds()
	b.tokens = min(rl.burst, b.tokens+decorrido*rl.rate)
	b.ultimaVez = agora

	if b.tokens < 1 {
		faltam := (1 - b.tokens) / rl.rate
		return false, time.Duration(faltam * float64(time.Second))
	}
	b.tokens--
	return true, 0
}

func (rl *RateLimiter) limpezaPeriodica() {
	t := time.NewTicker(5 * time.Minute)
	defer t.Stop()
	for range t.C {
		limite := time.Now().Add(-rl.ttl)
		rl.mu.Lock()
		for k, b := range rl.buckets {
			if b.ultimaVez.Before(limite) {
				delete(rl.buckets, k)
			}
		}
		rl.mu.Unlock()
	}
}

// Limit devolve um middleware que limita por IP.
//
// A rota de login não tinha qualquer limite, o que permitia força-bruta de senhas e
// registo automatizado de tenants em massa.
func (rl *RateLimiter) Limit() gin.HandlerFunc {
	return func(c *gin.Context) {
		if ok, espera := rl.permitir(c.ClientIP()); !ok {
			c.Header("Retry-After", fmt.Sprintf("%d", int(espera.Seconds())+1))
			abortJSON(c, http.StatusTooManyRequests,
				"Demasiadas tentativas. Aguarde um momento e tente novamente.")
			return
		}
		c.Next()
	}
}

// LimitBy limita por uma chave derivada do pedido (por exemplo IP + email), para que a
// força-bruta a uma conta específica não seja diluída por rotação de IPs.
func (rl *RateLimiter) LimitBy(chave func(*gin.Context) string) gin.HandlerFunc {
	return func(c *gin.Context) {
		if ok, espera := rl.permitir(chave(c)); !ok {
			c.Header("Retry-After", fmt.Sprintf("%d", int(espera.Seconds())+1))
			abortJSON(c, http.StatusTooManyRequests,
				"Demasiadas tentativas. Aguarde um momento e tente novamente.")
			return
		}
		c.Next()
	}
}

func min(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}
