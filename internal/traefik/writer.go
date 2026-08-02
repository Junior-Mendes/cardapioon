// Package traefik gera a configuração dinâmica consumida pelo File Provider do Traefik.
//
// Duas diferenças em relação à versão anterior, que construía o YAML com fmt.Sprintf:
//
//  1. O YAML é serializado por gopkg.in/yaml.v3, não interpolado. Qualquer valor com
//     crases, newlines ou aspas é escapado pelo serializador em vez de se tornar sintaxe.
//  2. A escrita é atómica (ficheiro temporário no mesmo directório + rename). O Traefik
//     observa o directório com watch=true e lia ficheiros a meio de escrita, o que
//     produzia erros de parse intermitentes e rotas em falta.
package traefik

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"
)

// Writer serializa ficheiros de rota, um por tenant.
type Writer struct {
	dir        string
	mainDomain string
	backendURL string
	// mu serializa as escritas: várias goroutines a gravar o mesmo ficheiro
	// produziriam conteúdo intercalado.
	mu sync.Mutex
}

func NewWriter(dir, mainDomain, backendURL string) *Writer {
	if backendURL == "" {
		backendURL = "http://cardapio_online_api:8081"
	}
	return &Writer{dir: dir, mainDomain: mainDomain, backendURL: backendURL}
}

// dynamicConfig espelha o subconjunto do schema de configuração dinâmica do Traefik
// que usamos.
type dynamicConfig struct {
	HTTP httpConfig `yaml:"http"`
}

type httpConfig struct {
	Routers  map[string]router  `yaml:"routers"`
	Services map[string]service `yaml:"services"`
}

type router struct {
	Rule        string   `yaml:"rule"`
	EntryPoints []string `yaml:"entryPoints"`
	Service     string   `yaml:"service"`
	TLS         *tlsConf `yaml:"tls,omitempty"`
}

type tlsConf struct {
	CertResolver string `yaml:"certResolver"`
}

type service struct {
	LoadBalancer loadBalancer `yaml:"loadBalancer"`
}

type loadBalancer struct {
	Servers []serverURL `yaml:"servers"`
}

type serverURL struct {
	URL string `yaml:"url"`
}

// TenantRoute descreve o que uma rota de tenant precisa de saber.
//
// CustomDomain só deve ser preenchido quando a propriedade do domínio foi provada:
// gerar a rota antes disso faria o Traefik pedir um certificado Let's Encrypt para um
// domínio de terceiros e encaminhar o respectivo tráfego.
type TenantRoute struct {
	Slug         string
	CustomDomain string
}

// WriteTenant grava (ou regrava) o ficheiro de rota de um tenant.
func (w *Writer) WriteTenant(t TenantRoute) error {
	if t.Slug == "" {
		return fmt.Errorf("slug vazio")
	}
	// Defesa em profundidade: o slug já foi validado por validate.Slug antes de chegar
	// aqui, mas esta função escreve num caminho de ficheiro e não deve confiar nisso.
	if err := safeFileComponent(t.Slug); err != nil {
		return fmt.Errorf("slug inseguro %q: %w", t.Slug, err)
	}

	hosts := []string{
		fmt.Sprintf("%s.%s", t.Slug, w.mainDomain),
		fmt.Sprintf("www.%s.%s", t.Slug, w.mainDomain),
	}
	if t.CustomDomain != "" {
		if err := safeHost(t.CustomDomain); err != nil {
			return fmt.Errorf("domínio inseguro %q: %w", t.CustomDomain, err)
		}
		hosts = append(hosts, t.CustomDomain)
		// O www do domínio próprio só é acrescentado se o cliente não o tiver já indicado.
		if !strings.HasPrefix(t.CustomDomain, "www.") {
			hosts = append(hosts, "www."+t.CustomDomain)
		}
	}

	name := "tenant-" + t.Slug
	cfg := dynamicConfig{
		HTTP: httpConfig{
			Routers: map[string]router{
				name: {
					Rule:        hostRule(hosts),
					EntryPoints: []string{"websecure"},
					Service:     name,
					TLS:         &tlsConf{CertResolver: "myresolver"},
				},
			},
			Services: map[string]service{
				name: {LoadBalancer: loadBalancer{Servers: []serverURL{{URL: w.backendURL}}}},
			},
		},
	}

	return w.writeFile(t.Slug+".yml", cfg)
}

