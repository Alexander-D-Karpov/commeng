package service

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/Alexander-D-Karpov/commeng/internal/auth"
	"github.com/Alexander-D-Karpov/commeng/internal/domain"
)

// Precomputed bcrypt hash of a dummy password for constant-work failed logins.
// It is not used to grant access, only to avoid account-enumeration timing.
var dummyHash, _ = bcrypt.GenerateFromPassword([]byte("unused-dummy-password"), bcrypt.DefaultCost)

func validateAccount(name, email, password string, minimumLength int) (domain.User, error) {
	cleanName, err := domain.LabelText(name, 80)
	if err != nil {
		return domain.User{}, domain.Field("name", err)
	}
	cleanEmail, err := domain.NormalizeEmail(email)
	if err != nil {
		return domain.User{}, domain.Field("email", err)
	}
	if len(password) < minimumLength || len(password) > 72 {
		return domain.User{}, domain.Fault(domain.ErrInput, "password must contain 12–72 bytes (16 for admin)")
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return domain.User{}, err
	}
	return domain.User{Name: cleanName, Email: cleanEmail, PasswordHash: string(hash)}, nil
}

func (s *Service) Register(ctx context.Context, name, email, password string) (domain.User, error) {
	user, err := validateAccount(name, email, password, 12)
	if err != nil {
		return domain.User{}, err
	}
	user.Role = domain.RoleUser
	return s.Store.CreateUser(ctx, user)
}

func (s *Service) BootstrapAdmin(ctx context.Context, name, email, password string) (domain.User, error) {
	user, err := validateAccount(name, email, password, 16)
	if err != nil {
		return domain.User{}, err
	}
	user.Role = domain.RoleAdmin
	return s.Store.CreateUser(ctx, user)
}

func randomToken() (string, error) {
	data := make([]byte, 32)
	if _, err := rand.Read(data); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(data), nil
}

func (s *Service) Login(ctx context.Context, email, password string) (string, domain.User, error) {
	invalid := domain.Fault(domain.ErrAuth, "invalid credentials")
	normalized, err := domain.NormalizeEmail(email)
	if err != nil {
		_ = bcrypt.CompareHashAndPassword(dummyHash, []byte(password))
		return "", domain.User{}, invalid
	}
	user, err := s.Store.UserByEmail(ctx, normalized)
	if domain.HasCode(err, domain.ErrNotFound) {
		_ = bcrypt.CompareHashAndPassword(dummyHash, []byte(password))
		return "", domain.User{}, invalid
	}
	if err != nil {
		return "", domain.User{}, err
	}
	if bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(password)) != nil {
		return "", domain.User{}, invalid
	}
	token, err := randomToken()
	if err != nil {
		return "", domain.User{}, err
	}
	if err := s.Store.SaveSession(ctx, auth.TokenHash(token), user.ID, time.Now().UTC().Add(7*24*time.Hour)); err != nil {
		return "", domain.User{}, err
	}
	return token, user, nil
}

func (s *Service) Logout(ctx context.Context, sessionHash string) error {
	if sessionHash == "" {
		return domain.Fault(domain.ErrAuth, "login required")
	}
	return s.Store.DeleteSession(ctx, sessionHash)
}

func (s *Service) Account(ctx context.Context, token string) (domain.User, error) {
	if token == "" {
		return domain.User{}, domain.Fault(domain.ErrAuth, "login required")
	}
	return s.Store.SessionUser(ctx, auth.TokenHash(token))
}
