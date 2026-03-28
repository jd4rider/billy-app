package license

import "time"

type Tier string

const (
	TierFree       Tier = "free"
	TierPro        Tier = "pro"
	TierPremium    Tier = "premium"
	TierTeam       Tier = "team"
	TierEnterprise Tier = "enterprise"
)

type License struct {
	Email    string
	Tier     Tier
	Expiry   time.Time // zero = lifetime
	IssuedAt time.Time
	Seats    int
}

// Badge returns the display label for the status bar.
func (l *License) Badge() string {
	return "[OPEN]"
}

// IsActive returns true if the license has not expired.
func (l *License) IsActive() bool {
	if l.Expiry.IsZero() {
		return true
	}
	return time.Now().Before(l.Expiry)
}

// EffectiveTier returns TierFree if expired, otherwise Tier.
func (l *License) EffectiveTier() Tier {
	if !l.IsActive() {
		return TierFree
	}
	return l.Tier
}

// Free returns true if the user has no paid tier.
func (l *License) Free() bool {
	return l.EffectiveTier() == TierFree
}
