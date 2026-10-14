package postgres

import (
	"context"
	"fmt"

	"github.com/IvanLobanok/evnavigation/internal/models/user"
)

func (r *Repository) GetUser(ctx context.Context, id int) (*user.User, error) {
	query :=
		`
		SELECT ID, name,email,passwordHash, createdAt FROM users WHERE id = $1	
		`
	var u user.User
	err := r.db.QueryRow(ctx, query, id).Scan(&u.ID, &u.Name, &u.Email, &u.PasswordHash, &u.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("")
	}
	return &u, nil
}
