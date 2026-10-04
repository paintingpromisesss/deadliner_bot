package repo

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/sauron/deadliner/internal/domain"
)

const groupColumns = `id, slug, slug_norm, title, status, official, created_by,
	default_presets, claim_expires_at, created_at, updated_at, deleted_at`

type groupsRepo struct {
	pool *pgxpool.Pool
}

func NewGroups(pool *pgxpool.Pool) domain.GroupRepo {
	return &groupsRepo{pool: pool}
}

var _ domain.GroupRepo = (*groupsRepo)(nil)

// presetsToMinutes converts durations to the int[] (minutes) column format.
func presetsToMinutes(presets []time.Duration) []int32 {
	out := make([]int32, 0, len(presets))
	for _, p := range presets {
		out = append(out, int32(p.Minutes()))
	}
	return out
}

func presetsFromMinutes(minutes []int32) []time.Duration {
	out := make([]time.Duration, 0, len(minutes))
	for _, m := range minutes {
		out = append(out, time.Duration(m)*time.Minute)
	}
	return out
}

func scanGroup(row pgx.Row) (*domain.Group, error) {
	var (
		g       domain.Group
		status  string
		presets []int32
		expiry  *time.Time
		delAt   *time.Time
	)
	err := row.Scan(
		&g.ID, &g.Slug, &g.SlugNorm, &g.Title, &status, &g.Official, &g.CreatedBy,
		&presets, &expiry, &g.CreatedAt, &g.UpdatedAt, &delAt,
	)
	if err != nil {
		return nil, err
	}
	g.Status = domain.GroupStatus(status)
	g.DefaultPresets = presetsFromMinutes(presets)
	g.ClaimExpiresAt = expiry
	g.DeletedAt = delAt
	return &g, nil
}

func (r *groupsRepo) Create(ctx context.Context, g *domain.Group) error {
	// Defensive re-normalization: uniqueness of slug_norm is the invariant
	// (М8О-401Б-23 and м8о-401б-23 are the same group).
	slugNorm := domain.Normalize(g.Slug)
	row := r.pool.QueryRow(ctx,
		`INSERT INTO groups (slug, slug_norm, title, status, official, created_by,
			default_presets, claim_expires_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		 RETURNING id, created_at, updated_at`,
		g.Slug, slugNorm, g.Title, string(g.Status), g.Official, g.CreatedBy,
		presetsToMinutes(g.DefaultPresets), g.ClaimExpiresAt)
	if err := row.Scan(&g.ID, &g.CreatedAt, &g.UpdatedAt); err != nil {
		return mapErr(err)
	}
	g.SlugNorm = slugNorm
	return nil
}

func (r *groupsRepo) GetByID(ctx context.Context, id int64) (*domain.Group, error) {
	g, err := scanGroup(r.pool.QueryRow(ctx,
		`SELECT `+groupColumns+` FROM groups WHERE id = $1 AND deleted_at IS NULL`, id))
	if err != nil {
		return nil, mapNotFoundErr(err, fmt.Sprintf("group id=%d", id))
	}
	return g, nil
}

func (r *groupsRepo) GetBySlugNorm(ctx context.Context, slugNorm string) (*domain.Group, error) {
	g, err := scanGroup(r.pool.QueryRow(ctx,
		`SELECT `+groupColumns+` FROM groups
		 WHERE slug_norm = $1 AND deleted_at IS NULL`, slugNorm))
	if err != nil {
		return nil, mapNotFoundErr(err, fmt.Sprintf("group slug_norm=%q", slugNorm))
	}
	return g, nil
}

// SearchByPrefix returns non-deleted groups whose slug_norm starts with the
// normalized prefix: all active groups plus pending groups created by
// callerID (spec §6.4 — pending groups are visible only to their creator).
func (r *groupsRepo) SearchByPrefix(ctx context.Context, prefix string, callerID int64, limit int) ([]domain.Group, error) {
	pattern := escapeLike(domain.Normalize(prefix)) + "%"
	rows, err := r.pool.Query(ctx,
		`SELECT `+groupColumns+` FROM groups
		 WHERE deleted_at IS NULL
		   AND slug_norm LIKE $1
		   AND (status = 'active' OR (status = 'pending' AND created_by = $2))
		 ORDER BY slug_norm
		 LIMIT $3`,
		pattern, callerID, limit)
	if err != nil {
		return nil, mapErr(err)
	}
	defer rows.Close()
	return collectGroups(rows)
}

