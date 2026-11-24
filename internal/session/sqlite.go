package session

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"

	_ "modernc.org/sqlite"
)

const createSessionsTable = `
CREATE TABLE IF NOT EXISTS sessions (
	user_id TEXT NOT NULL,
	session_id TEXT NOT NULL,
	state TEXT NOT NULL DEFAULT '{}',
	revision INTEGER NOT NULL DEFAULT 0,
	PRIMARY KEY (user_id, session_id)
)`

// SQLiteStore 在 SQLite 中保存最近提交的会话快照。
type SQLiteStore struct {
	db *sql.DB

	mu    sync.Mutex
	turns map[SessionKey]*turnLock
}

type turnLock struct {
	held chan struct{}
	refs int
}

var _ SessionStore = (*SQLiteStore)(nil)

func OpenSQLiteStore(ctx context.Context, path string) (*SQLiteStore, error) {
	if path == "" {
		return nil, errors.New("SQLite database path is required")
	}
	path, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("resolve SQLite path: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("create SQLite directory: %w", err)
	}
	uriPath := filepath.ToSlash(path)
	if !strings.HasPrefix(uriPath, "/") {
		uriPath = "/" + uriPath
	}
	dsn := url.URL{Scheme: "file", Path: uriPath, RawQuery: "_pragma=busy_timeout(5000)"}
	db, err := sql.Open("sqlite", dsn.String())
	if err != nil {
		return nil, fmt.Errorf("open SQLite: %w", err)
	}
	db.SetMaxOpenConns(1)
	if _, err := db.ExecContext(ctx, "PRAGMA journal_mode = WAL"); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("initialize SQLite journal: %w", err)
	}
	if _, err := db.ExecContext(ctx, createSessionsTable); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("initialize sessions table: %w", err)
	}
	return &SQLiteStore{db: db, turns: make(map[SessionKey]*turnLock)}, nil
}

func (s *SQLiteStore) Close() error {
	return s.db.Close()
}

func (s *SQLiteStore) Create(ctx context.Context, key SessionKey) error {
	result, err := s.db.ExecContext(ctx, `
		INSERT INTO sessions (user_id, session_id) VALUES (?, ?)
		ON CONFLICT (user_id, session_id) DO NOTHING`, key.UserID, key.SessionID)
	if err != nil {
		return fmt.Errorf("insert session: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read session insert result: %w", err)
	}
	if rows == 0 {
		return ErrSessionExists
	}
	return nil
}

func (s *SQLiteStore) Load(ctx context.Context, key SessionKey) (Session, error) {
	state, _, err := s.load(ctx, key)
	return state, err
}

func (s *SQLiteStore) load(ctx context.Context, key SessionKey) (Session, int64, error) {
	var data string
	var revision int64
	err := s.db.QueryRowContext(ctx, `
		SELECT state, revision FROM sessions WHERE user_id = ? AND session_id = ?`,
		key.UserID, key.SessionID).Scan(&data, &revision)
	if errors.Is(err, sql.ErrNoRows) {
		return Session{}, 0, ErrSessionNotFound
	}
	if err != nil {
		return Session{}, 0, fmt.Errorf("read session: %w", err)
	}
	state, err := decodeSnapshot([]byte(data))
	if err != nil {
		return Session{}, 0, fmt.Errorf("decode session snapshot: %w", err)
	}
	return state, revision, nil
}

func (s *SQLiteStore) Update(ctx context.Context, key SessionKey, update func(*Session) error) error {
	lock, err := s.acquire(ctx, key)
	if err != nil {
		return err
	}
	defer func() {
		<-lock.held
		s.release(key, lock)
	}()

	state, revision, err := s.load(ctx, key)
	if errors.Is(err, ErrSessionNotFound) {
		if err := s.Create(ctx, key); err != nil && !errors.Is(err, ErrSessionExists) {
			return err
		}
		state, revision, err = s.load(ctx, key)
	}
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	// 模型和工具运行期间只锁定当前会话，SQLite 连接可供其他会话使用。
	if err := update(&state); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	data, err := encodeSnapshot(state)
	if err != nil {
		return fmt.Errorf("encode session snapshot: %w", err)
	}
	// 版本条件防止另外一个进程已经提交的结果被覆盖。
	result, err := s.db.ExecContext(ctx, `
		UPDATE sessions SET state = ?, revision = revision + 1
		WHERE user_id = ? AND session_id = ? AND revision = ?`,
		string(data), key.UserID, key.SessionID, revision)
	if err != nil {
		return fmt.Errorf("commit session: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read session update result: %w", err)
	}
	if rows == 0 {
		return ErrSessionConflict
	}
	return nil
}

func (s *SQLiteStore) acquire(ctx context.Context, key SessionKey) (*turnLock, error) {
	s.mu.Lock()
	lock := s.turns[key]
	if lock == nil {
		lock = &turnLock{held: make(chan struct{}, 1)}
		s.turns[key] = lock
	}
	lock.refs++
	s.mu.Unlock()

	select {
	case lock.held <- struct{}{}:
		return lock, nil
	case <-ctx.Done():
		s.release(key, lock)
		return nil, ctx.Err()
	}
}

func (s *SQLiteStore) release(key SessionKey, lock *turnLock) {
	s.mu.Lock()
	defer s.mu.Unlock()
	lock.refs--
	if lock.refs == 0 {
		delete(s.turns, key)
	}
}
