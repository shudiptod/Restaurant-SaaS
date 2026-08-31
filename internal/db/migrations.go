package db

import (
	"database/sql"
	"fmt"
	"io/ioutil"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
)

type Migration struct {
	Version int
	Name    string
	Path    string
}

// RunMigrations checks the schema_migrations table and applies any pending migrations found in docsDir.
func RunMigrations(docsDir string) error {
	// 1. Create migrations table if not exists
	_, err := DB.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations (version bigint PRIMARY KEY, dirty boolean NOT NULL)`)
	if err != nil {
		return fmt.Errorf("failed to create migrations table: %w", err)
	}

	// 2. Get current applied version
	var currentVersion int64
	var dirty bool
	err = DB.QueryRow(`SELECT version, dirty FROM schema_migrations LIMIT 1`).Scan(&currentVersion, &dirty)
	if err != nil {
		if err == sql.ErrNoRows {
			currentVersion = 0
			dirty = false
		} else {
			return fmt.Errorf("failed to query migrations table: %w", err)
		}
	}

	if dirty {
		return fmt.Errorf("database is in a dirty state; please resolve manually before proceeding")
	}

	// 3. Scan docs directory for up migrations
	files, err := ioutil.ReadDir(docsDir)
	if err != nil {
		return fmt.Errorf("failed to read migrations dir %s: %w", docsDir, err)
	}

	var migrations []Migration
	re := regexp.MustCompile(`^(\d+)_.*\.up\.sql$`)
	for _, f := range files {
		if f.IsDir() {
			continue
		}
		matches := re.FindStringSubmatch(f.Name())
		if len(matches) < 2 {
			continue
		}
		version, err := strconv.Atoi(matches[1])
		if err != nil {
			continue
		}
		migrations = append(migrations, Migration{
			Version: version,
			Name:    f.Name(),
			Path:    filepath.Join(docsDir, f.Name()),
		})
	}

	sort.Slice(migrations, func(i, j int) bool {
		return migrations[i].Version < migrations[j].Version
	})

	// 4. Run unapplied migrations
	for _, m := range migrations {
		if int64(m.Version) <= currentVersion {
			continue
		}

		fmt.Printf("Applying migration %s...\n", m.Name)
		content, err := ioutil.ReadFile(m.Path)
		if err != nil {
			return fmt.Errorf("failed to read migration file %s: %w", m.Name, err)
		}

		// Begin transaction for the migration
		tx, err := DB.Begin()
		if err != nil {
			return fmt.Errorf("failed to begin migration transaction: %w", err)
		}

		// Update schema_migrations to mark dirty (single row constraint)
		_, err = tx.Exec(`DELETE FROM schema_migrations`)
		if err != nil {
			tx.Rollback()
			return fmt.Errorf("failed to clear migrations table: %w", err)
		}
		_, err = tx.Exec(`INSERT INTO schema_migrations (version, dirty) VALUES ($1, true)`, m.Version)
		if err != nil {
			tx.Rollback()
			return fmt.Errorf("failed to mark migration dirty: %w", err)
		}

		// Run migration content
		_, err = tx.Exec(string(content))
		if err != nil {
			tx.Rollback()
			return fmt.Errorf("failed to execute migration %s: %w", m.Name, err)
		}

		// Mark clean and update version
		_, err = tx.Exec(`UPDATE schema_migrations SET dirty = false WHERE version = $1`, m.Version)
		if err != nil {
			tx.Rollback()
			return fmt.Errorf("failed to mark migration clean: %w", err)
		}

		if err := tx.Commit(); err != nil {
			return fmt.Errorf("failed to commit migration transaction: %w", err)
		}
		fmt.Printf("Successfully applied migration %s.\n", m.Name)
	}

	return nil
}
