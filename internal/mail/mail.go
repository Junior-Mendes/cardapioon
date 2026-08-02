// Package mail envia o email transaccional do produto.
//
// Existia zero envio de email no projecto, o que significava que um restaurante que
// perdesse a senha ficava permanentemente sem acesso, recuperável apenas por intervenção
// manual na base de dados.
package mail

import (
	"crypto/tls"
	"fmt"
	"log/slog"
	"mime"
	"net"
	"net/smtp"
	"strings"
	"time"
)

// Sender é a interface usada pelos handlers, para que os testes não enviem email real.
type Sender interface {
	Send(para, assunto, corpoTexto, corpoHTML string) error
}

// Config do transporte SMTP.
type Config struct {
	Host     string
	Port     string
	User     string
	Password string
	From     string
	FromName string
}

// Configurado indica se há transporte SMTP utilizável.
func (c Config) Configurado() bool {
	return c.Host != "" && c.From != ""
}

// SMTPSender envia por SMTP com STARTTLS.
type SMTPSender struct {
	cfg Config
}

func NewSMTPSender(cfg Config) *SMTPSender { return &SMTPSender{cfg: cfg} }

func (s *SMTPSender) Send(para, assunto, corpoTexto, corpoHTML string) error {
	if !s.cfg.Configurado() {
		return fmt.Errorf("SMTP não configurado")
	}
	if err := validarEndereco(para); err != nil {
		return err
	}

	de := s.cfg.From
	if s.cfg.FromName != "" {
		de = fmt.Sprintf("%s <%s>", s.cfg.FromName, s.cfg.From)
	}

	fronteira := fmt.Sprintf("limite_%d", time.Now().UnixNano())
	var msg strings.Builder
	fmt.Fprintf(&msg, "From: %s\r\n", de)
	fmt.Fprintf(&msg, "To: %s\r\n", para)
	fmt.Fprintf(&msg, "Subject: %s\r\n", codificarAssunto(assunto))
	fmt.Fprintf(&msg, "MIME-Version: 1.0\r\n")
	fmt.Fprintf(&msg, "Content-Type: multipart/alternative; boundary=%s\r\n\r\n", fronteira)
	fmt.Fprintf(&msg, "--%s\r\nContent-Type: text/plain; charset=UTF-8\r\n\r\n%s\r\n", fronteira, corpoTexto)
	fmt.Fprintf(&msg, "--%s\r\nContent-Type: text/html; charset=UTF-8\r\n\r\n%s\r\n", fronteira, corpoHTML)
	fmt.Fprintf(&msg, "--%s--\r\n", fronteira)

	endereco := net.JoinHostPort(s.cfg.Host, s.cfg.Port)

	var auth smtp.Auth
	if s.cfg.User != "" {
		auth = smtp.PlainAuth("", s.cfg.User, s.cfg.Password, s.cfg.Host)
	}

	// Porta 465 usa TLS implícito; as restantes usam STARTTLS.
	if s.cfg.Port == "465" {
		return s.enviarTLSImplicito(endereco, auth, para, msg.String())
	}
	return smtp.SendMail(endereco, auth, s.cfg.From, []string{para}, []byte(msg.String()))
}

func (s *SMTPSender) enviarTLSImplicito(endereco string, auth smtp.Auth, para, msg string) error {
	conn, err := tls.Dial("tcp", endereco, &tls.Config{ServerName: s.cfg.Host, MinVersion: tls.VersionTLS12})
	if err != nil {
		return fmt.Errorf("ligar por TLS a %s: %w", endereco, err)
	}
	defer conn.Close()

	cliente, err := smtp.NewClient(conn, s.cfg.Host)
	if err != nil {
		return fmt.Errorf("cliente SMTP: %w", err)
	}
	defer cliente.Quit()

	if auth != nil {
		if err := cliente.Auth(auth); err != nil {
			return fmt.Errorf("autenticação SMTP: %w", err)
		}
	}
	if err := cliente.Mail(s.cfg.From); err != nil {
		return err
	}
	if err := cliente.Rcpt(para); err != nil {
		return err
	}
	w, err := cliente.Data()
	if err != nil {
		return err
	}
	if _, err := w.Write([]byte(msg)); err != nil {
		return err
	}
	return w.Close()
}

// LogSender escreve o email no log em vez de o enviar.
//
// É o fallback quando não há SMTP configurado: em desenvolvimento permite copiar o link
// de reset do log, e em produção deixa registo de que a mensagem não saiu, em vez de
// falhar silenciosamente.
type LogSender struct{}

func (LogSender) Send(para, assunto, corpoTexto, _ string) error {
	slog.Warn("SMTP não configurado — email não enviado",
		"para", para, "assunto", assunto, "corpo", corpoTexto)
	return nil
}

// New devolve o Sender adequado à configuração disponível.
func New(cfg Config) Sender {
	if cfg.Configurado() {
		slog.Info("email transaccional activo", "host", cfg.Host, "from", cfg.From)
		return NewSMTPSender(cfg)
	}
	slog.Warn("SMTP não configurado: emails serão escritos no log. " +
		"O reset de senha não chega aos utilizadores até configurar SMTP_HOST e SMTP_FROM.")
	return LogSender{}
}

func validarEndereco(e string) error {
	// Rejeita injecção de cabeçalhos via newline no endereço.
	if strings.ContainsAny(e, "\r\n") {
		return fmt.Errorf("endereço de email inválido")
	}
	if !strings.Contains(e, "@") {
		return fmt.Errorf("endereço de email inválido")
	}
	return nil
}

// codificarAssunto aplica MIME encoded-word para que os acentos apareçam corretamente
// nos clientes de email. BEncoding é no-op para ASCII puro.
func codificarAssunto(s string) string {
	return mime.BEncoding.Encode("UTF-8", s)
}
