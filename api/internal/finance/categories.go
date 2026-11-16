package finance

import (
	"context"
	"errors"
	"strings"

	"github.com/GagaismoTuc/centro-estudiantes-tuc/api/internal/auth"
	"github.com/GagaismoTuc/centro-estudiantes-tuc/api/internal/db"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

var (
	ErrCategoryName     = errors.New("finance: name required")
	ErrCategoryExists   = errors.New("finance: category exists")
	ErrCategoryInUse    = errors.New("finance: category in use")
	ErrReservedCategory = errors.New("finance: category reserved")
)

func (s *Store) withCategoryWriter(ctx context.Context, sess auth.Session, fn func(pgx.Tx) error) error {
	if sess.TenantID == "" || sess.UserID == "" {
		return ErrForbidden
	}
	return db.WithTenant(ctx, s.pool, sess.TenantID, func(tx pgx.Tx) error {
		var officer bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM memberships WHERE tenant_id=$1 AND user_id=$2 AND role='officer')`, sess.TenantID, sess.UserID).Scan(&officer); err != nil {
			return err
		}
		if !officer {
			return ErrForbidden
		}
		if err := auth.RequireMFA(sess); err != nil {
			return err
		}
		return fn(tx)
	})
}
func (s *Store) AuthorizeCategoryWrite(ctx context.Context, sess auth.Session) error {
	return s.withCategoryWriter(ctx, sess, func(pgx.Tx) error { return nil })
}
func categoryConflict(err error) error {
	var e *pgconn.PgError
	if errors.As(err, &e) && e.Code == "23505" {
		return ErrCategoryExists
	}
	return err
}
func (s *Store) AddCategory(ctx context.Context, sess auth.Session, name, direction string) (out Category, err error) {
	err = s.withCategoryWriter(ctx, sess, func(tx pgx.Tx) error {
		name = strings.TrimSpace(name)
		if name == "" {
			return ErrCategoryName
		}
		if direction != "income" && direction != "expense" {
			return ErrCategory
		}
		return categoryConflict(tx.QueryRow(ctx, `INSERT INTO finance_categories(tenant_id,name,direction) VALUES($1,$2,$3) RETURNING id::text,direction,name,active,reserved`, sess.TenantID, name, direction).Scan(&out.ID, &out.Direction, &out.Name, &out.Active, &out.Reserved))
	})
	return
}
func (s *Store) ChangeCategory(ctx context.Context, sess auth.Session, id, operation, name string) (out Category, err error) {
	err = s.withCategoryWriter(ctx, sess, func(tx pgx.Tx) error {
		if !uuidPattern.MatchString(id) {
			return ErrNotFound
		}
		e := tx.QueryRow(ctx, `SELECT id::text,direction,name,active,reserved FROM finance_categories WHERE tenant_id=$1 AND id=$2 FOR UPDATE`, sess.TenantID, id).Scan(&out.ID, &out.Direction, &out.Name, &out.Active, &out.Reserved)
		if errors.Is(e, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if e != nil {
			return e
		}
		if out.Reserved {
			return ErrReservedCategory
		}
		switch operation {
		case "rename":
			name = strings.TrimSpace(name)
			if name == "" {
				return ErrCategoryName
			}
			e = categoryConflict(tx.QueryRow(ctx, `UPDATE finance_categories SET name=$3 WHERE tenant_id=$1 AND id=$2 RETURNING name`, sess.TenantID, id, name).Scan(&out.Name))
		case "archive":
			_, e = tx.Exec(ctx, `UPDATE finance_categories SET active=false WHERE tenant_id=$1 AND id=$2`, sess.TenantID, id)
			out.Active = false
		case "delete":
			var used bool
			if e = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM finance_entries WHERE tenant_id=$1 AND category_id=$2)`, sess.TenantID, id).Scan(&used); e != nil {
				return e
			}
			if used {
				return ErrCategoryInUse
			}
			_, e = tx.Exec(ctx, `DELETE FROM finance_categories WHERE tenant_id=$1 AND id=$2`, sess.TenantID, id)
		}
		return e
	})
	return
}
