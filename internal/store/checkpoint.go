package store

import (
	"database/sql"
	"errors"
	"time"
)

const checkpointSchema = `
CREATE TABLE IF NOT EXISTS checkpoints (
    id              TEXT PRIMARY KEY,
    conversation_id TEXT NOT NULL,
    name            TEXT NOT NULL,
    summary         TEXT NOT NULL,
    message_count   INTEGER NOT NULL,
    created_at      TIMESTAMP NOT NULL
);
`

// Checkpoint is a named snapshot of a conversation with an AI-generated summary.
type Checkpoint struct {
	ID             string
	ConversationID string
	Name           string
	Summary        string
	MessageCount   int
	CreatedAt      time.Time
}

func initCheckpoints(db *sql.DB) error {
	_, err := db.Exec(checkpointSchema)
	return err
}

// SaveCheckpoint inserts or updates a checkpoint record.
func (s *Store) SaveCheckpoint(id, convID, name, summary string, msgCount int) error {
	_, err := s.db.Exec(
		`INSERT INTO checkpoints (id, conversation_id, name, summary, message_count, created_at) VALUES (?, ?, ?, ?, ?, ?)
         ON CONFLICT(id) DO UPDATE SET name=excluded.name, summary=excluded.summary`,
		id, convID, name, summary, msgCount, time.Now(),
	)
	return err
}

// ListCheckpoints returns all checkpoints for a conversation, newest first.
func (s *Store) ListCheckpoints(convID string) ([]Checkpoint, error) {
	rows, err := s.db.Query(
		`SELECT id, conversation_id, name, summary, message_count, created_at FROM checkpoints WHERE conversation_id = ? ORDER BY created_at DESC`,
		convID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Checkpoint
	for rows.Next() {
		var c Checkpoint
		if err := rows.Scan(&c.ID, &c.ConversationID, &c.Name, &c.Summary, &c.MessageCount, &c.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// GetCheckpointByName returns the most recent checkpoint with the given name.
func (s *Store) GetCheckpointByName(name string) (*Checkpoint, error) {
	var c Checkpoint
	err := s.db.QueryRow(
		`SELECT id, conversation_id, name, summary, message_count, created_at FROM checkpoints WHERE name = ? ORDER BY created_at DESC LIMIT 1`,
		name,
	).Scan(&c.ID, &c.ConversationID, &c.Name, &c.Summary, &c.MessageCount, &c.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return &c, err
}

// AllCheckpoints returns every checkpoint across all conversations, newest first.
func (s *Store) AllCheckpoints() ([]Checkpoint, error) {
	rows, err := s.db.Query(
		`SELECT id, conversation_id, name, summary, message_count, created_at FROM checkpoints ORDER BY created_at DESC`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Checkpoint
	for rows.Next() {
		var c Checkpoint
		if err := rows.Scan(&c.ID, &c.ConversationID, &c.Name, &c.Summary, &c.MessageCount, &c.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}
