package mail_test

import (
	"bufio"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"io"
	"mime"
	"mime/quotedprintable"
	"net"
	"net/http/httptest"
	stdmail "net/mail"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/GagaismoTuc/centro-estudiantes-tuc/api/internal/mail"
)

var msg = mail.Message{To: "ana@example.com", Subject: "Confirmá tu correo", Text: "Hola:\n\nabrí https://ce.example/verificar#token=abc\n"}

// wantMessage parses raw as RFC 5322 and checks headers and decoded body.
func wantMessage(t *testing.T, raw []byte, from string) {
	t.Helper()
	m, err := stdmail.ReadMessage(strings.NewReader(string(raw)))
	if err != nil {
		t.Fatalf("parse message: %v\n%s", err, raw)
	}
	subject, err := new(mime.WordDecoder).DecodeHeader(m.Header.Get("Subject"))
	if err != nil {
		t.Fatal(err)
	}
	for k, want := range map[string]string{
		"From": from, "To": msg.To, "MIME-Version": "1.0",
		"Content-Type": "text/plain; charset=utf-8", "Content-Transfer-Encoding": "quoted-printable",
	} {
		if got := m.Header.Get(k); got != want {
			t.Errorf("%s = %q, want %q", k, got, want)
		}
	}
	if subject != msg.Subject {
		t.Errorf("Subject = %q, want %q", subject, msg.Subject)
	}
	if m.Header.Get("Date") == "" || m.Header.Get("Message-ID") == "" {
		t.Errorf("Date or Message-ID missing: %v", m.Header)
	}
	body, err := io.ReadAll(quotedprintable.NewReader(m.Body))
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.ReplaceAll(string(body), "\r\n", "\n"); got != msg.Text {
		t.Errorf("body = %q, want %q", got, msg.Text)
	}
}

func TestFileSenderWritesOneEmlPerMessage(t *testing.T) {
	dir := t.TempDir()
	s := mail.FileSender{Dir: dir, From: "CE <no-reply@ce.example>"}
	for range 2 {
		if err := s.Send(context.Background(), msg); err != nil {
			t.Fatalf("Send: %v", err)
		}
	}
	files, _ := filepath.Glob(filepath.Join(dir, "*.eml"))
	if len(files) != 2 {
		t.Fatalf("files = %v, want 2 .eml files", files)
	}
	raw, err := os.ReadFile(files[0])
	if err != nil {
		t.Fatal(err)
	}
	wantMessage(t, raw, "CE <no-reply@ce.example>")
}

func TestSendersRejectHeaderInjection(t *testing.T) {
	dir := t.TempDir()
	s := mail.FileSender{Dir: dir, From: "no-reply@ce.example"}
	for _, m := range []mail.Message{
		{To: "ana@example.com\r\nBcc: x@evil.example", Subject: "s", Text: "t"},
		{To: "Ana <ana@example.com>", Subject: "s", Text: "t"},
		{To: "ana@example.com", Subject: "s\r\nBcc: x@evil.example", Text: "t"},
	} {
		if err := s.Send(context.Background(), m); err == nil {
			t.Errorf("Send(%q) = nil, want error", m.To+"|"+m.Subject)
		}
	}
	if files, _ := filepath.Glob(filepath.Join(dir, "*")); len(files) != 0 {
		t.Fatalf("files = %v, want none", files)
	}
}

// fakeSMTP is a minimal SMTP server. With tlsConf it offers STARTTLS; it records every command
// and the DATA payload.
type fakeSMTP struct {
	ln      net.Listener
	tlsConf *tls.Config
	mu      sync.Mutex
	cmds    []string
	data    string
	done    chan struct{}
}

func startFakeSMTP(t *testing.T, tlsConf *tls.Config) *fakeSMTP {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeSMTP{ln: ln, tlsConf: tlsConf, done: make(chan struct{})}
	t.Cleanup(func() { ln.Close() })
	go f.serve()
	return f
}

func (f *fakeSMTP) port() string { _, p, _ := net.SplitHostPort(f.ln.Addr().String()); return p }

