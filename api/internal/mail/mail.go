// Package mail sends plain-text transactional email (verification, sign-in links, password reset)
// through a Sender: FileSender for development and SMTPSender for a real provider.
package mail

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"mime"
	"mime/quotedprintable"
	"net"
	netmail "net/mail"
	"net/smtp"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Message is one plain-text email to a single recipient.
type Message struct {
	To, Subject, Text string
}

// Sender delivers a Message.
type Sender interface {
	Send(ctx context.Context, m Message) error
}

// ErrInvalidMessage is returned for a recipient that is not a bare address or a header with a line break.
var ErrInvalidMessage = errors.New("mail: invalid recipient or header")

// build renders m as an RFC 5322 message with CRLF line endings.
func build(from string, m Message, now time.Time) ([]byte, error) {
	a, err := netmail.ParseAddress(m.To)
	if err != nil || a.Address != m.To || strings.ContainsAny(m.Subject, "\r\n") {
		return nil, ErrInvalidMessage
	}
	sender, err := netmail.ParseAddress(from)
	if err != nil {
		return nil, fmt.Errorf("mail: invalid from address: %w", err)
	}
	domain := sender.Address[strings.LastIndexByte(sender.Address, '@')+1:]
	var b bytes.Buffer
	fmt.Fprintf(&b, "From: %s\r\nTo: %s\r\nSubject: %s\r\nDate: %s\r\nMessage-ID: <%s@%s>\r\n",
		from, m.To, mime.QEncoding.Encode("utf-8", m.Subject), now.Format(time.RFC1123Z), rand.Text(), domain)
	b.WriteString("MIME-Version: 1.0\r\nContent-Type: text/plain; charset=utf-8\r\nContent-Transfer-Encoding: quoted-printable\r\n\r\n")
	qp := quotedprintable.NewWriter(&b)
	if _, err := qp.Write([]byte(strings.ReplaceAll(m.Text, "\n", "\r\n"))); err != nil {
		return nil, err
	}
	if err := qp.Close(); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

// FileSender writes every message as an .eml file into Dir instead of sending it. Development only:
// open the files with any mail client. It never logs the message.
type FileSender struct {
	Dir  string
	From string
}

// Send writes m to Dir/<time>-<random>.eml, readable only by the owner.
func (s FileSender) Send(_ context.Context, m Message) error {
	now := time.Now()
	raw, err := build(s.From, m, now)
	if err != nil {
		return err
	}
	name := filepath.Join(s.Dir, now.UTC().Format("20060102T150405.000000000")+"-"+rand.Text()[:8]+".eml")
	if err := os.WriteFile(name, raw, 0o600); err != nil {
		return fmt.Errorf("mail: write %s: %w", filepath.Base(name), err)
	}
	return nil
}

// SMTPSender submits messages to an SMTP relay (port 587 submission). It requires STARTTLS and
// refuses to authenticate or send over a plaintext connection, then authenticates with PLAIN.
type SMTPSender struct {
	Host, Port         string
	Username, Password string
	From               string
	// RootCAs verifies the server certificate; nil uses the system roots.
	RootCAs *x509.CertPool
}

// Send delivers m, honouring ctx for the whole conversation.
func (s SMTPSender) Send(ctx context.Context, m Message) error {
	raw, err := build(s.From, m, time.Now())
	if err != nil {
		return err
	}
	from, err := netmail.ParseAddress(s.From)
	if err != nil {
		return fmt.Errorf("mail: invalid from address: %w", err)
	}
	var d net.Dialer
	conn, err := d.DialContext(ctx, "tcp", net.JoinHostPort(s.Host, s.Port))
	if err != nil {
		return fmt.Errorf("mail: dial: %w", err)
	}
	defer conn.Close()
	if dl, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(dl)
	}
	c, err := smtp.NewClient(conn, s.Host)
	if err != nil {
		return fmt.Errorf("mail: smtp greeting: %w", err)
	}
	defer c.Close()
	if ok, _ := c.Extension("STARTTLS"); !ok {
		return errors.New("mail: SMTP server does not offer STARTTLS; refusing to send without TLS")
	}
	if err := c.StartTLS(&tls.Config{ServerName: s.Host, RootCAs: s.RootCAs, MinVersion: tls.VersionTLS12}); err != nil {
		return fmt.Errorf("mail: STARTTLS: %w", err)
	}
	// PlainAuth itself refuses to send credentials unless the connection is TLS.
	if err := c.Auth(smtp.PlainAuth("", s.Username, s.Password, s.Host)); err != nil {
		return fmt.Errorf("mail: auth: %w", err)
	}
	if err := c.Mail(from.Address); err != nil {
		return fmt.Errorf("mail: MAIL FROM: %w", err)
	}
	if err := c.Rcpt(m.To); err != nil {
		return fmt.Errorf("mail: RCPT TO: %w", err)
	}
	w, err := c.Data()
	if err != nil {
		return fmt.Errorf("mail: DATA: %w", err)
	}
	if _, err := w.Write(raw); err != nil {
		return fmt.Errorf("mail: write: %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("mail: DATA end: %w", err)
	}
	return c.Quit()
}
