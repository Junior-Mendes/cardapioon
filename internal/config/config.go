package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	// Base de dados
	DBHost     string
	DBPort     string
	DBUser     string
	DBPassword string
	DBName     string

	// Servidor
	Port       string
	GinMode    string
	MainDomain string
	BaseURL    string

	// Autenticação
	JWTSecret  string
	AccessTTL  time.Duration
	RefreshTTL time.Duration

	// Email transaccional
	SMTPHost     string
	SMTPPort     string
	SMTPUser     string
	SMTPPassword string
	MailFrom     string
	MailFromName string

	// Infra-estrutura
	TraefikDynamicDir string
	BackendURL        string
	UploadDir         string
	// TraefikInternalAddr é o endereço TLS do Traefik na rede interna. Usado para
	// confirmar, sem depender de DNS externo, se o endereço de um restaurante já
	// responde e já tem certificado.
	TraefikInternalAddr string

	// Operação
	SeedDemoData   bool
	TrustedProxies []string
}

// DevMode indica ambiente de desenvolvimento. Controla CORS para localhost, HSTS e o
// nível de detalhe dos erros devolvidos ao cliente.
func (c *Config) DevMode() bool { return c.GinMode != "release" }

// LoadConfig lê o ambiente e valida o que é obrigatório.
//
// Falhar no arranque é preferível a servir tráfego com configuração insegura: sem
// JWT_SECRET, por exemplo, não há forma de assinar tokens.
func LoadConfig() (*Config, error) {
	cfg := &Config{
		DBHost:     getEnv("DB_HOST", "localhost"),
		DBPort:     getEnv("DB_PORT", "3306"),
		DBUser:     getEnv("DB_USER", ""),
		DBPassword: getEnv("DB_PASSWORD", ""),
		DBName:     getEnv("DB_NAME", "cardapio_online"),

		Port:       getEnv("PORT", "8081"),
		GinMode:    getEnv("GIN_MODE", "release"),
		MainDomain: strings.ToLower(getEnv("MAIN_DOMAIN", "")),

		JWTSecret:  getEnv("JWT_SECRET", ""),
		AccessTTL:  time.Duration(getEnvInt("JWT_TTL_MINUTES", 60)) * time.Minute,
		RefreshTTL: time.Duration(getEnvInt("REFRESH_TTL_HOURS", 720)) * time.Hour,

		SMTPHost:     getEnv("SMTP_HOST", ""),
		SMTPPort:     getEnv("SMTP_PORT", "587"),
		SMTPUser:     getEnv("SMTP_USER", ""),
		SMTPPassword: getEnv("SMTP_PASSWORD", ""),
		MailFrom:     getEnv("MAIL_FROM", ""),
		MailFromName: getEnv("MAIL_FROM_NAME", "Cardápio Online"),

		TraefikDynamicDir:   getEnv("TRAEFIK_DYNAMIC_DIR", "/traefik_dynamic"),
		BackendURL:          getEnv("BACKEND_URL", "http://cardapio_online_api:8081"),
		UploadDir:           getEnv("UPLOAD_DIR", "./static/uploads"),
		TraefikInternalAddr: getEnv("TRAEFIK_INTERNAL_ADDR", "cardapio_traefik:443"),

		SeedDemoData: getEnvBool("SEED_DEMO_DATA", false),
	}

	if p := getEnv("TRUSTED_PROXIES", ""); p != "" {
		for _, item := range strings.Split(p, ",") {
			if s := strings.TrimSpace(item); s != "" {
				cfg.TrustedProxies = append(cfg.TrustedProxies, s)
			}
		}
	}

	if cfg.MainDomain == "" {
		return nil, fmt.Errorf("MAIN_DOMAIN é obrigatório")
	}

	cfg.BaseURL = getEnv("BASE_URL", "https://"+cfg.MainDomain)

	if cfg.DBUser == "" {
		return nil, fmt.Errorf("DB_USER é obrigatório")
	}

	// O segredo do JWT não tem valor por omissão de propósito: um valor por omissão
	// acabaria em produção e tornaria todos os tokens forjáveis por quem leia o código.
	if strings.TrimSpace(cfg.JWTSecret) == "" {
		return nil, fmt.Errorf("JWT_SECRET é obrigatório. Gere um com: openssl rand -base64 48")
	}
	if len(cfg.JWTSecret) < 32 {
		return nil, fmt.Errorf(
			"JWT_SECRET tem %d bytes; use pelo menos 32. Gere com: openssl rand -base64 48",
			len(cfg.JWTSecret))
	}

	if cfg.SeedDemoData && !cfg.DevMode() {
		return nil, fmt.Errorf(
			"SEED_DEMO_DATA=true com GIN_MODE=release: os seeders criam restaurantes de " +
				"demonstração com senha conhecida e não devem correr em produção")
	}

	return cfg, nil
}

func getEnv(chave, omissao string) string {
	if v, existe := os.LookupEnv(chave); existe && strings.TrimSpace(v) != "" {
		return strings.TrimSpace(v)
	}
	return omissao
}

func getEnvInt(chave string, omissao int) int {
	if v := getEnv(chave, ""); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return omissao
}

func getEnvBool(chave string, omissao bool) bool {
	if v := getEnv(chave, ""); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			return b
		}
	}
	return omissao
}
