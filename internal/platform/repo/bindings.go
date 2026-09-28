package repo

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/sauron/deadliner/internal/domain"
)

const bindingColumns = `id, group_id, chat_id, message_thread_id, chat_title, bound_by, bound_at`

type bindingsRepo struct {
	pool *pgxpool.Pool
}

func NewBindings(pool *pgxpool.Pool) domain.ChatBindingRepo {
	return &bindingsRepo{pool: pool}
}

var _ domain.ChatBindingRepo = (*bindingsRepo)(nil)

func scanBinding(row pgx.Row) (*domain.ChatBinding, error) {
	var (
		b     domain.ChatBinding
		title *string
	)
	err := row.Scan(
		&b.ID, &b.GroupID, &b.ChatID, &b.MessageThreadID,
		&title, &b.BoundBy, &b.BoundAt,
	)
	if err != nil {
		return nil, err
	}
	if title != nil {
		b.ChatTitle = *title
	}
	return &b, nil
}

// Create inserts the binding. Both unique constraints — group_id and
// (chat_id, message_thread_id) NULLS NOT DISTINCT — map to domain.ErrConflict.
func (r *bindingsRepo) Create(ctx context.Context, b *domain.ChatBinding) error {
	row := r.pool.QueryRow(ctx,
		`INSERT INTO chat_bindings (group_id, chat_id, message_thread_id, chat_title, bound_by)
		 VALUES ($1, $2, $3, $4, $5)
		 RETURNING id, bound_at`,
		b.GroupID, b.ChatID, b.MessageThreadID, b.ChatTitle, b.BoundBy)
	if err := row.Scan(&b.ID, &b.BoundAt); err != nil {
		return mapErr(err)
	}
	return nil
}

func (r *bindingsRepo) GetByGroup(ctx context.Context, groupID int64) (*domain.ChatBinding, error) {
	b, err := scanBinding(r.pool.QueryRow(ctx,
		`SELECT `+bindingColumns+` FROM chat_bindings WHERE group_id = $1`, groupID))
	if err != nil {
		return nil, mapNotFoundErr(err, fmt.Sprintf("binding group_id=%d", groupID))
	}
	return b, nil
}

// GetByChat matches NULLS NOT DISTINCT semantics: a nil threadID must match
// rows with message_thread_id IS NULL.
func (r *bindingsRepo) GetByChat(ctx context.Context, chatID int64, threadID *int64) (*domain.ChatBinding, error) {
	b, err := scanBinding(r.pool.QueryRow(ctx,
		`SELECT `+bindingColumns+` FROM chat_bindings
		 WHERE chat_id = $1 AND message_thread_id IS NOT DISTINCT FROM $2`,
		chatID, threadID))
	if err != nil {
		return nil, mapNotFoundErr(err, fmt.Sprintf("binding chat_id=%d thread=%v", chatID, threadID))
	}
	return b, nil
}

func (r *bindingsRepo) Delete(ctx context.Context, groupID int64) error {
	tag, err := r.pool.Exec(ctx,
		`DELETE FROM chat_bindings WHERE group_id = $1`, groupID)
	if err != nil {
		return mapErr(err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("%w: binding group_id=%d", domain.ErrNotFound, groupID)
	}
	return nil
}
