package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/IvanLobanok/evnavigation/internal/models/user"
	"github.com/jackc/pgerrcode"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

const userColumns = `id, name, email, password_hash, created_at`

func (r *Repository) GetUser(ctx context.Context, id int) (*user.User, error) {
	query := `SELECT ` + userColumns + ` FROM users WHERE id = $1`
	return r.scanUser(r.db.QueryRow(ctx, query, id))
}

// GetUserByEmail ищет пользователя без учёта регистра email.
func (r *Repository) GetUserByEmail(ctx context.Context, email string) (*user.User, error) {
	query := `SELECT ` + userColumns + ` FROM users WHERE lower(email) = lower($1)`
	return r.scanUser(r.db.QueryRow(ctx, query, email))
}

func (r *Repository) scanUser(row pgx.Row) (*user.User, error) {
	var u user.User
	err := row.Scan(&u.ID, &u.Name, &u.Email, &u.PasswordHash, &u.CreatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, user.ErrNotFound
		}
		return nil, fmt.Errorf("get user: %w", err)
	}
	return &u, nil
}

// CreateUser создаёт пользователя. Если email уже занят - возвращает user.ErrEmailTaken.
func (r *Repository) CreateUser(ctx context.Context, name string, email string, passwordHash string, createdAt time.Time) (int, error) {
	query := `
		INSERT INTO users (name, email, password_hash, created_at)
		VALUES ($1, $2, $3, $4)
		RETURNING id
	`
	var id int
	err := r.db.QueryRow(ctx, query, name, email, passwordHash, createdAt).Scan(&id)
	if err != nil {
		if isPgError(err, pgerrcode.UniqueViolation) {
			return 0, user.ErrEmailTaken
		}
		return 0, fmt.Errorf("create user: %w", err)
	}
	return id, nil
}

// isPgError проверяет, что err - ошибка PostgreSQL с данным SQLSTATE-кодом.
func isPgError(err error, code string) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == code
}
