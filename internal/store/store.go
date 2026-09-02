package store

import (
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"strings"

	_ "modernc.org/sqlite"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// Store 封装 SQLite 连接与所有仓储方法。
type Store struct {
	*sql.DB
}

func Open(dbPath string) (*Store, error) {
	// 单文件 + WAL + 外键。host 上用文件即可（抓取模式无并发写压力）。
	dsn := fmt.Sprintf("file:%s?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(ON)", dbPath)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open db: %w", err)
	}
	db.SetMaxOpenConns(1) // SQLite 单写者，避免锁
	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("ping db: %w", err)
	}
	s := &Store{db}
	if err := s.migrate(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Store) migrate() error {
	var version int
	if err := s.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		return fmt.Errorf("read user_version: %w", err)
	}
	entries, err := fs.ReadDir(migrationsFS, "migrations")
	if err != nil {
		return err
	}
	applied := 0
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".sql") {
			continue
		}
		// 文件名形式 001_init.sql -> 版本号 1
		var v int
		if _, err := fmt.Sscanf(e.Name(), "%03d", &v); err != nil || v <= version {
			continue
		}
		data, err := migrationsFS.ReadFile("migrations/" + e.Name())
		if err != nil {
			return err
		}
		tx, err := s.Begin()
		if err != nil {
			return err
		}
		for _, stmt := range splitSQL(string(data)) {
			if strings.TrimSpace(stmt) == "" {
				continue
			}
			if _, err := tx.Exec(stmt); err != nil {
				_ = tx.Rollback()
				return fmt.Errorf("migrate %s: %w", e.Name(), err)
			}
		}
		if _, err := tx.Exec(fmt.Sprintf("PRAGMA user_version = %d", v)); err != nil {
			_ = tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
		applied++
		version = v
	}
	if applied > 0 {
		fmt.Printf("store: applied %d migration(s) -> v%d\n", applied, version)
	}
	return nil
}

func splitSQL(data string) []string {
	// 简单按分号切割（本项目迁移无存储过程/触发器，足够）
	var out []string
	for _, part := range strings.Split(data, ";") {
		if strings.TrimSpace(part) != "" {
			out = append(out, part)
		}
	}
	return out
}

func (s *Store) Health() error {
	return s.Ping()
}