// WriteDefault grava a rota do domínio principal do SaaS (landing page e painel).
func (w *Writer) WriteDefault() error {
	hosts := []string{w.mainDomain, "www." + w.mainDomain}

	cfg := dynamicConfig{
		HTTP: httpConfig{
			Routers: map[string]router{
				"main": {
					Rule:        hostRule(hosts),
					EntryPoints: []string{"websecure"},
					Service:     "main",
					TLS:         &tlsConf{CertResolver: "myresolver"},
				},
			},
			Services: map[string]service{
				"main": {LoadBalancer: loadBalancer{Servers: []serverURL{{URL: w.backendURL}}}},
			},
		},
	}
	return w.writeFile("default.yml", cfg)
}

// DeleteTenant remove o ficheiro de rota de um tenant.
func (w *Writer) DeleteTenant(slug string) error {
	if err := safeFileComponent(slug); err != nil {
		return fmt.Errorf("slug inseguro %q: %w", slug, err)
	}
	w.mu.Lock()
	defer w.mu.Unlock()

	err := os.Remove(filepath.Join(w.dir, slug+".yml"))
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// Prune remove ficheiros de rota que não correspondam a nenhum tenant activo.
// Sem isto, um tenant desactivado continuava a servir o cardápio indefinidamente.
func (w *Writer) Prune(slugsActivos []string) error {
	activos := make(map[string]bool, len(slugsActivos))
	for _, s := range slugsActivos {
		activos[s+".yml"] = true
	}
	activos["default.yml"] = true

	entries, err := os.ReadDir(w.dir)
	if err != nil {
		return err
	}

	var erros []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".yml") || activos[e.Name()] {
			continue
		}
		if err := os.Remove(filepath.Join(w.dir, e.Name())); err != nil {
			erros = append(erros, fmt.Sprintf("%s: %v", e.Name(), err))
		}
	}
	if len(erros) > 0 {
		return fmt.Errorf("remover rotas órfãs: %s", strings.Join(erros, "; "))
	}
	return nil
}

// writeFile serializa a configuração e grava-a atomicamente.
func (w *Writer) writeFile(nome string, cfg dynamicConfig) error {
	dados, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("serializar YAML: %w", err)
	}

	w.mu.Lock()
	defer w.mu.Unlock()

	if err := os.MkdirAll(w.dir, 0o755); err != nil {
		return fmt.Errorf("criar directório %s: %w", w.dir, err)
	}

	destino := filepath.Join(w.dir, nome)

	// O ficheiro temporário tem de ficar no mesmo sistema de ficheiros do destino,
	// caso contrário o rename não é atómico. Prefixo com ponto para que o watch do
	// Traefik o ignore enquanto está a ser escrito.
	tmp, err := os.CreateTemp(w.dir, ".tmp-"+nome+"-*")
	if err != nil {
		return fmt.Errorf("criar ficheiro temporário: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op se o rename tiver sucedido

	if _, err := tmp.Write(dados); err != nil {
		tmp.Close()
		return fmt.Errorf("escrever ficheiro temporário: %w", err)
	}
	// Sync antes do rename: sem isto, uma falha de energia pode deixar um ficheiro
	// com o nome final e conteúdo vazio.
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("sync: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("fechar ficheiro temporário: %w", err)
	}
	if err := os.Chmod(tmpName, 0o644); err != nil {
		return fmt.Errorf("chmod: %w", err)
	}
	if err := os.Rename(tmpName, destino); err != nil {
		return fmt.Errorf("rename para %s: %w", destino, err)
	}
	return nil
}

// hostRule constrói a regra Host(...) do Traefik a partir de hosts já validados.
func hostRule(hosts []string) string {
	// Ordem estável para que reescritas com o mesmo conteúdo não alterem o ficheiro.
	uniq := make(map[string]bool, len(hosts))
	var lista []string
	for _, h := range hosts {
		if h == "" || uniq[h] {
			continue
		}
		uniq[h] = true
		lista = append(lista, h)
	}
	sort.Strings(lista)

	partes := make([]string, 0, len(lista))
	for _, h := range lista {
		partes = append(partes, fmt.Sprintf("Host(`%s`)", h))
	}
	return strings.Join(partes, " || ")
}

// safeFileComponent garante que o valor não escapa do directório nem contém caracteres
// que o tornem sintaxe em YAML.
func safeFileComponent(s string) error {
	if s == "" {
		return fmt.Errorf("vazio")
	}
	if s != filepath.Base(s) || s == "." || s == ".." {
		return fmt.Errorf("contém componentes de caminho")
	}
	if strings.ContainsAny(s, `/\:`+"`"+"\n\r\t\"'$&|;<>* ") {
		return fmt.Errorf("contém caracteres não permitidos")
	}
	return nil
}

func safeHost(h string) error {
	if err := safeFileComponent(h); err != nil {
		// Um host contém pontos, o que safeFileComponent aceita; só rejeitamos os
		// caracteres perigosos.
		return err
	}
	return nil
}
