package storage

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	_ "modernc.org/sqlite"
)

type SQLiteStore struct {
	db *sql.DB
}

func NewSQLiteStore(path string) (*SQLiteStore, error) {
	db, err := sql.Open("sqlite", path+"?_journal_mode=WAL")
	if err != nil {
		return nil, fmt.Errorf("opening sqlite: %w", err)
	}

	store := &SQLiteStore{db: db}
	if err := store.migrate(); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrating: %w", err)
	}

	return store, nil
}

func (s *SQLiteStore) Close() error {
	return s.db.Close()
}

func (s *SQLiteStore) migrate() error {
	_, err := s.db.Exec(`
		CREATE TABLE IF NOT EXISTS sessions (
			id TEXT PRIMARY KEY,
			title TEXT NOT NULL DEFAULT '',
			memory TEXT NOT NULL DEFAULT '',
			directory TEXT NOT NULL,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		);

		CREATE TABLE IF NOT EXISTS messages (
			id TEXT PRIMARY KEY,
			session_id TEXT NOT NULL,
			role TEXT NOT NULL,
			content TEXT NOT NULL,
			usage_input INTEGER DEFAULT 0,
			usage_output INTEGER DEFAULT 0,
			created_at TEXT NOT NULL,
			FOREIGN KEY (session_id) REFERENCES sessions(id)
		);

		CREATE INDEX IF NOT EXISTS idx_messages_session ON messages(session_id, created_at);
	`)
	if err != nil {
		return err
	}
	return s.ensureSessionColumn("memory", "TEXT NOT NULL DEFAULT ''")
}

func (s *SQLiteStore) ensureSessionColumn(name, definition string) error {
	rows, err := s.db.Query("PRAGMA table_info(sessions)")
	if err != nil {
		return err
	}
	defer rows.Close()

	for rows.Next() {
		var cid int
		var colName, colType string
		var notNull, pk int
		var defaultValue sql.NullString
		if err := rows.Scan(&cid, &colName, &colType, &notNull, &defaultValue, &pk); err != nil {
			return err
		}
		if colName == name {
			return nil
		}
	}

	_, err = s.db.Exec(fmt.Sprintf("ALTER TABLE sessions ADD COLUMN %s %s", name, definition))
	return err
}

// Session operations

type SessionRow struct {
	ID        string
	Title     string
	Memory    string
	Directory string
	CreatedAt time.Time
	UpdatedAt time.Time
}

func (s *SQLiteStore) CreateSession(sess *SessionRow) error {
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := s.db.Exec(
		"INSERT INTO sessions (id, title, memory, directory, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?)",
		sess.ID, sess.Title, sess.Memory, sess.Directory, now, now,
	)
	return err
}

func (s *SQLiteStore) GetSession(id string) (*SessionRow, error) {
	row := s.db.QueryRow("SELECT id, title, memory, directory, created_at, updated_at FROM sessions WHERE id = ?", id)
	sess := &SessionRow{}
	var createdAt, updatedAt string
	if err := row.Scan(&sess.ID, &sess.Title, &sess.Memory, &sess.Directory, &createdAt, &updatedAt); err != nil {
		return nil, err
	}
	sess.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
	sess.UpdatedAt, _ = time.Parse(time.RFC3339, updatedAt)
	return sess, nil
}

func (s *SQLiteStore) UpdateSessionTitle(id, title string) error {
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := s.db.Exec("UPDATE sessions SET title = ?, updated_at = ? WHERE id = ?", title, now, id)
	return err
}

func (s *SQLiteStore) UpdateSessionMemory(id, memory string) error {
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := s.db.Exec("UPDATE sessions SET memory = ?, updated_at = ? WHERE id = ?", memory, now, id)
	return err
}

func (s *SQLiteStore) DeleteSession(id string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.Exec("DELETE FROM messages WHERE session_id = ?", id); err != nil {
		return err
	}
	if _, err := tx.Exec("DELETE FROM sessions WHERE id = ?", id); err != nil {
		return err
	}

	return tx.Commit()
}