// ListAll implements domain.GroupRepo.ListAll: non-deleted groups of a status,
// ordered by slug_norm; limit <= 0 means "all". Used by CLI `admin list-groups`
// (спека §2).
func (r *groupsRepo) ListAll(ctx context.Context, status *domain.GroupStatus, limit int) ([]domain.Group, error) {
	q := `SELECT ` + groupColumns + ` FROM groups WHERE deleted_at IS NULL`
	args := []any{}
	if status != nil {
		args = append(args, string(*status))
		q += fmt.Sprintf(` AND status = $%d`, len(args))
	}
	q += ` ORDER BY slug_norm`
	if limit > 0 {
		args = append(args, limit)
		q += fmt.Sprintf(` LIMIT $%d`, len(args))
	}
	rows, err := r.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, mapErr(err)
	}
	defer rows.Close()
	return collectGroups(rows)
}

func (r *groupsRepo) Update(ctx context.Context, g *domain.Group) error {
	tag, err := r.pool.Exec(ctx,
		`UPDATE groups
		 SET title = $2, official = $3, default_presets = $4, updated_at = now()
		 WHERE id = $1 AND deleted_at IS NULL`,
		g.ID, g.Title, g.Official, presetsToMinutes(g.DefaultPresets))
	if err != nil {
		return mapErr(err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("%w: group id=%d", domain.ErrNotFound, g.ID)
	}
	return nil
}

func (r *groupsRepo) SetStatus(ctx context.Context, id int64, status domain.GroupStatus) error {
	tag, err := r.pool.Exec(ctx,
		`UPDATE groups SET status = $2, updated_at = now()
		 WHERE id = $1 AND deleted_at IS NULL`, id, string(status))
	if err != nil {
		return mapErr(err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("%w: group id=%d", domain.ErrNotFound, id)
	}
	return nil
}

func (r *groupsRepo) SoftDelete(ctx context.Context, id int64) error {
	tag, err := r.pool.Exec(ctx,
		`UPDATE groups SET deleted_at = now(), updated_at = now()
		 WHERE id = $1 AND deleted_at IS NULL`, id)
	if err != nil {
		return mapErr(err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("%w: group id=%d", domain.ErrNotFound, id)
	}
	return nil
}

func (r *groupsRepo) HardDelete(ctx context.Context, id int64) error {
	tag, err := r.pool.Exec(ctx, `DELETE FROM groups WHERE id = $1`, id)
	if err != nil {
		return mapErr(err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("%w: group id=%d", domain.ErrNotFound, id)
	}
	return nil
}

// ListMine returns non-deleted groups the user belongs to, ordered by slug_norm.
func (r *groupsRepo) ListMine(ctx context.Context, userID int64) ([]domain.Group, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT `+prefixColumns("g", groupColumns)+` FROM groups g
		 JOIN group_memberships m ON m.group_id = g.id
		 WHERE m.user_id = $1 AND g.deleted_at IS NULL
		 ORDER BY g.slug_norm`, userID)
	if err != nil {
		return nil, mapErr(err)
	}
	defer rows.Close()
	return collectGroups(rows)
}

func (r *groupsRepo) ListPendingExpired(ctx context.Context, now time.Time, limit int) ([]domain.Group, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT `+groupColumns+` FROM groups
		 WHERE status = 'pending' AND deleted_at IS NULL
		   AND claim_expires_at IS NOT NULL AND claim_expires_at <= $1
		 ORDER BY claim_expires_at
		 LIMIT $2`, now, limit)
	if err != nil {
		return nil, mapErr(err)
	}
	defer rows.Close()
	return collectGroups(rows)
}

func collectGroups(rows pgx.Rows) ([]domain.Group, error) {
	out := []domain.Group{}
	for rows.Next() {
		g, err := scanGroup(rows)
		if err != nil {
			return nil, mapErr(err)
		}
		out = append(out, *g)
	}
	return out, rows.Err()
}

// prefixColumns qualifies a plain column list with a table alias.
func prefixColumns(alias, columns string) string {
	parts := strings.FieldsFunc(columns, func(r rune) bool {
		return r == ',' || r == '\n' || r == '\t'
	})
	for i, p := range parts {
		parts[i] = alias + "." + strings.TrimSpace(p)
	}
	return strings.Join(parts, ", ")
}

// escapeLike neutralizes LIKE wildcards in user input.
func escapeLike(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `%`, `\%`)
	return strings.ReplaceAll(s, `_`, `\_`)
}
