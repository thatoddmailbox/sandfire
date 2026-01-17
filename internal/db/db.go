package db

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	_ "github.com/mattn/go-sqlite3"
)

type DB struct {
	*sql.DB
}

func Open(dataDir string) (*DB, error) {
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		return nil, fmt.Errorf("create data directory: %w", err)
	}

	dbPath := filepath.Join(dataDir, "sandfire.db")
	sqlDB, err := sql.Open("sqlite3", dbPath+"?_foreign_keys=on")
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}

	if err := sqlDB.Ping(); err != nil {
		sqlDB.Close()
		return nil, fmt.Errorf("ping database: %w", err)
	}

	db := &DB{sqlDB}
	if err := db.migrate(); err != nil {
		sqlDB.Close()
		return nil, fmt.Errorf("run migrations: %w", err)
	}

	return db, nil
}

func (db *DB) migrate() error {
	migrations := []string{
		`CREATE TABLE IF NOT EXISTS os_images (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			kernel_path TEXT NOT NULL,
			rootfs_path TEXT NOT NULL,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE IF NOT EXISTS vms (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			os_image_id TEXT NOT NULL,
			ram_mb INTEGER NOT NULL DEFAULT 512,
			disk_size_gb INTEGER NOT NULL DEFAULT 8,
			vcpu_count INTEGER NOT NULL DEFAULT 1,
			internet_enabled BOOLEAN NOT NULL DEFAULT 0,
			state TEXT NOT NULL DEFAULT 'stopped',
			ip_address TEXT,
			tap_device TEXT,
			was_running BOOLEAN NOT NULL DEFAULT 0,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			FOREIGN KEY (os_image_id) REFERENCES os_images(id)
		)`,
	}

	for _, m := range migrations {
		if _, err := db.Exec(m); err != nil {
			return fmt.Errorf("execute migration: %w", err)
		}
	}

	return nil
}
