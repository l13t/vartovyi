package store

import (
	"context"
	"errors"
	"strings"

	"vartovyi/internal/model"
)

var ErrDeleteSelf = errors.New("cannot delete current account")
var ErrLastAdmin = errors.New("cannot delete last administrator")
var ErrInvalidSelection = errors.New("invalid user selection")
var ErrForbidden = errors.New("administrator required")

// DeleteUsers is atomic. It serializes with user role changes, revokes sessions
// via the foreign key, and never touches recordings or camera configuration.
func (s *Store) DeleteUsers(ctx context.Context, actor string, ids []string) (int, error) {
	if len(ids) == 0 || len(ids) > 1000 {
		return 0, ErrInvalidSelection
	}
	seen := map[string]bool{}
	for _, id := range ids {
		if strings.TrimSpace(id) == "" || len(id) > 200 || seen[id] {
			return 0, ErrInvalidSelection
		}
		if id == actor {
			return 0, ErrDeleteSelf
		}
		seen[id] = true
	}
	tx, e := s.Pool.Begin(ctx)
	if e != nil {
		return 0, e
	}
	defer tx.Rollback(ctx)
	if _, e = tx.Exec(ctx, "SELECT pg_advisory_xact_lock(712039442)"); e != nil {
		return 0, e
	}
	var role model.Role
	if e = tx.QueryRow(ctx, "SELECT role FROM api_users WHERE id=$1", actor).Scan(&role); e != nil || role != model.Admin {
		return 0, ErrForbidden
	}
	var count int
	if e = tx.QueryRow(ctx, "SELECT count(*) FROM api_users WHERE id=ANY($1)", ids).Scan(&count); e != nil {
		return 0, e
	}
	if count != len(ids) {
		return 0, ErrNotFound
	}
	if e = tx.QueryRow(ctx, "SELECT count(*) FROM api_users WHERE role='admin' AND NOT(id=ANY($1))", ids).Scan(&count); e != nil {
		return 0, e
	}
	if count == 0 {
		return 0, ErrLastAdmin
	}
	tag, e := tx.Exec(ctx, "DELETE FROM api_users WHERE id=ANY($1)", ids)
	if e != nil {
		return 0, e
	}
	return int(tag.RowsAffected()), tx.Commit(ctx)
}

// WalkUsers streams public fields so HTML search/pagination does not load an
// unbounded user list or truncate searches at the legacy API's 1000-row limit.
func (s *Store) WalkUsers(ctx context.Context, visit func(model.User)) error {
	rows, e := s.Pool.Query(ctx, "SELECT id,username,role,''::text,''::text,display_name,language FROM api_users ORDER BY username,id")
	if e != nil {
		return e
	}
	defer rows.Close()
	for rows.Next() {
		u, e := scanUser(rows)
		if e != nil {
			return e
		}
		visit(u)
	}
	return rows.Err()
}
