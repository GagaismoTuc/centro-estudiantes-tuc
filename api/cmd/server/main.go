// Command server runs the Centro de Estudiantes HTTP API.
//
// Environment:
//   - DATABASE_URL: application connection; must be a member of ce_app without SUPERUSER or BYPASSRLS.
//   - MIGRATE_DATABASE_URL: owner connection used only to apply migrations at startup.
//   - ACCESS_CODE_KEY: base64 HMAC key (at least 32 random bytes) for access codes; the server
//     refuses to start without it. Generate it with `openssl rand -base64 32`.
//   - APP_BASE_URL: public origin of the web app used in emailed links (https://..., or
//     http://localhost:PORT in development).
//   - SMTP_HOST, SMTP_PORT (default 587), SMTP_USERNAME, SMTP_PASSWORD, MAIL_FROM: SMTP relay
//     (STARTTLS required). Without SMTP_HOST, MAIL_DEV_DIR makes the server write each email as an
//     .eml file there instead (development). With neither, the server refuses to start.
//   - ADDR: listen address (default :8080).
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/GagaismoTuc/centro-estudiantes-tuc/api/internal/db"
	"github.com/GagaismoTuc/centro-estudiantes-tuc/api/internal/httpapi"
	"github.com/GagaismoTuc/centro-estudiantes-tuc/api/internal/mail"
	"github.com/GagaismoTuc/centro-estudiantes-tuc/api/internal/roster"
)

func main() {
	if err := run(os.Getenv); err != nil {
		slog.Error("server stopped", "err", err)
		os.Exit(1)
	}
}

func run(getenv func(string) string) error {
	ctx := context.Background()
	appDSN, migrateDSN := getenv("DATABASE_URL"), getenv("MIGRATE_DATABASE_URL")
	if appDSN == "" || migrateDSN == "" {
		return errors.New("DATABASE_URL and MIGRATE_DATABASE_URL must be set")
	}
	// Checked before touching the database: without it codes would be hashed with a guessable key.
	codeKey, err := roster.ParseKey(getenv("ACCESS_CODE_KEY"))
	if err != nil {
		return errors.New("ACCESS_CODE_KEY must be base64 of at least 32 random bytes (generate it with: openssl rand -base64 32)")
	}
	sender, err := mailSender(getenv)
	if err != nil {
		return err
	}
	baseURL, err := parseBaseURL(getenv("APP_BASE_URL"))
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
	srv := &http.Server{
		Addr:              addr,
		Handler:           httpapi.NewRouter(httpapi.Deps{Pool: pool, AccessCodeKey: codeKey, Mail: sender, BaseURL: baseURL}),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	slog.Info("listening", "addr", addr)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
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
