package repository

import (
	"context"
	"fmt"

	"gorm.io/gorm"
)

// TransactionManager executes a function within a database transaction.
type TransactionManager interface {
	RunInTransaction(ctx context.Context, fn func(ctx context.Context) error) error
}

type transactionManager struct {
	db *gorm.DB
}

// NewTransactionManager creates a new transaction manager.
func NewTransactionManager(db *gorm.DB) TransactionManager {
	return &transactionManager{db: db}
}

// RunInTransaction executes fn in a DB transaction and injects tx into context.
func (tm *transactionManager) RunInTransaction(ctx context.Context, fn func(ctx context.Context) error) error {
	if tm == nil || tm.db == nil {
		return fmt.Errorf("db is nil")
	}

	return tm.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		txCtx := withTx(ctx, tx)
		return fn(txCtx)
	})
}

type txContextKey struct{}

func withTx(ctx context.Context, tx *gorm.DB) context.Context {
	return context.WithValue(ctx, txContextKey{}, tx)
}

func dbFromContext(ctx context.Context, fallback *gorm.DB) *gorm.DB {
	if ctx != nil {
		if tx, ok := ctx.Value(txContextKey{}).(*gorm.DB); ok && tx != nil {
			return tx
		}
	}

	return fallback
}
