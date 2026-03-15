package store

import (
	"database/sql"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

const schema = `
CREATE TABLE IF NOT EXISTS conversations (
	id         TEXT PRIMARY KEY,
	title      TEXT NOT NULL,
	model      TEXT NOT NULL,
	created_at TIMESTAMP NOT NULL,
	updated_at TIMESTAMP NOT NULL
);

CREATE TABLE IF NOT EXISTS messages (
	id              TEXT PRIMARY KEY,
	conversation_id TEXT NOT NULL,
	role            TEXT NOT NULL,
	content         TEXT NOT NULL,
	created_at      TIMESTAMP NOT NULL,
	FOREIGN KEY(conversation_id) REFERENCES conversations(id)
);
`

// Message stored in the database.
type Message struct {
	ID             string
	ConversationID string
	Role           string
	Content        string
	CreatedAt      time.Time
}

// Conversation stored in the database.
type Conversation struct {
	ID        string
	Title     string
	Model     string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// Store manages conversation history.
type Store struct {
	db *sql.DB
}

// New opens (or creates) the SQLite database at the given path.
func New(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return nil, err
	}

	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}

	if _, err := db.Exec(schema); err != nil {
		return nil, err
	}

	if err := initMemories(db); err != nil {
		return nil, err
	}

	return &Store{db: db}, nil
}

// Close closes the underlying database connection.
func (s *Store) Close() error {
	return s.db.Close()
}

// CreateConversation inserts a new conversation record.
func (s *Store) CreateConversation(id, title, model string) error {
	now := time.Now()
	_, err := s.db.Exec(
		`INSERT INTO conversations (id, title, model, created_at, updated_at) VALUES (?, ?, ?, ?, ?)`,
		id, title, model, now, now,
	)
	return err
}

// AddMessage appends a message to a conversation.
func (s *Store) AddMessage(msgID, convID, role, content string) error {
	_, err := s.db.Exec(
		`INSERT INTO messages (id, conversation_id, role, content, created_at) VALUES (?, ?, ?, ?, ?)`,
		msgID, convID, role, content, time.Now(),
	)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(
		`UPDATE conversations SET updated_at = ? WHERE id = ?`,
		time.Now(), convID,
	)
	return err
}

// GetMessages returns all messages for a conversation in order.
func (s *Store) GetMessages(convID string) ([]Message, error) {
	rows, err := s.db.Query(
		`SELECT id, conversation_id, role, content, created_at FROM messages WHERE conversation_id = ? ORDER BY created_at ASC`,
		convID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var msgs []Message
	for rows.Next() {
		var m Message
		if err := rows.Scan(&m.ID, &m.ConversationID, &m.Role, &m.Content, &m.CreatedAt); err != nil {
			return nil, err
		}
		msgs = append(msgs, m)
	}
	return msgs, rows.Err()
}

// ListConversations returns all conversations ordered by most recently updated.
func (s *Store) ListConversations() ([]Conversation, error) {
	rows, err := s.db.Query(
		`SELECT id, title, model, created_at, updated_at FROM conversations ORDER BY updated_at DESC`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var convs []Conversation
	for rows.Next() {
		var c Conversation
		if err := rows.Scan(&c.ID, &c.Title, &c.Model, &c.CreatedAt, &c.UpdatedAt); err != nil {
			return nil, err
		}
		convs = append(convs, c)
	}
	return convs, rows.Err()
}
