package license

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

const lsAPIBase = "https://api.lemonsqueezy.com/v1/licenses"

// VariantTier maps LemonSqueezy variant IDs to license tiers.
var VariantTier = map[int]Tier{
	// Test-mode variants
	1408429: TierPro,
	1408393: TierPremium,
	1408425: TierTeam,
	1408426: TierTeam,
	1408428: TierTeam,
	// Live-mode variants
	1420712: TierPro,
	1420713: TierPremium,
	1420715: TierTeam,
	1420716: TierTeam,
	1420717: TierTeam,
}

// VariantSeats maps team variant IDs to seat counts.
var VariantSeats = map[int]int{
	1408425: 5,
	1408426: 10,
	1408428: 25,
	1420715: 5,
	1420716: 10,
	1420717: 25,
}

// Activation is the encrypted blob stored locally after a successful activation.
type Activation struct {
	Key         string    `json:"key"`
	InstanceID  string    `json:"instance_id"`
	Tier        Tier      `json:"tier"`
	Seats       int       `json:"seats,omitempty"`
	Email       string    `json:"email,omitempty"`
	ValidatedAt time.Time `json:"validated_at"`
}

func (a *Activation) Marshal() ([]byte, error) {
	return json.Marshal(a)
}

func UnmarshalActivation(data []byte) (*Activation, error) {
	var a Activation
	if err := json.Unmarshal(data, &a); err != nil {
		return nil, err
	}
	return &a, nil
}

func (a *Activation) ToLicense() *License {
	return &License{
		Email:    a.Email,
		Tier:     a.Tier,
		Seats:    a.Seats,
		IssuedAt: a.ValidatedAt,
	}
}

// LS API response types

type lsLicenseKey struct {
	Status          string  `json:"status"`
	ActivationLimit int     `json:"activation_limit"`
	ActivationUsage int     `json:"activation_usage"`
	VariantID       int     `json:"variant_id"`
	ExpiresAt       *string `json:"expires_at"` // null = lifetime
}

type lsInstance struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"created_at"`
}

type lsMeta struct {
	CustomerEmail string `json:"customer_email"`
	VariantID     int    `json:"variant_id"`
}

type lsActivateResponse struct {
	Activated  bool         `json:"activated"`
	Error      string       `json:"error,omitempty"`
	Instance   lsInstance   `json:"instance"`
	LicenseKey lsLicenseKey `json:"license_key"`
	Meta       lsMeta       `json:"meta"`
}

type lsValidateResponse struct {
	Valid      bool         `json:"valid"`
	Error      string       `json:"error,omitempty"`
	Instance   lsInstance   `json:"instance"`
	LicenseKey lsLicenseKey `json:"license_key"`
	Meta       lsMeta       `json:"meta"`
}

type lsDeactivateResponse struct {
	Deactivated bool   `json:"deactivated"`
	Error       string `json:"error,omitempty"`
}

func lsPost(endpoint string, form url.Values) ([]byte, error) {
	req, err := http.NewRequest("POST", lsAPIBase+endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("network error: %w", err)
	}
	defer resp.Body.Close()
	return io.ReadAll(resp.Body)
}

// Activate calls the LS API to activate a license key on this machine.
// Returns the License, instanceID, and any error.
func Activate(licenseKey, instanceName string) (*License, string, error) {
	body, err := lsPost("/activate", url.Values{
		"license_key":   {licenseKey},
		"instance_name": {instanceName},
	})
	if err != nil {
		return nil, "", err
	}

	var result lsActivateResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, "", fmt.Errorf("invalid response from license server")
	}
	if !result.Activated {
		msg := result.Error
		if msg == "" {
			msg = "activation failed"
		}
		return nil, "", fmt.Errorf("%s", msg)
	}

	variantID := result.LicenseKey.VariantID
	if variantID == 0 {
		variantID = result.Meta.VariantID
	}
	tier := VariantTier[variantID]
	if tier == "" {
		tier = TierPro
	}
	seats := VariantSeats[variantID]

	var expiry time.Time
	if result.LicenseKey.ExpiresAt != nil && *result.LicenseKey.ExpiresAt != "" {
		expiry, _ = time.Parse(time.RFC3339, *result.LicenseKey.ExpiresAt)
	}

	lic := &License{
		Email:    result.Meta.CustomerEmail,
		Tier:     tier,
		Seats:    seats,
		Expiry:   expiry,
		IssuedAt: result.Instance.CreatedAt,
	}
	return lic, result.Instance.ID, nil
}

// Validate checks that a license key + instance ID are still active with LS.
func Validate(licenseKey, instanceID string) (*License, error) {
	body, err := lsPost("/validate", url.Values{
		"license_key": {licenseKey},
		"instance_id": {instanceID},
	})
	if err != nil {
		return nil, err
	}

	var result lsValidateResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("invalid response from license server")
	}
	if !result.Valid {
		msg := result.Error
		if msg == "" {
			msg = "license invalid or deactivated"
		}
		return nil, fmt.Errorf("%s", msg)
	}

	variantID := result.LicenseKey.VariantID
	if variantID == 0 {
		variantID = result.Meta.VariantID
	}
	tier := VariantTier[variantID]
	if tier == "" {
		tier = TierPro
	}
	seats := VariantSeats[variantID]

	var expiry time.Time
	if result.LicenseKey.ExpiresAt != nil && *result.LicenseKey.ExpiresAt != "" {
		expiry, _ = time.Parse(time.RFC3339, *result.LicenseKey.ExpiresAt)
	}

	lic := &License{
		Email:    result.Meta.CustomerEmail,
		Tier:     tier,
		Seats:    seats,
		Expiry:   expiry,
		IssuedAt: result.Instance.CreatedAt,
	}
	return lic, nil
}

// Deactivate removes this machine's activation seat from LS.
func Deactivate(licenseKey, instanceID string) error {
	body, err := lsPost("/deactivate", url.Values{
		"license_key": {licenseKey},
		"instance_id": {instanceID},
	})
	if err != nil {
		return err
	}

	var result lsDeactivateResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return fmt.Errorf("invalid response")
	}
	if !result.Deactivated {
		msg := result.Error
		if msg == "" {
			msg = "deactivation failed"
		}
		return fmt.Errorf("%s", msg)
	}
	return nil
}

// InstanceName returns a stable machine identifier for LS activation records.
func InstanceName() string {
	if h, err := os.Hostname(); err == nil && h != "" {
		return h
	}
	return "unknown"
}