func (f *fakeSMTP) serve() {
	defer close(f.done)
	conn, err := f.ln.Accept()
	if err != nil {
		return
	}
	defer conn.Close()
	rw := bufio.NewReadWriter(bufio.NewReader(conn), bufio.NewWriter(conn))
	reply := func(s string) { rw.WriteString(s + "\r\n"); rw.Flush() }
	reply("220 fake ESMTP")
	secure := false
	for {
		line, err := rw.ReadString('\n')
		if err != nil {
			return
		}
		line = strings.TrimRight(line, "\r\n")
		f.mu.Lock()
		f.cmds = append(f.cmds, line)
		f.mu.Unlock()
		verb := strings.ToUpper(strings.SplitN(line, " ", 2)[0])
		switch {
		case verb == "EHLO" && f.tlsConf != nil && !secure:
			reply("250-fake\r\n250 STARTTLS")
		case verb == "EHLO":
			reply("250-fake\r\n250 AUTH PLAIN")
		case verb == "STARTTLS":
			reply("220 go ahead")
			tc := tls.Server(conn, f.tlsConf)
			if tc.Handshake() != nil {
				return
			}
			conn, secure = tc, true
			rw = bufio.NewReadWriter(bufio.NewReader(tc), bufio.NewWriter(tc))
		case verb == "AUTH":
			reply("235 ok")
		case verb == "DATA":
			reply("354 go")
			var b strings.Builder
			for {
				l, err := rw.ReadString('\n')
				if err != nil || l == ".\r\n" {
					break
				}
				b.WriteString(l)
			}
			f.mu.Lock()
			f.data = b.String()
			f.mu.Unlock()
			reply("250 queued")
		case verb == "QUIT":
			reply("221 bye")
			return
		default:
			reply("250 ok")
		}
	}
}

func TestSMTPSenderRefusesServerWithoutSTARTTLS(t *testing.T) {
	srv := startFakeSMTP(t, nil)
	s := mail.SMTPSender{Host: "127.0.0.1", Port: srv.port(), Username: "u", Password: "p", From: "no-reply@ce.example"}
	err := s.Send(context.Background(), msg)
	if err == nil || !strings.Contains(err.Error(), "STARTTLS") {
		t.Fatalf("Send = %v, want an error about STARTTLS", err)
	}
	srv.ln.Close()
	<-srv.done
	for _, c := range srv.cmds {
		if v := strings.ToUpper(strings.SplitN(c, " ", 2)[0]); v == "AUTH" || v == "MAIL" || v == "DATA" {
			t.Fatalf("command %q sent in plaintext; commands %q", c, srv.cmds)
		}
	}
}

func TestSMTPSenderDeliversOverSTARTTLSWithPlainAuth(t *testing.T) {
	https := httptest.NewTLSServer(nil) // only for its self-signed certificate (valid for 127.0.0.1)
	defer https.Close()
	roots := x509.NewCertPool()
	roots.AddCert(https.Certificate())
	srv := startFakeSMTP(t, https.TLS)
	s := mail.SMTPSender{Host: "127.0.0.1", Port: srv.port(), Username: "user", Password: "secret",
		From: "no-reply@ce.example", RootCAs: roots}

	if err := s.Send(context.Background(), msg); err != nil {
		t.Fatalf("Send: %v", err)
	}
	<-srv.done
	want := []string{"STARTTLS", "AUTH PLAIN " + base64.StdEncoding.EncodeToString([]byte("\x00user\x00secret")),
		"MAIL FROM:<no-reply@ce.example>", "RCPT TO:<ana@example.com>", "DATA", "QUIT"}
	var got []string
	for _, c := range srv.cmds {
		if !strings.HasPrefix(c, "EHLO") {
			got = append(got, strings.SplitN(c, " BODY=", 2)[0])
		}
	}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("commands = %q, want %q", got, want)
	}
	wantMessage(t, []byte(srv.data), "no-reply@ce.example")
}
