package postgres

import (
	"context"
	"fmt"
	"time"

	"backend/internal/config"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Client wraps a pgxpool.Pool connection.
type Client struct {
	Pool *pgxpool.Pool
}

// New initializes a new PostgreSQL connection pool using configuration parameters.
func New(ctx context.Context, cfg config.PostgresConfig) (*Client, error) {
	poolCfg, err := pgxpool.ParseConfig(cfg.DSN())
	if err != nil {
		return nil, fmt.Errorf("postgres: invalid DSN: %w", err)
	}

	if cfg.MaxConns > 0 {
		poolCfg.MaxConns = cfg.MaxConns
	}
	if cfg.MinConns > 0 {
		poolCfg.MinConns = cfg.MinConns
	}
	if cfg.MaxConnLifetime > 0 {
		poolCfg.MaxConnLifetime = cfg.MaxConnLifetime
	}
	if cfg.MaxConnIdleTime > 0 {
		poolCfg.MaxConnIdleTime = cfg.MaxConnIdleTime
	}
	if cfg.HealthCheckPeriod > 0 {
		poolCfg.HealthCheckPeriod = cfg.HealthCheckPeriod
	}

	pool, err := pgxpool.NewWithConfig(ctx, poolCfg)
	if err != nil {
		return nil, fmt.Errorf("postgres: connection failed: %w", err)
	}

	pingCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	if err := pool.Ping(pingCtx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("postgres: ping failed: %w", err)
	}

	return &Client{Pool: pool}, nil
}

// Ping checks if the database is reachable.
func (c *Client) Ping(ctx context.Context) error {
	return c.Pool.Ping(ctx)
}

// Close gracefully closes the connection pool.
func (c *Client) Close() {
	if c.Pool != nil {
		c.Pool.Close()
	}
}

// TxManager defines the transaction execution helper interface.
type TxManager interface {
	WithinTransaction(ctx context.Context, fn func(ctx context.Context, tx pgx.Tx) error) error
	WithinTransactionWithOptions(ctx context.Context, opts pgx.TxOptions, fn func(ctx context.Context, tx pgx.Tx) error) error
}

type txManager struct {
	pool *pgxpool.Pool
}

// NewTxManager creates a new transaction manager wrapping the provided pool.
func NewTxManager(pool *pgxpool.Pool) TxManager {
	return &txManager{pool: pool}
}

// WithinTransaction runs a function within a standard database transaction.
// If fn returns an error or panics, the transaction is rolled back; otherwise, it is committed.
func (m *txManager) WithinTransaction(ctx context.Context, fn func(ctx context.Context, tx pgx.Tx) error) error {
	return m.WithinTransactionWithOptions(ctx, pgx.TxOptions{}, fn)
}

// WithinTransactionWithOptions runs a function within a transaction configured with custom options.
func (m *txManager) WithinTransactionWithOptions(ctx context.Context, opts pgx.TxOptions, fn func(ctx context.Context, tx pgx.Tx) error) (err error) {
	tx, err := m.pool.BeginTx(ctx, opts)
	if err != nil {
		return fmt.Errorf("postgres: begin tx error: %w", err)
	}

	defer func() {
		if p := recover(); p != nil {
			_ = tx.Rollback(ctx)
			panic(p)
		} else if err != nil {
			_ = tx.Rollback(ctx)
		} else {
			if commitErr := tx.Commit(ctx); commitErr != nil {
				err = fmt.Errorf("postgres: commit tx error: %w", commitErr)
			}
		}
	}()

	err = fn(ctx, tx)
	return err
}
