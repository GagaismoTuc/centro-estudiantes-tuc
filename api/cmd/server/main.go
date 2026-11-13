// Command server runs the Centro de Estudiantes HTTP API.
//
// Environment:
//   - DATABASE_URL: application connection; must be a member of ce_app without SUPERUSER or BYPASSRLS.
//   - MIGRATE_DATABASE_URL: owner connection used only to apply migrations at startup.
//   - ACCESS_CODE_KEY: base64 HMAC key (at least 32 random bytes) for access codes; the server
//     refuses to start without it. Generate it with `openssl rand -base64 32`.
//   - ADDR: listen address (default :8080).
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/GagaismoTuc/centro-estudiantes-tuc/api/internal/db"
	"github.com/GagaismoTuc/centro-estudiantes-tuc/api/internal/httpapi"
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
		Handler:           httpapi.NewRouter(httpapi.Deps{Pool: pool, AccessCodeKey: codeKey}),
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
