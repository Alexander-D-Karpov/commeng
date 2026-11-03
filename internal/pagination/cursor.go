// Package pagination encodes scoped, signed keyset cursors. A cursor is not an
// authorization credential, but integrity prevents mixing collections.
package pagination

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"

	"github.com/Alexander-D-Karpov/commeng/internal/domain"
)

type Cursor struct {
	Version  int         `json:"v"`
	Kind     string      `json:"k"`
	PostID   int64       `json:"p,omitempty"`
	ParentID int64       `json:"parent,omitempty"`
	Order    domain.Sort `json:"o,omitempty"`
	Scope    string      `json:"scope,omitempty"`
	LastID   int64       `json:"last"`
}
type Signer struct{ secret []byte }

func NewSigner(secret []byte) (*Signer, error) {
	if len(secret) < 32 {
		return nil, errors.New("cursor signing secret must contain at least 32 bytes")
	}
	return &Signer{secret: append([]byte(nil), secret...)}, nil
}
func (s *Signer) Encode(cursor Cursor) string {
	cursor.Version = 1
	raw, _ := json.Marshal(cursor)
	data := base64.RawURLEncoding.EncodeToString(raw)
	mac := hmac.New(sha256.New, s.secret)
	_, _ = mac.Write([]byte(data))
	return data + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}
func (s *Signer) Decode(token string, expected Cursor) (int64, error) {
	data, signature, ok := strings.Cut(token, ".")
	if !ok || len(token) > 2048 {
		return 0, domain.Fault(domain.ErrCursor, "invalid cursor")
	}
	signatureBytes, err := base64.RawURLEncoding.DecodeString(signature)
	if err != nil {
		return 0, domain.Fault(domain.ErrCursor, "invalid cursor")
	}
	mac := hmac.New(sha256.New, s.secret)
	_, _ = mac.Write([]byte(data))
	if !hmac.Equal(signatureBytes, mac.Sum(nil)) {
		return 0, domain.Fault(domain.ErrCursor, "invalid cursor")
	}
	raw, err := base64.RawURLEncoding.DecodeString(data)
	if err != nil {
		return 0, domain.Fault(domain.ErrCursor, "invalid cursor")
	}
	var actual Cursor
	if err := json.Unmarshal(raw, &actual); err != nil {
		return 0, domain.Fault(domain.ErrCursor, "invalid cursor")
	}
	wrongCollection := actual.Kind != expected.Kind ||
		actual.PostID != expected.PostID ||
		actual.ParentID != expected.ParentID ||
		actual.Order != expected.Order || actual.Scope != expected.Scope
	if actual.Version != 1 || wrongCollection || actual.LastID <= 0 {
		return 0, domain.Fault(domain.ErrCursor, "cursor belongs to another collection")
	}
	return actual.LastID, nil
}
