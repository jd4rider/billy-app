package license

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

const PublicKeyB64 = "lqE+Vy7TsRUnObQu32V93SNU/Gq2hCOp1mOg28JfO+g="

type Tier string

const (
	TierFree       Tier = "free"
	TierPro        Tier = "pro"
	TierPremium    Tier = "premium"
	TierTeam       Tier = "team"
	TierEnterprise Tier = "enterprise"
)

type License struct {
	Email    string    `json:"email"`
	Tier     Tier      `json:"tier"`
	Expiry   time.Time `json:"expiry"`            // zero = lifetime
	IssuedAt time.Time `json:"issued_at"`
	Seats    int       `json:"seats,omitempty"` // for team licenses
}

// Badge returns the display label for the status bar
func (l *License) Badge() string {
	switch l.Tier {
	case TierPro:
		return "[PRO]"
	case TierPremium:
		return "[PREMIUM]"
	case TierTeam:
		return "[TEAM]"
	case TierEnterprise:
		return "[ENTERPRISE]"
	default:
		return "[FREE]"
	}
}

// IsActive returns true if the license has not expired
func (l *License) IsActive() bool {
	if l.Expiry.IsZero() {
		return true
	}
	return time.Now().Before(l.Expiry)
}

// EffectiveTier returns TierFree if expired, otherwise Tier
func (l *License) EffectiveTier() Tier {
	if !l.IsActive() {
		return TierFree
	}
	return l.Tier
}

// Free returns true if the user has no paid tier
func (l *License) Free() bool {
	return l.EffectiveTier() == TierFree
}

// Parse decodes and verifies a BILLY-xxx license key
func Parse(key string) (*License, error) {
	key = strings.TrimSpace(key)
	if !strings.HasPrefix(key, "BILLY-") {
		return nil, errors.New("invalid license key format")
	}
	raw, err := base64.URLEncoding.DecodeString(strings.TrimPrefix(key, "BILLY-"))
	if err != nil {
		return nil, errors.New("invalid license key encoding")
	}
	if len(raw) < ed25519.SignatureSize {
		return nil, errors.New("license key too short")
	}
	sig := raw[:ed25519.SignatureSize]
	payload := raw[ed25519.SignatureSize:]

	pubBytes, err := base64.StdEncoding.DecodeString(PublicKeyB64)
	if err != nil {
		return nil, errors.New("embedded public key corrupt")
	}
	if !ed25519.Verify(pubBytes, payload, sig) {
		return nil, errors.New("license signature invalid")
	}

	var lic License
	if err := json.Unmarshal(payload, &lic); err != nil {
		return nil, errors.New("license payload corrupt")
	}
	return &lic, nil
}
