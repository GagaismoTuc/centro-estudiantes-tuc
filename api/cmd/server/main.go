// Command server runs the Centro de Estudiantes HTTP API.
//
// Environment:
//   - DATABASE_URL: application connection; must be a member of ce_app without SUPERUSER or BYPASSRLS.
//   - MIGRATE_DATABASE_URL: owner connection used only to apply migrations at startup.
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
)

func main() {
	if err := run(); err != nil {
		slog.Error("server stopped", "err", err)
		os.Exit(1)
	}
}

func run() error {
	ctx := context.Background()
	appDSN, migrateDSN := os.Getenv("DATABASE_URL"), os.Getenv("MIGRATE_DATABASE_URL")
	if appDSN == "" || migrateDSN == "" {
		return errors.New("DATABASE_URL and MIGRATE_DATABASE_URL must be set")
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

	addr := os.Getenv("ADDR")
	if addr == "" {
		addr = ":8080"
	}
	srv := &http.Server{
		Addr:              addr,
		Handler:           httpapi.NewRouter(httpapi.Deps{Pool: pool}),
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
