package store

import (
	"database/sql"
	"time"
)

const memorySchema = `
CREATE TABLE IF NOT EXISTS memories (
	id         TEXT PRIMARY KEY,
	content    TEXT NOT NULL,
	created_at TIMESTAMP NOT NULL
);
`

// Memory is a single remembered fact.
type Memory struct {
	ID        string
	Content   string
	CreatedAt time.Time
}

// initMemories ensures the memories table exists (called from New).
func initMemories(db *sql.DB) error {
	_, err := db.Exec(memorySchema)
	return err
}

// SaveMemory stores a new memory.
func (s *Store) SaveMemory(id, content string) error {
	_, err := s.db.Exec(
		`INSERT OR REPLACE INTO memories (id, content, created_at) VALUES (?, ?, ?)`,
		id, content, time.Now(),
	)
	return err
}

// ListMemories returns all memories ordered by creation time.
func (s *Store) ListMemories() ([]Memory, error) {
	rows, err := s.db.Query(
		`SELECT id, content, created_at FROM memories ORDER BY created_at ASC`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var mems []Memory
	for rows.Next() {
		var m Memory
		if err := rows.Scan(&m.ID, &m.Content, &m.CreatedAt); err != nil {
			return nil, err
		}
		mems = append(mems, m)
	}
	return mems, rows.Err()
}

// ForgetMemory deletes a memory by ID prefix (matches first result).
func (s *Store) ForgetMemory(idPrefix string) (bool, error) {
	// Find full ID by prefix
	var fullID string
	err := s.db.QueryRow(
		`SELECT id FROM memories WHERE id LIKE ? LIMIT 1`,
		idPrefix+"%",
	).Scan(&fullID)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	_, err = s.db.Exec(`DELETE FROM memories WHERE id = ?`, fullID)
	return err == nil, err
}

// ClearMemories deletes all memories.
func (s *Store) ClearMemories() error {
	_, err := s.db.Exec(`DELETE FROM memories`)
	return err
}
