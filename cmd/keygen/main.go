// keygen is a local tool for generating Billy license keys.
// Usage: BILLY_PRIVATE_KEY=<b64> go run ./cmd/keygen -email user@example.com -tier pro [-expiry 2027-01-01] [-seats 10]
package main

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/jonathanforrider/billy/internal/license"
)

func main() {
	email := flag.String("email", "", "Customer email")
	tier := flag.String("tier", "pro", "License tier: pro|premium|team|enterprise")
	expiry := flag.String("expiry", "", "Expiry date YYYY-MM-DD (empty = lifetime)")
	seats := flag.Int("seats", 0, "Seats for team licenses")
	flag.Parse()

	if *email == "" {
		fmt.Fprintln(os.Stderr, "error: -email required")
		os.Exit(1)
	}

	privB64 := os.Getenv("BILLY_PRIVATE_KEY")
	if privB64 == "" {
		fmt.Fprintln(os.Stderr, "error: BILLY_PRIVATE_KEY env var required")
		os.Exit(1)
	}
	privBytes, err := base64.StdEncoding.DecodeString(privB64)
	if err != nil || len(privBytes) != ed25519.PrivateKeySize {
		fmt.Fprintln(os.Stderr, "error: invalid BILLY_PRIVATE_KEY")
		os.Exit(1)
	}

	lic := license.License{
		Email:    *email,
		Tier:     license.Tier(*tier),
		IssuedAt: time.Now().UTC(),
		Seats:    *seats,
	}
	if *expiry != "" {
		t, err := time.Parse("2006-01-02", *expiry)
		if err != nil {
			fmt.Fprintln(os.Stderr, "error: invalid expiry format, use YYYY-MM-DD")
			os.Exit(1)
		}
		lic.Expiry = t
	}

	payload, err := json.Marshal(lic)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}

	sig := ed25519.Sign(privBytes, payload)
	raw := append(sig, payload...)
	key := "BILLY-" + base64.URLEncoding.EncodeToString(raw)
	fmt.Println(key)
}
