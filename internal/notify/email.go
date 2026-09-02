package notify

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/smtp"
	"strings"
	"time"
)

type emailSender struct {
	baseSender
	cfg EmailConfig
}

// buildMessage 组装 RFC822 邮件（HTML 正文）。
func buildMessage(from string, to, cc []string, subject, html string) []byte {
	var b strings.Builder
	b.WriteString("From: " + from + "\r\n")
	b.WriteString("To: " + strings.Join(to, ", ") + "\r\n")
	if len(cc) > 0 {
		b.WriteString("Cc: " + strings.Join(cc, ", ") + "\r\n")
	}
	b.WriteString("Subject: " + subject + "\r\n")
	b.WriteString("MIME-Version: 1.0\r\n")
	b.WriteString("Content-Type: text/html; charset=UTF-8\r\n")
	b.WriteString("Date: " + time.Now().Format(time.RFC1123Z) + "\r\n")
	b.WriteString("\r\n")
	b.WriteString(html)
	return []byte(b.String())
}

func (s *emailSender) send(ctx context.Context, subject, html string) error {
	body := buildMessage(s.cfg.From, s.cfg.To, s.cfg.Cc, subject, html)
	return s.deliver(ctx, body)
}

func (s *emailSender) Send(ctx context.Context, data TemplateData) error {
	subject := renderEmailSubject(data)
	html, err := renderEmailHTML(data)
	if err != nil {
		return err
	}
	return s.send(ctx, subject, html)
}

func (s *emailSender) SendDigest(ctx context.Context, items []TemplateData) error {
	subject := fmt.Sprintf("[lanwatch] 通知摘要（%d 条）", len(items))
	var b strings.Builder
	for i, it := range items {
		html, err := renderEmailHTML(it)
		if err != nil {
			return err
		}
		if i > 0 {
			b.WriteString("<hr>")
		}
		b.WriteString(html)
	}
	return s.send(ctx, subject, b.String())
}

// deliver 通过 SMTP 发送已组装好的邮件。
func (s *emailSender) deliver(_ context.Context, body []byte) error {
	addr := net.JoinHostPort(s.cfg.Host, s.cfg.Port)
	recipients := append(append([]string{}, s.cfg.To...), s.cfg.Cc...)
	if len(recipients) == 0 {
		return fmt.Errorf("email channel %d: no recipients", s.id)
	}

	var client *smtp.Client
	var err error
	switch s.cfg.TLS {
	case "ssl":
		conn, derr := tls.Dial("tcp", addr, &tls.Config{ServerName: s.cfg.Host})
		if derr != nil {
			return fmt.Errorf("tls dial: %w", derr)
		}
		client, err = smtp.NewClient(conn, s.cfg.Host)
	case "none":
		client, err = smtp.Dial(addr)
	default: // starttls
		client, err = smtp.Dial(addr)
		if err == nil {
			if terr := client.StartTLS(&tls.Config{ServerName: s.cfg.Host}); terr != nil {
				_ = client.Close()
				return fmt.Errorf("starttls: %w", terr)
			}
		}
	}
	if err != nil {
		return fmt.Errorf("smtp dial %s: %w", addr, err)
	}
	defer client.Close()

	if s.cfg.Username != "" {
		auth := smtp.PlainAuth("", s.cfg.Username, s.cfg.Password, s.cfg.Host)
		if err := client.Auth(auth); err != nil {
			return fmt.Errorf("smtp auth: %w", err)
		}
	}
	if err := client.Mail(s.cfg.From); err != nil {
		return fmt.Errorf("mail from: %w", err)
	}
	for _, rcpt := range recipients {
		if err := client.Rcpt(rcpt); err != nil {
			return fmt.Errorf("rcpt %s: %w", rcpt, err)
		}
	}
	w, err := client.Data()
	if err != nil {
		return fmt.Errorf("data: %w", err)
	}
	if _, err := w.Write(body); err != nil {
		return fmt.Errorf("write body: %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("close body: %w", err)
	}
	return client.Quit()
}
