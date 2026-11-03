package auth

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"strings"

	"github.com/Alexander-D-Karpov/commeng/internal/domain"
)

type ctxKey struct{}
type Viewer struct {
	User        *domain.User
	ActorKey    string
	SessionHash string
}

func WithViewer(ctx context.Context, viewer Viewer) context.Context {
	return context.WithValue(ctx, ctxKey{}, viewer)
}
func FromContext(ctx context.Context) Viewer {
	viewer, _ := ctx.Value(ctxKey{}).(Viewer)
	return viewer
}
func RequiredUser(ctx context.Context) (domain.User, error) {
	viewer := FromContext(ctx)
	if viewer.User == nil {
		return domain.User{}, domain.Fault(domain.ErrAuth, "login required")
	}
	return *viewer.User, nil
}

type Visitors struct{ secret []byte }

func NewVisitors(secret []byte) (*Visitors, error) {
	if len(secret) < 32 {
		return nil, errors.New("signing secret is too short")
	}
	return &Visitors{secret: append([]byte(nil), secret...)}, nil
}
func (v *Visitors) Issue() (string, error) {
	random := make([]byte, 32)
	if _, err := rand.Read(random); err != nil {
		return "", err
	}
	identity := base64.RawURLEncoding.EncodeToString(random)
	mac := hmac.New(sha256.New, v.secret)
	_, _ = mac.Write([]byte(identity))
	return identity + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil)), nil
}
func (v *Visitors) Verify(token string) (string, error) {
	identity, signature, ok := strings.Cut(token, ".")
	if !ok || len(identity) != 43 {
		return "", domain.Fault(domain.ErrAuth, "invalid visitor token")
	}
	sig, err := base64.RawURLEncoding.DecodeString(signature)
	if err != nil {
		return "", domain.Fault(domain.ErrAuth, "invalid visitor token")
	}
	mac := hmac.New(sha256.New, v.secret)
	_, _ = mac.Write([]byte(identity))
	if !hmac.Equal(mac.Sum(nil), sig) {
		return "", domain.Fault(domain.ErrAuth, "invalid visitor token")
	}
	return "visitor:" + TokenHash(identity), nil
}
