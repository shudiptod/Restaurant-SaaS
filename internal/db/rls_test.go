package db

import (
	"context"
	"database/sql"
	"testing"
)

func TestRLSIsolation(t *testing.T) {
	// 1. Initialize admin DB pool (superuser)
	adminURL := "postgres://postgres:devpass@localhost:5432/rms_dev?sslmode=disable"
	err := InitDB(adminURL)
	if err != nil {
		t.Fatalf("Failed to connect to test database: %v", err)
	}
	defer DB.Close()

	// Run migrations to ensure schema exists
	err = RunMigrations("../../docs")
	if err != nil {
		t.Fatalf("Failed to run migrations for test DB: %v", err)
	}

	// Create non-superuser role for RLS validation
	_, err = DB.Exec("CREATE ROLE rms_test_user WITH LOGIN PASSWORD 'testpass'")
	if err != nil {
		// Ignore role already exists error and just update password
		_, err = DB.Exec("ALTER ROLE rms_test_user WITH PASSWORD 'testpass'")
		if err != nil {
			t.Fatalf("Failed to create/alter non-superuser role: %v", err)
		}
	}
	_, err = DB.Exec("GRANT ALL PRIVILEGES ON DATABASE rms_dev TO rms_test_user")
	if err != nil {
		t.Fatalf("Failed to grant database privileges: %v", err)
	}
	_, err = DB.Exec("GRANT USAGE, CREATE ON SCHEMA public TO rms_test_user")
	if err != nil {
		t.Fatalf("Failed to grant schema privileges: %v", err)
	}
	_, err = DB.Exec("GRANT ALL PRIVILEGES ON ALL TABLES IN SCHEMA public TO rms_test_user")
	if err != nil {
		t.Fatalf("Failed to grant table privileges: %v", err)
	}
	_, err = DB.Exec("GRANT ALL PRIVILEGES ON ALL SEQUENCES IN SCHEMA public TO rms_test_user")
	if err != nil {
		t.Fatalf("Failed to grant sequence privileges: %v", err)
	}

	ctx := context.Background()

	// Insert mock testing rows as superadmin (so it ignores RLS)
	var userA, userB string
	var restA, restB string

	err = DB.QueryRowContext(ctx, "INSERT INTO users (email, password_hash, full_name) VALUES ('usera@test.com', 'hash', 'User A') RETURNING id").Scan(&userA)
	if err != nil {
		t.Fatalf("Failed to insert user A: %v", err)
	}
	err = DB.QueryRowContext(ctx, "INSERT INTO users (email, password_hash, full_name) VALUES ('userb@test.com', 'hash', 'User B') RETURNING id").Scan(&userB)
	if err != nil {
		t.Fatalf("Failed to insert user B: %v", err)
	}

	defer func() {
		// Clean up data after tests
		_, _ = DB.ExecContext(ctx, "DELETE FROM tables WHERE restaurant_id IN ($1, $2)", restA, restB)
		_, _ = DB.ExecContext(ctx, "DELETE FROM restaurant_users WHERE user_id IN ($1, $2)", userA, userB)
		_, _ = DB.ExecContext(ctx, "DELETE FROM restaurants WHERE id IN ($1, $2)", restA, restB)
		_, _ = DB.ExecContext(ctx, "DELETE FROM accounts WHERE owner_user_id IN ($1, $2)", userA, userB)
		_, _ = DB.ExecContext(ctx, "DELETE FROM users WHERE id IN ($1, $2)", userA, userB)
		_, _ = DB.Exec("DROP ROLE IF EXISTS rms_test_user")
	}()

	var accA, accB string
	err = DB.QueryRowContext(ctx, "INSERT INTO accounts (name, owner_user_id) VALUES ('Acc A', $1) RETURNING id", userA).Scan(&accA)
	if err != nil {
		t.Fatalf("Failed to insert acc A: %v", err)
	}
	err = DB.QueryRowContext(ctx, "INSERT INTO accounts (name, owner_user_id) VALUES ('Acc B', $1) RETURNING id", userB).Scan(&accB)
	if err != nil {
		t.Fatalf("Failed to insert acc B: %v", err)
	}

	err = DB.QueryRowContext(ctx, "INSERT INTO restaurants (account_id, name, slug) VALUES ($1, 'Rest A', 'rest-a') RETURNING id", accA).Scan(&restA)
	if err != nil {
		t.Fatalf("Failed to insert rest A: %v", err)
	}
	err = DB.QueryRowContext(ctx, "INSERT INTO restaurants (account_id, name, slug) VALUES ($1, 'Rest B', 'rest-b') RETURNING id", accB).Scan(&restB)
	if err != nil {
		t.Fatalf("Failed to insert rest B: %v", err)
	}

	// Active memberships
	_, err = DB.ExecContext(ctx, "INSERT INTO restaurant_users (restaurant_id, user_id, role, status) VALUES ($1, $2, 'owner', 'active')", restA, userA)
	if err != nil {
		t.Fatalf("Failed to insert membership A: %v", err)
	}
	_, err = DB.ExecContext(ctx, "INSERT INTO restaurant_users (restaurant_id, user_id, role, status) VALUES ($1, $2, 'owner', 'active')", restB, userB)
	if err != nil {
		t.Fatalf("Failed to insert membership B: %v", err)
	}

	// Add tables under each restaurant
	var tableA, tableB string
	err = DB.QueryRowContext(ctx, "INSERT INTO tables (restaurant_id, name, capacity, status) VALUES ($1, 'Table A', 4, 'available') RETURNING id", restA).Scan(&tableA)
	if err != nil {
		t.Fatalf("Failed to insert table A: %v", err)
	}
	err = DB.QueryRowContext(ctx, "INSERT INTO tables (restaurant_id, name, capacity, status) VALUES ($1, 'Table B', 6, 'available') RETURNING id", restB).Scan(&tableB)
	if err != nil {
		t.Fatalf("Failed to insert table B: %v", err)
	}

	// Add inventory items under each restaurant
	var invA, invB string
	err = DB.QueryRowContext(ctx, "INSERT INTO inventory_items (restaurant_id, name, unit, current_quantity, reorder_threshold) VALUES ($1, 'Item A', 'kg', 10, 5) RETURNING id", restA).Scan(&invA)
	if err != nil {
		t.Fatalf("Failed to insert inventory item A: %v", err)
	}
	err = DB.QueryRowContext(ctx, "INSERT INTO inventory_items (restaurant_id, name, unit, current_quantity, reorder_threshold) VALUES ($1, 'Item B', 'kg', 20, 5) RETURNING id", restB).Scan(&invB)
	if err != nil {
		t.Fatalf("Failed to insert inventory item B: %v", err)
	}

	defer func() {
		_, _ = DB.ExecContext(ctx, "DELETE FROM inventory_items WHERE restaurant_id IN ($1, $2)", restA, restB)
	}()

	// 2. Open non-superuser connection for validation
	testDB, err := sql.Open("postgres", "postgres://rms_test_user:testpass@localhost:5432/rms_dev?sslmode=disable")
	if err != nil {
		t.Fatalf("Failed to connect as non-superuser: %v", err)
	}
	defer testDB.Close()

	// 3. Test RLS Enforcements as User A
	txA, err := testDB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("Failed to begin tx as User A: %v", err)
	}
	defer txA.Rollback()

	_, err = txA.ExecContext(ctx, "SELECT set_config('app.current_user_id', $1, true)", userA)
	if err != nil {
		t.Fatalf("Failed to set app.current_user_id for User A: %v", err)
	}

	rows, err := txA.QueryContext(ctx, "SELECT id FROM tables")
	if err != nil {
		t.Fatalf("Failed to select tables as User A: %v", err)
	}
	defer rows.Close()

	var foundTableA, foundTableB bool
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err == nil {
			if id == tableA {
				foundTableA = true
			}
			if id == tableB {
				foundTableB = true
			}
		}
	}

	if !foundTableA {
		t.Errorf("RLS Isolation Violation: User A could not see their own Table A")
	}
	if foundTableB {
		t.Errorf("RLS Security Leak: User A was able to read User B's Table B!")
	}

	// Verify inventory isolation for User A
	invRowsA, err := txA.QueryContext(ctx, "SELECT id FROM inventory_items")
	if err != nil {
		t.Fatalf("Failed to select inventory as User A: %v", err)
	}
	defer invRowsA.Close()
	var foundInvA, foundInvB bool
	for invRowsA.Next() {
		var id string
		if err := invRowsA.Scan(&id); err == nil {
			if id == invA {
				foundInvA = true
			}
			if id == invB {
				foundInvB = true
			}
		}
	}
	if !foundInvA {
		t.Errorf("RLS Isolation Violation: User A could not see their own Inventory Item A")
	}
	if foundInvB {
		t.Errorf("RLS Security Leak: User A was able to read User B's Inventory Item B!")
	}

	// 4. Test RLS Enforcements as User B
	txB, err := testDB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("Failed to begin tx as User B: %v", err)
	}
	defer txB.Rollback()

	_, err = txB.ExecContext(ctx, "SELECT set_config('app.current_user_id', $1, true)", userB)
	if err != nil {
		t.Fatalf("Failed to set app.current_user_id for User B: %v", err)
	}

	rowsB, err := txB.QueryContext(ctx, "SELECT id FROM tables")
	if err != nil {
		t.Fatalf("Failed to select tables as User B: %v", err)
	}
	defer rowsB.Close()

	foundTableA = false
	foundTableB = false
	for rowsB.Next() {
		var id string
		if err := rowsB.Scan(&id); err == nil {
			if id == tableA {
				foundTableA = true
			}
			if id == tableB {
				foundTableB = true
			}
		}
	}

	if foundTableA {
		t.Errorf("RLS Security Leak: User B was able to read User A's Table A!")
	}
	if !foundTableB {
		t.Errorf("RLS Isolation Violation: User B could not see their own Table B")
	}

	// Verify inventory isolation for User B
	invRowsB, err := txB.QueryContext(ctx, "SELECT id FROM inventory_items")
	if err != nil {
		t.Fatalf("Failed to select inventory as User B: %v", err)
	}
	defer invRowsB.Close()
	foundInvA = false
	foundInvB = false
	for invRowsB.Next() {
		var id string
		if err := invRowsB.Scan(&id); err == nil {
			if id == invA {
				foundInvA = true
			}
			if id == invB {
				foundInvB = true
			}
		}
	}
	if foundInvA {
		t.Errorf("RLS Security Leak: User B was able to read User A's Inventory Item A!")
	}
	if !foundInvB {
		t.Errorf("RLS Isolation Violation: User B could not see their own Inventory Item B")
	}
}
