package memory

import (
	"context"
	"time"

	"github.com/Alexander-D-Karpov/commeng/internal/domain"
)

func (s *Store) CreateUser(ctx context.Context, user domain.User) (domain.User, error) {
	if err := ctx.Err(); err != nil {
		return domain.User{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.usersByEmail[user.Email] != 0 {
		return domain.User{}, domain.Fault(domain.ErrAlreadyExists, "email already registered")
	}
	s.nextUser++
	user.ID = s.nextUser
	s.users[user.ID] = user
	s.usersByEmail[user.Email] = user.ID
	return user, nil
}
func (s *Store) UserByEmail(ctx context.Context, email string) (domain.User, error) {
	if err := ctx.Err(); err != nil {
		return domain.User{}, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	id := s.usersByEmail[email]
	if id == 0 {
		return domain.User{}, domain.Fault(domain.ErrNotFound, "user not found")
	}
	return s.users[id], nil
}
func (s *Store) UserByID(ctx context.Context, id int64) (domain.User, error) {
	if err := ctx.Err(); err != nil {
		return domain.User{}, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	user, ok := s.users[id]
	if !ok {
		return domain.User{}, domain.Fault(domain.ErrNotFound, "user not found")
	}
	return user, nil
}
func (s *Store) Users(ctx context.Context, ids []int64) (map[int64]domain.User, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make(map[int64]domain.User, len(ids))
	for _, id := range ids {
		if user, ok := s.users[id]; ok {
			result[id] = user
		}
	}
	return result, nil
}
func (s *Store) SaveSession(ctx context.Context, key string, id int64, expires time.Time) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.users[id]; !ok {
		return domain.Fault(domain.ErrNotFound, "user not found")
	}
	for token, entry := range s.sessions {
		if !time.Now().Before(entry.expires) {
			delete(s.sessions, token)
		}
	}
	s.sessions[key] = session{userID: id, expires: expires}
	return nil
}
func (s *Store) SessionUser(ctx context.Context, key string) (domain.User, error) {
	if err := ctx.Err(); err != nil {
		return domain.User{}, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	sess, ok := s.sessions[key]
	if !ok || time.Now().After(sess.expires) {
		return domain.User{}, domain.Fault(domain.ErrAuth, "session expired")
	}
	return s.users[sess.userID], nil
}

func (s *Store) DeleteSession(ctx context.Context, hash string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.sessions, hash)
	return nil
}
