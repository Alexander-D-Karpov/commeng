package auth

import (
	"crypto/sha256"
	"encoding/hex"
)

// TokenHash never exposes the raw session or visitor token to the repository.
func TokenHash(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}
