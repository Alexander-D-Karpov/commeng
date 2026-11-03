package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/Alexander-D-Karpov/commeng/internal/domain"
)

const userColumns = `id,name,email,password_hash,role`

func scanUser(row pgx.Row) (domain.User, error) {
	var user domain.User
	err := row.Scan(&user.ID, &user.Name, &user.Email, &user.PasswordHash, &user.Role)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.User{}, domain.Fault(domain.ErrNotFound, "user not found")
	}
	return user, err
}
func (s *Store) CreateUser(ctx context.Context, user domain.User) (domain.User, error) {
	created, err := scanUser(s.pool.QueryRow(ctx, `
        INSERT INTO users(name,email,password_hash,role) VALUES($1,$2,$3,$4)
        RETURNING `+userColumns, user.Name, user.Email, user.PasswordHash, user.Role))
	if isUniqueViolation(err) {
		return domain.User{}, domain.Fault(domain.ErrAlreadyExists, "email already registered")
	}
	return created, err
}
func (s *Store) UserByEmail(ctx context.Context, email string) (domain.User, error) {
	return scanUser(s.pool.QueryRow(ctx, `SELECT `+userColumns+` FROM users WHERE email=$1`, email))
}
func (s *Store) UserByID(ctx context.Context, id int64) (domain.User, error) {
	return scanUser(s.pool.QueryRow(ctx, `SELECT `+userColumns+` FROM users WHERE id=$1`, id))
}
func (s *Store) Users(ctx context.Context, ids []int64) (map[int64]domain.User, error) {
	result := make(map[int64]domain.User, len(ids))
	if len(ids) == 0 {
		return result, nil
	}
	rows, err := s.pool.Query(ctx, `SELECT `+userColumns+` FROM users WHERE id=ANY($1::bigint[])`, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var u domain.User
		if err := rows.Scan(&u.ID, &u.Name, &u.Email, &u.PasswordHash, &u.Role); err != nil {
			return nil, err
		}
		result[u.ID] = u
	}
	return result, rows.Err()
}
func (s *Store) SaveSession(ctx context.Context, hash string, userID int64, expires time.Time) error {
	_, err := s.pool.Exec(
		ctx,
		`INSERT INTO sessions(token_hash,user_id,expires_at) VALUES($1,$2,$3)`,
		hash,
		userID,
		expires,
	)
	return err
}
func (s *Store) SessionUser(ctx context.Context, hash string) (domain.User, error) {
	return scanUser(s.pool.QueryRow(ctx, `
        SELECT u.id,u.name,u.email,u.password_hash,u.role
        FROM sessions s JOIN users u ON u.id=s.user_id
        WHERE s.token_hash=$1 AND s.expires_at>now()`, hash))
}

func isUniqueViolation(err error) bool {
	var p *pgconn.PgError
	return errors.As(err, &p) && p.Code == "23505"
}

func (s *Store) DeleteSession(ctx context.Context, hash string) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM sessions WHERE token_hash=$1`, hash)
	return err
}