func (s *SQLiteStore) ListSessions() ([]*SessionRow, error) {
	rows, err := s.db.Query("SELECT id, title, memory, directory, created_at, updated_at FROM sessions ORDER BY updated_at DESC LIMIT 50")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var sessions []*SessionRow
	for rows.Next() {
		sess := &SessionRow{}
		var createdAt, updatedAt string
		if err := rows.Scan(&sess.ID, &sess.Title, &sess.Memory, &sess.Directory, &createdAt, &updatedAt); err != nil {
			continue
		}
		sess.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
		sess.UpdatedAt, _ = time.Parse(time.RFC3339, updatedAt)
		sessions = append(sessions, sess)
	}
	return sessions, nil
}

func (s *SQLiteStore) FindSessionByDir(dir string) (*SessionRow, error) {
	row := s.db.QueryRow(
		"SELECT id, title, memory, directory, created_at, updated_at FROM sessions WHERE directory = ? ORDER BY updated_at DESC LIMIT 1",
		dir,
	)
	sess := &SessionRow{}
	var createdAt, updatedAt string
	if err := row.Scan(&sess.ID, &sess.Title, &sess.Memory, &sess.Directory, &createdAt, &updatedAt); err != nil {
		return nil, err
	}
	sess.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
	sess.UpdatedAt, _ = time.Parse(time.RFC3339, updatedAt)
	return sess, nil
}

// Message operations

type MessageRow struct {
	ID          string
	SessionID   string
	Role        string
	Content     json.RawMessage
	UsageInput  int
	UsageOutput int
	CreatedAt   time.Time
}

func (s *SQLiteStore) AddMessage(msg *MessageRow) error {
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := s.db.Exec(
		"INSERT INTO messages (id, session_id, role, content, usage_input, usage_output, created_at) VALUES (?, ?, ?, ?, ?, ?, ?)",
		msg.ID, msg.SessionID, msg.Role, string(msg.Content), msg.UsageInput, msg.UsageOutput, now,
	)
	if err == nil {
		s.db.Exec("UPDATE sessions SET updated_at = ? WHERE id = ?", now, msg.SessionID)
	}
	return err
}

func (s *SQLiteStore) GetMessages(sessionID string) ([]*MessageRow, error) {
	rows, err := s.db.Query(
		"SELECT id, session_id, role, content, usage_input, usage_output, created_at FROM messages WHERE session_id = ? ORDER BY created_at ASC",
		sessionID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var messages []*MessageRow
	for rows.Next() {
		msg := &MessageRow{}
		var content string
		var createdAt string
		if err := rows.Scan(&msg.ID, &msg.SessionID, &msg.Role, &content, &msg.UsageInput, &msg.UsageOutput, &createdAt); err != nil {
			continue
		}
		msg.Content = json.RawMessage(content)
		msg.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
		messages = append(messages, msg)
	}
	return messages, nil
}

func (s *SQLiteStore) ReplaceMessages(sessionID string, messages []MessageRow) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.Exec("DELETE FROM messages WHERE session_id = ?", sessionID); err != nil {
		return err
	}

	now := time.Now().UTC()
	for i, msg := range messages {
		createdAt := now.Add(time.Duration(i) * time.Millisecond).Format(time.RFC3339)
		if _, err := tx.Exec(
			"INSERT INTO messages (id, session_id, role, content, usage_input, usage_output, created_at) VALUES (?, ?, ?, ?, ?, ?, ?)",
			msg.ID, sessionID, msg.Role, string(msg.Content), msg.UsageInput, msg.UsageOutput, createdAt,
		); err != nil {
			return err
		}
	}

	if _, err := tx.Exec("UPDATE sessions SET updated_at = ? WHERE id = ?", now.Format(time.RFC3339), sessionID); err != nil {
		return err
	}

	return tx.Commit()
}
