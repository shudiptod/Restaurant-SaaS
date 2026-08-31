package db

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	_ "github.com/lib/pq"
)

var DB *sql.DB

type contextKey string

const UserIDKey contextKey = "user_id"

func cleanPostgresDSN(dsn string) string {
	// Remove channel_binding parameter if present as lib/pq does not support it
	dsn = strings.ReplaceAll(dsn, "&channel_binding=require", "")
	dsn = strings.ReplaceAll(dsn, "channel_binding=require&", "")
	dsn = strings.ReplaceAll(dsn, "?channel_binding=require", "")
	return dsn
}

// InitDB initializes the global DB pool
func InitDB(dataSourceName string) error {
	dataSourceName = cleanPostgresDSN(dataSourceName)
	var err error
	DB, err = sql.Open("postgres", dataSourceName)
	if err != nil {
		return err
	}
	return DB.Ping()
}

// WithTx runs the callback function fn inside a transaction.
// If a user ID is set in the context, it sets the session variable 'app.current_user_id'
// locally within the transaction using set_config, enforcing Postgres Row Level Security.
func WithTx(ctx context.Context, fn func(*sql.Tx) error) error {
	tx, err := DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if userID, ok := ctx.Value(UserIDKey).(string); ok && userID != "" {
		// Use set_config to set transaction-local app.current_user_id setting safely
		_, err = tx.ExecContext(ctx, "SELECT set_config('app.current_user_id', $1, true)", userID)
		if err != nil {
			return fmt.Errorf("failed to set RLS user context: %w", err)
		}
	}

	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit()
}
