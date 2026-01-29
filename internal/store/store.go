package store

import (
	"database/sql"
	"fmt"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

type Account struct {
	ID           int64     `json:"id"`
	Name         string    `json:"name"`
	SessionID    string    `json:"session_id"`
	ClientCookie string    `json:"client_cookie"`
	ClientUat    string    `json:"client_uat"`
	ProjectID    string    `json:"project_id"`
	UserID       string    `json:"user_id"`
	AgentMode    string    `json:"agent_mode"`
	Email        string    `json:"email"`
	Weight       int       `json:"weight"`
	Enabled      bool      `json:"enabled"`
	RequestCount int64     `json:"request_count"`
	SuccessCount int64     `json:"success_count"`
	FailureCount int64     `json:"failure_count"`
	LastUsedAt   time.Time `json:"last_used_at"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

type Settings struct {
	ID    int64  `json:"id"`
	Key   string `json:"key"`
	Value string `json:"value"`
}

type Store struct {
	db *sql.DB
	mu sync.RWMutex
}

func New(dbPath string) (*Store, error) {
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	store := &Store{db: db}
	if err := store.migrate(); err != nil {
		return nil, fmt.Errorf("failed to migrate database: %w", err)
	}

	return store, nil
}

func (s *Store) migrate() error {
	queries := []string{
		`CREATE TABLE IF NOT EXISTS accounts (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT NOT NULL,
			session_id TEXT NOT NULL,
			client_cookie TEXT NOT NULL,
			client_uat TEXT NOT NULL,
			project_id TEXT NOT NULL,
			user_id TEXT NOT NULL,
			agent_mode TEXT DEFAULT 'claude-opus-4.5',
			email TEXT NOT NULL,
			weight INTEGER DEFAULT 1,
			enabled INTEGER DEFAULT 1,
			request_count INTEGER DEFAULT 0,
			success_count INTEGER DEFAULT 0,
			failure_count INTEGER DEFAULT 0,
			last_used_at DATETIME,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE IF NOT EXISTS settings (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			key TEXT UNIQUE NOT NULL,
			value TEXT NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_accounts_enabled ON accounts(enabled)`,
		// 迁移：添加 success_count 和 failure_count 列（如果不存在）
		`ALTER TABLE accounts ADD COLUMN success_count INTEGER DEFAULT 0`,
		`ALTER TABLE accounts ADD COLUMN failure_count INTEGER DEFAULT 0`,
	}

	for _, q := range queries {
		_, err := s.db.Exec(q)
		// 忽略 "duplicate column" 错误（ALTER TABLE 添加已存在列）
		if err != nil && !isDuplicateColumnError(err) {
			return err
		}
	}

	return nil
}

// isDuplicateColumnError 检查是否是重复列错误
func isDuplicateColumnError(err error) bool {
	return err != nil && (
		// SQLite 错误消息
		contains(err.Error(), "duplicate column") ||
		contains(err.Error(), "already exists"))
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsHelper(s, substr))
}

func containsHelper(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

func (s *Store) Close() error {
	return s.db.Close()
}

func (s *Store) CreateAccount(acc *Account) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	result, err := s.db.Exec(`
		INSERT INTO accounts (name, session_id, client_cookie, client_uat, project_id, user_id, agent_mode, email, weight, enabled)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, acc.Name, acc.SessionID, acc.ClientCookie, acc.ClientUat, acc.ProjectID, acc.UserID, acc.AgentMode, acc.Email, acc.Weight, acc.Enabled)
	if err != nil {
		return err
	}

	id, err := result.LastInsertId()
	if err != nil {
		return err
	}
	acc.ID = id
	return nil
}

func (s *Store) UpdateAccount(acc *Account) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	_, err := s.db.Exec(`
		UPDATE accounts SET
			name = ?, session_id = ?, client_cookie = ?, client_uat = ?,
			project_id = ?, user_id = ?, agent_mode = ?, email = ?,
			weight = ?, enabled = ?, updated_at = CURRENT_TIMESTAMP
		WHERE id = ?
	`, acc.Name, acc.SessionID, acc.ClientCookie, acc.ClientUat, acc.ProjectID, acc.UserID, acc.AgentMode, acc.Email, acc.Weight, acc.Enabled, acc.ID)
	return err
}

func (s *Store) DeleteAccount(id int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	_, err := s.db.Exec("DELETE FROM accounts WHERE id = ?", id)
	return err
}

func (s *Store) GetAccount(id int64) (*Account, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	acc := &Account{}
	var lastUsedAt sql.NullTime
	err := s.db.QueryRow(`
		SELECT id, name, session_id, client_cookie, client_uat, project_id, user_id,
			   agent_mode, email, weight, enabled, request_count, success_count, failure_count,
			   last_used_at, created_at, updated_at
		FROM accounts WHERE id = ?
	`, id).Scan(&acc.ID, &acc.Name, &acc.SessionID, &acc.ClientCookie, &acc.ClientUat,
		&acc.ProjectID, &acc.UserID, &acc.AgentMode, &acc.Email, &acc.Weight,
		&acc.Enabled, &acc.RequestCount, &acc.SuccessCount, &acc.FailureCount,
		&lastUsedAt, &acc.CreatedAt, &acc.UpdatedAt)
	if err != nil {
		return nil, err
	}
	if lastUsedAt.Valid {
		acc.LastUsedAt = lastUsedAt.Time
	}
	return acc, nil
}

func (s *Store) ListAccounts() ([]*Account, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	rows, err := s.db.Query(`
		SELECT id, name, session_id, client_cookie, client_uat, project_id, user_id,
			   agent_mode, email, weight, enabled, request_count, success_count, failure_count,
			   last_used_at, created_at, updated_at
		FROM accounts ORDER BY id
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var accounts []*Account
	for rows.Next() {
		acc := &Account{}
		var lastUsedAt sql.NullTime
		err := rows.Scan(&acc.ID, &acc.Name, &acc.SessionID, &acc.ClientCookie, &acc.ClientUat,
			&acc.ProjectID, &acc.UserID, &acc.AgentMode, &acc.Email, &acc.Weight,
			&acc.Enabled, &acc.RequestCount, &acc.SuccessCount, &acc.FailureCount,
			&lastUsedAt, &acc.CreatedAt, &acc.UpdatedAt)
		if err != nil {
			return nil, err
		}
		if lastUsedAt.Valid {
			acc.LastUsedAt = lastUsedAt.Time
		}
		accounts = append(accounts, acc)
	}
	return accounts, nil
}

func (s *Store) GetEnabledAccounts() ([]*Account, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	rows, err := s.db.Query(`
		SELECT id, name, session_id, client_cookie, client_uat, project_id, user_id,
			   agent_mode, email, weight, enabled, request_count, success_count, failure_count,
			   last_used_at, created_at, updated_at
		FROM accounts WHERE enabled = 1 ORDER BY id
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var accounts []*Account
	for rows.Next() {
		acc := &Account{}
		var lastUsedAt sql.NullTime
		err := rows.Scan(&acc.ID, &acc.Name, &acc.SessionID, &acc.ClientCookie, &acc.ClientUat,
			&acc.ProjectID, &acc.UserID, &acc.AgentMode, &acc.Email, &acc.Weight,
			&acc.Enabled, &acc.RequestCount, &acc.SuccessCount, &acc.FailureCount,
			&lastUsedAt, &acc.CreatedAt, &acc.UpdatedAt)
		if err != nil {
			return nil, err
		}
		if lastUsedAt.Valid {
			acc.LastUsedAt = lastUsedAt.Time
		}
		accounts = append(accounts, acc)
	}
	return accounts, nil
}

func (s *Store) IncrementRequestCount(id int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	_, err := s.db.Exec(`
		UPDATE accounts SET request_count = request_count + 1, last_used_at = CURRENT_TIMESTAMP
		WHERE id = ?
	`, id)
	return err
}

// AddRequestCount 批量增加请求计数（用于异步批量更新）
func (s *Store) AddRequestCount(id int64, count int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	_, err := s.db.Exec(`
		UPDATE accounts SET request_count = request_count + ?, last_used_at = CURRENT_TIMESTAMP
		WHERE id = ?
	`, count, id)
	return err
}

// IncrementSuccessCount 增加成功计数
func (s *Store) IncrementSuccessCount(id int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	_, err := s.db.Exec(`
		UPDATE accounts SET success_count = success_count + 1
		WHERE id = ?
	`, id)
	return err
}

// IncrementFailureCount 增加失败计数
func (s *Store) IncrementFailureCount(id int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	_, err := s.db.Exec(`
		UPDATE accounts SET failure_count = failure_count + 1
		WHERE id = ?
	`, id)
	return err
}

// AddSuccessCount 批量增加成功计数（用于异步批量更新）
func (s *Store) AddSuccessCount(id int64, count int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	_, err := s.db.Exec(`
		UPDATE accounts SET success_count = success_count + ?
		WHERE id = ?
	`, count, id)
	return err
}

// AddFailureCount 批量增加失败计数（用于异步批量更新）
func (s *Store) AddFailureCount(id int64, count int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	_, err := s.db.Exec(`
		UPDATE accounts SET failure_count = failure_count + ?
		WHERE id = ?
	`, count, id)
	return err
}

func (s *Store) GetSetting(key string) (string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var value string
	err := s.db.QueryRow("SELECT value FROM settings WHERE key = ?", key).Scan(&value)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return value, err
}

func (s *Store) SetSetting(key, value string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	_, err := s.db.Exec(`
		INSERT INTO settings (key, value) VALUES (?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value
	`, key, value)
	return err
}
