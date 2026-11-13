// Command server runs the Centro de Estudiantes HTTP API.
//
// Environment:
//   - DATABASE_URL: application connection; must be a member of ce_app without SUPERUSER or BYPASSRLS.
//   - MIGRATE_DATABASE_URL: owner connection used only to apply migrations at startup.
//   - ACCESS_CODE_KEY: base64 HMAC key (at least 32 random bytes) for access codes; the server
//     refuses to start without it. Generate it with `openssl rand -base64 32`.
//   - TOTP_ENC_KEY: base64 of exactly 32 random bytes; AES-256-GCM key that encrypts TOTP secrets at
//     rest. The server refuses to start without it. Losing or changing it disables every enrolled
//     second factor (each user then needs `admin reset-mfa`).
//   - APP_BASE_URL: public origin of the web app used in emailed links (https://..., or
//     http://localhost:PORT in development).
//   - SMTP_HOST, SMTP_PORT (default 587), SMTP_USERNAME, SMTP_PASSWORD, MAIL_FROM: SMTP relay
//     (STARTTLS required). Without SMTP_HOST, MAIL_DEV_DIR makes the server write each email as an
//     .eml file there instead (development). With neither, the server refuses to start.
//   - TRUSTED_PROXIES: comma-separated CIDRs of the reverse proxies in front of the server (default
//     empty). Only when the TCP peer is in one of them is X-Forwarded-For used to find the client
//     address for rate limits.
//   - ADDR: listen address (default :8080).
//
// SIGINT or SIGTERM stops the server gracefully: in-flight requests get up to 15 s, then the session
// cleanup stops and queued mail gets up to 10 s before the process exits (25 s in total; give the
// container a stop_grace_period of 30 s).
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/GagaismoTuc/centro-estudiantes-tuc/api/internal/auth"
	"github.com/GagaismoTuc/centro-estudiantes-tuc/api/internal/db"
	"github.com/GagaismoTuc/centro-estudiantes-tuc/api/internal/httpapi"
	"github.com/GagaismoTuc/centro-estudiantes-tuc/api/internal/mail"
	"github.com/GagaismoTuc/centro-estudiantes-tuc/api/internal/roster"
	"github.com/GagaismoTuc/centro-estudiantes-tuc/api/internal/totp"
)

func main() {
	if err := run(os.Getenv); err != nil {
		slog.Error("server stopped", "err", err)
		os.Exit(1)
	}
}

// Shutdown budget: shutdownTimeout + drainTimeout (25 s) stays under the orchestrator's grace period
// (stop_grace_period: 30s) so the process is never SIGKILLed mid-drain.
const (
	shutdownTimeout = 15 * time.Second
	drainTimeout    = 10 * time.Second
	cleanupInterval = 10 * time.Minute
)

func run(getenv func(string) string) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	appDSN, migrateDSN := getenv("DATABASE_URL"), getenv("MIGRATE_DATABASE_URL")
	if appDSN == "" || migrateDSN == "" {
		return errors.New("DATABASE_URL and MIGRATE_DATABASE_URL must be set")
	}
	// Checked before touching the database: without it codes would be hashed with a guessable key.
	codeKey, err := roster.ParseKey(getenv("ACCESS_CODE_KEY"))
	if err != nil {
		return errors.New("ACCESS_CODE_KEY must be base64 of at least 32 random bytes (generate it with: openssl rand -base64 32)")
	}
	totpKey, err := totp.ParseKey(getenv("TOTP_ENC_KEY"))
	if err != nil {
		return errors.New("TOTP_ENC_KEY must be base64 of exactly 32 random bytes (generate it with: openssl rand -base64 32)")
	}
	sender, err := mailSender(getenv)
	if err != nil {
		return err
	}
	baseURL, err := parseBaseURL(getenv("APP_BASE_URL"))
	if err != nil {
		return err
	}
	trusted, err := parseTrustedProxies(getenv("TRUSTED_PROXIES"))
	if err != nil {
		return err
	}
	// Migrations need the owner role; the app role owns nothing (ADR 0001, tenant isolation rule 2).
	if err := db.Migrate(ctx, migrateDSN); err != nil {
		return err
	}
	pool, err := pgxpool.New(ctx, appDSN)
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	defer pool.Close()
	if err := db.CheckAppRole(ctx, pool); err != nil {
		return err
	}

	addr := getenv("ADDR")
	if addr == "" {
		addr = ":8080"
	}
	queue := mail.NewQueue(4, 256)
	srv := &http.Server{
		Handler: httpapi.NewRouter(httpapi.Deps{Pool: pool, AccessCodeKey: codeKey, TOTPKey: totpKey, Mail: sender,
			BaseURL: baseURL, Background: queue.Go, TrustedProxies: trusted}),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	slog.Info("listening", "addr", addr)
	store := auth.NewStore(pool, time.Now)
	return serve(ctx, srv, ln,
		func(bg context.Context) { store.CleanSessionsEvery(bg, cleanupInterval) },
		queue.Close)
}

