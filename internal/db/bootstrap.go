package db

import (
	"context"
	"fmt"
	"os"
	"strings"

	"golang.org/x/crypto/bcrypt"
)

// BootstrapPlatformOwner creates the initial superadmin only when none exists.
func BootstrapPlatformOwner(ctx context.Context) error {
	username := strings.TrimSpace(os.Getenv("PLATFORM_OWNER_USERNAME"))
	password := os.Getenv("PLATFORM_OWNER_PASSWORD")
	email := strings.TrimSpace(os.Getenv("PLATFORM_OWNER_EMAIL"))
	if email == "" {
		email = username
	}
	fullName := strings.TrimSpace(os.Getenv("PLATFORM_OWNER_NAME"))
	if fullName == "" {
		fullName = "Platform Owner"
	}

	tx, err := DB.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin platform owner bootstrap: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, "SELECT pg_advisory_xact_lock(746281903)"); err != nil {
		return fmt.Errorf("lock platform owner bootstrap: %w", err)
	}
	var existing int
	if err := tx.QueryRowContext(ctx, "SELECT count(*) FROM platform_admins").Scan(&existing); err != nil {
		return fmt.Errorf("check existing platform owners: %w", err)
	}
	if existing > 0 {
		return tx.Commit()
	}
	if username == "" || password == "" {
		return fmt.Errorf("PLATFORM_OWNER_USERNAME and PLATFORM_OWNER_PASSWORD are required before the first startup")
	}
	if len(password) < 12 {
		return fmt.Errorf("PLATFORM_OWNER_PASSWORD must be at least 12 characters")
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return fmt.Errorf("hash platform owner password: %w", err)
	}

	var userID string
	err = tx.QueryRowContext(ctx, `
		INSERT INTO users (username, email, password_hash, full_name)
		VALUES ($1, $2, $3, $4)
		RETURNING id
	`, username, email, string(hash), fullName).Scan(&userID)
	if err != nil {
		return fmt.Errorf("create initial platform owner: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO platform_admins (user_id, role) VALUES ($1, 'superadmin')
	`, userID); err != nil {
		return fmt.Errorf("grant initial platform owner role: %w", err)
	}
	return tx.Commit()
}
