// Package testdb starts a throwaway PostgreSQL container for integration tests, migrated and with
// a login role that is a member of ce_app, the role the application runs as.
package testdb

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/GagaismoTuc/centro-estudiantes-tuc/api/internal/db"
)

// Env is one migrated database. Super bypasses RLS and is only for seeding and verifying;
// App connects as ce_app_test (member of ce_app) and is the pool under test.
type Env struct {
	SuperDSN string
	AppDSN   string
	Super    *pgxpool.Pool
	App      *pgxpool.Pool
}

// Start runs the container and returns the environment and a function that tears it down.
// The teardown is safe to call even when Start returned an error.
func Start(ctx context.Context) (*Env, func(), error) {
	env := &Env{}
	ctr, err := postgres.Run(ctx, "postgres:18-alpine",
		postgres.WithDatabase("ce"), postgres.WithUsername("postgres"), postgres.WithPassword("postgres"),
		postgres.BasicWaitStrategies())
	stop := func() {
		if env.App != nil {
			env.App.Close()
		}
		if env.Super != nil {
			env.Super.Close()
		}
		if ctr != nil {
			_ = ctr.Terminate(context.Background())
		}
	}
	if err != nil {
		return nil, stop, fmt.Errorf("start postgres container: %w", err)
	}
	if env.SuperDSN, err = ctr.ConnectionString(ctx, "sslmode=disable"); err != nil {
		return nil, stop, err
	}
	if err := db.Migrate(ctx, env.SuperDSN); err != nil {
		return nil, stop, fmt.Errorf("migrate: %w", err)
	}
	if env.Super, err = pgxpool.New(ctx, env.SuperDSN); err != nil {
		return nil, stop, err
	}
	if _, err := env.Super.Exec(ctx, `CREATE ROLE ce_app_test LOGIN PASSWORD 'ce_app_test' IN ROLE ce_app`); err != nil {
		return nil, stop, err
	}
	cfg, err := pgxpool.ParseConfig(env.SuperDSN)
	if err != nil {
		return nil, stop, err
	}
	c := cfg.ConnConfig
	env.AppDSN = fmt.Sprintf("postgres://ce_app_test:ce_app_test@%s:%d/%s?sslmode=disable", c.Host, c.Port, c.Database)
	if env.App, err = pgxpool.New(ctx, env.AppDSN); err != nil {
		return nil, stop, err
	}
	return env, stop, nil
}
