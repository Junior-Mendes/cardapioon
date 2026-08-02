package validate

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// slugRe define o único formato aceite: rótulo DNS em minúsculas, a começar e a acabar
// em alfanumérico, com hífenes pelo meio, entre 3 e 32 caracteres.
//
// Esta validação é a defesa contra dois problemas na geração dos ficheiros do Traefik:
// o slug entra num caminho de ficheiro (um "../" escreveria fora do directório) e no
// corpo YAML dentro de uma regra Host(`...`) (uma crase ou newline injectaria routers
// arbitrários no edge router, incluindo a captura do tráfego do domínio principal).
var slugRe = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{1,30}[a-z0-9])$`)

// slugsReservados protege nomes que colidiriam com infra-estrutura, com o próprio SaaS,
// ou que permitiriam a um tenant fazer-se passar por um serviço oficial.
var slugsReservados = map[string]bool{
	"www": true, "api": true, "admin": true, "app": true, "mail": true, "email": true,
	"smtp": true, "imap": true, "pop": true, "ftp": true, "ns": true, "ns1": true,
	"ns2": true, "dns": true, "mx": true, "static": true, "assets": true, "cdn": true,
	"traefik": true, "default": true, "router": true, "service": true, "proxy": true,
	"dashboard": true, "painel": true, "login": true, "conta": true, "suporte": true,
	"ajuda": true, "help": true, "docs": true, "blog": true, "loja": true, "pay": true,
	"pagamento": true, "pagamentos": true, "checkout": true, "billing": true,
	"faturacao": true, "facturacao": true, "webhook": true, "webhooks": true,
	"status": true, "health": true, "metrics": true, "test": true, "teste": true,
	"staging": true, "dev": true, "demo": true, "beta": true, "internal": true,
	"security": true, "seguranca": true, "abuse": true, "postmaster": true,
	"hostmaster": true, "webmaster": true, "root": true, "system": true,
	// Reservado pelo ACME para validação de certificados.
	"acme-challenge": true, "well-known": true,
}

var (
	ErrSlugFormato   = errors.New("o endereço deve ter entre 3 e 32 caracteres, apenas letras minúsculas, números e hífenes, começando e terminando por letra ou número")
	ErrSlugReservado = errors.New("este endereço está reservado; escolha outro")
)

// Slug normaliza e valida o slug de um tenant.
func Slug(raw string) (string, error) {
	s := strings.ToLower(strings.TrimSpace(raw))

	if !slugRe.MatchString(s) {
		return "", ErrSlugFormato
	}
	// Dois hífenes seguidos na 3ª/4ª posição é o prefixo de Punycode (xn--), que
	// permitiria construir um domínio homógrafo visualmente idêntico a outro.
	if strings.Contains(s, "--") {
		return "", ErrSlugFormato
	}
	if slugsReservados[s] {
		return "", ErrSlugReservado
	}
	return s, nil
}

// hostnameRe valida um nome de domínio completo, usado nos domínios personalizados.
var hostnameRe = regexp.MustCompile(`^(?:[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z]{2,63}$`)

var ErrDominioFormato = errors.New("domínio inválido: use o formato exemplo.pt ou www.exemplo.pt")

// Domain normaliza e valida um domínio personalizado.
func Domain(raw string) (string, error) {
	d := strings.ToLower(strings.TrimSpace(raw))
	d = strings.TrimSuffix(d, ".")        // FQDN com ponto final
	d = strings.TrimPrefix(d, "http://")  // colado do browser
	d = strings.TrimPrefix(d, "https://") //
	if i := strings.IndexAny(d, "/:?#"); i >= 0 {
		d = d[:i]
	}

	if d == "" {
		return "", nil // string vazia = remover o domínio
	}
	if len(d) > 253 {
		return "", ErrDominioFormato
	}
	if !hostnameRe.MatchString(d) {
		return "", ErrDominioFormato
	}
	return d, nil
}

// DomainNaoPodeSerDoSaaS impede que um tenant reclame o domínio principal da plataforma
// ou um subdomínio dele: isso desviaria a landing page ou o painel para dentro da conta
// desse tenant.
func DomainNaoPodeSerDoSaaS(domain, mainDomain string) error {
	if mainDomain == "" {
		return nil
	}
	if domain == mainDomain || strings.HasSuffix(domain, "."+mainDomain) {
		return fmt.Errorf("não é possível usar %s como domínio personalizado: pertence à plataforma", domain)
	}
	return nil
}