// serve runs srv on ln and background (the session cleanup) until ctx is done. Then it stops
// accepting connections and waits up to shutdownTimeout for in-flight requests, stops background,
// and waits up to drainTimeout for drain (queued mail) before returning.
func serve(ctx context.Context, srv *http.Server, ln net.Listener, background func(context.Context),
	drain func(context.Context) error) error {
	bgCtx, stopBG := context.WithCancel(context.Background())
	bgDone := make(chan struct{})
	go func() { defer close(bgDone); background(bgCtx) }()
	served := make(chan error, 1)
	go func() { served <- srv.Serve(ln) }()

	var err error
	select {
	case err = <-served: // listener failure
	case <-ctx.Done():
		slog.Info("shutting down")
		sctx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		err = srv.Shutdown(sctx)
		cancel()
		if serr := <-served; !errors.Is(serr, http.ErrServerClosed) && err == nil {
			err = serr
		}
	}
	stopBG()
	<-bgDone
	dctx, cancel := context.WithTimeout(context.Background(), drainTimeout)
	defer cancel()
	if derr := drain(dctx); derr != nil {
		slog.Error("mail queue not drained", "err", derr)
	}
	return err
}

// parseTrustedProxies parses TRUSTED_PROXIES: comma-separated CIDRs in canonical form (host bits
// zero, e.g. 10.0.0.0/8 or 192.168.1.10/32). Empty means no trusted proxy.
func parseTrustedProxies(raw string) ([]netip.Prefix, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	var out []netip.Prefix
	for _, part := range strings.Split(raw, ",") {
		p, err := netip.ParsePrefix(strings.TrimSpace(part))
		if err != nil || p != p.Masked() {
			return nil, errors.New("TRUSTED_PROXIES must be a comma-separated list of CIDRs, e.g. 10.0.0.0/8,192.168.1.10/32")
		}
		// Client addresses are unmapped before the check, so a ::ffff: prefix would match nothing.
		if p.Addr().Is4In6() {
			return nil, errors.New("TRUSTED_PROXIES must list IPv4 proxies as plain IPv4 CIDRs, not IPv4-mapped IPv6 (::ffff:…)")
		}
		out = append(out, p)
	}
	return out, nil
}

// mailSender picks SMTP when SMTP_HOST is set, else the development FileSender when MAIL_DEV_DIR is set.
func mailSender(getenv func(string) string) (mail.Sender, error) {
	if host := getenv("SMTP_HOST"); host != "" {
		s := mail.SMTPSender{Host: host, Port: getenv("SMTP_PORT"), Username: getenv("SMTP_USERNAME"),
			Password: getenv("SMTP_PASSWORD"), From: getenv("MAIL_FROM")}
		if s.Username == "" || s.Password == "" || s.From == "" {
			return nil, errors.New("SMTP_USERNAME, SMTP_PASSWORD and MAIL_FROM must be set with SMTP_HOST")
		}
		if s.Port == "" {
			s.Port = "587"
		}
		return s, nil
	}
	if dir := getenv("MAIL_DEV_DIR"); dir != "" {
		from := getenv("MAIL_FROM")
		if from == "" {
			from = "Centro de Estudiantes <no-reply@localhost>"
		}
		slog.Warn("MAIL_DEV_DIR is set: emails are written to files, not sent", "dir", dir)
		return mail.FileSender{Dir: dir, From: from}, nil
	}
	return nil, errors.New("set SMTP_HOST (with SMTP_PORT, SMTP_USERNAME, SMTP_PASSWORD, MAIL_FROM) or, for development, MAIL_DEV_DIR")
}

// parseBaseURL accepts an https origin, or an http one on localhost / 127.0.0.1 for development, and
// returns it without a trailing slash.
func parseBaseURL(raw string) (string, error) {
	bad := errors.New("APP_BASE_URL must be an https:// origin (http://localhost is allowed for development)")
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.User != nil || (u.Path != "" && u.Path != "/") ||
		u.RawQuery != "" || u.Fragment != "" || u.Opaque != "" {
		return "", bad
	}
	local := u.Hostname() == "localhost" || u.Hostname() == "127.0.0.1"
	if u.Scheme != "https" && !(u.Scheme == "http" && local) {
		return "", bad
	}
	return u.Scheme + "://" + u.Host, nil
}
