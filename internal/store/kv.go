package store

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
)

const kvSchema = `
CREATE TABLE IF NOT EXISTS kv (
	key   TEXT PRIMARY KEY,
	value BLOB NOT NULL,
	nonce BLOB NOT NULL
);
`

// initKV ensures the kv table exists (called from New).
func initKV(db *sql.DB) error {
	_, err := db.Exec(kvSchema)
	return err
}

// machineKey derives a 32-byte AES key from the machine fingerprint.
// Uses hostname + OS username + a fixed app constant, hashed with SHA-256.
// This is not a secret key — it just prevents the file from being copied
// verbatim to another machine and used as-is.
func machineKey() []byte {
	hostname, _ := os.Hostname()
	user := os.Getenv("USER")
	if user == "" {
		user = os.Getenv("USERNAME") // Windows fallback
	}
	const appConstant = "billy.sh-v1-license-store"
	h := sha256.Sum256([]byte(fmt.Sprintf("%s|%s|%s", hostname, user, appConstant)))
	return h[:]
}

// SetEncrypted encrypts plaintext with AES-256-GCM and stores it in the kv table.
func (s *Store) SetEncrypted(key string, plaintext []byte) error {
	block, err := aes.NewCipher(machineKey())
	if err != nil {
		return err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err = io.ReadFull(rand.Reader, nonce); err != nil {
		return err
	}
	ciphertext := gcm.Seal(nil, nonce, plaintext, nil)

	_, err = s.db.Exec(
		`INSERT INTO kv (key, value, nonce) VALUES (?, ?, ?)
		 ON CONFLICT(key) DO UPDATE SET value=excluded.value, nonce=excluded.nonce`,
		key, ciphertext, nonce,
	)
	return err
}

// GetEncrypted retrieves and decrypts a value from the kv table.
// Returns nil, nil if the key does not exist.
func (s *Store) GetEncrypted(key string) ([]byte, error) {
	var ciphertext, nonce []byte
	err := s.db.QueryRow(`SELECT value, nonce FROM kv WHERE key = ?`, key).Scan(&ciphertext, &nonce)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	block, err := aes.NewCipher(machineKey())
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return nil, errors.New("license key decryption failed (wrong machine?)")
	}
	return plaintext, nil
}
