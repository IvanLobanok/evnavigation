//go:build integration

package postgres

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/IvanLobanok/evnavigation/internal/models/evmodel"
	"github.com/IvanLobanok/evnavigation/internal/models/user"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Интеграционные тесты работают с настоящей БД, в которой уже применены миграции:
//
//	make db-up && go run ./cmd/api   (или make migrate-up)
//	TEST_DATABASE_URL=postgres://postgres:evnav_dev_pass@localhost:5432/evnavigation?sslmode=disable \
//	    go test -tags=integration ./internal/repository/postgres/
//
// Каждый тест создаёт своих пользователей со случайными email и удаляет их
// в t.Cleanup (машины удаляются каскадом), поэтому БД можно не чистить.

func newTestRepo(t *testing.T) (*Repository, *pgxpool.Pool) {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	pool, err := pgxpool.New(context.Background(), url)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(pool.Close)
	return NewRepository(pool), pool
}

func createTestUser(t *testing.T, r *Repository, pool *pgxpool.Pool, tag string) int {
	t.Helper()
	email := fmt.Sprintf("%s-%d@test.example", tag, time.Now().UnixNano())
	id, err := r.CreateUser(context.Background(), "Test "+tag, email, "hash", time.Now())
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM users WHERE id = $1`, id)
	})
	return id
}

func firstModelID(t *testing.T, r *Repository) int {
	t.Helper()
	models, err := r.GetAllEVModels(context.Background())
	if err != nil || len(models) == 0 {
		t.Fatalf("seed models missing: %v (n=%d)", err, len(models))
	}
	return models[0].ID
}

func TestUsers_CreateAndGet(t *testing.T) {
	r, pool := newTestRepo(t)
	ctx := context.Background()

	email := fmt.Sprintf("Mixed.Case-%d@Test.Example", time.Now().UnixNano())
	id, err := r.CreateUser(ctx, "Ivan", email, "bcrypt-hash", time.Now())
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, id) })

	byID, err := r.GetUser(ctx, id)
	if err != nil {
		t.Fatalf("GetUser: %v", err)
	}
	if byID.Name != "Ivan" || byID.Email != email || byID.PasswordHash != "bcrypt-hash" || byID.ID != id {
		t.Errorf("unexpected user: %+v", byID)
	}

	// поиск по email не зависит от регистра
	byEmail, err := r.GetUserByEmail(ctx, strings.ToLower(email))
	if err != nil {
		t.Fatalf("GetUserByEmail: %v", err)
	}
	if byEmail.ID != id {
		t.Errorf("GetUserByEmail returned user %d, want %d", byEmail.ID, id)
	}
}

func TestUsers_NotFound(t *testing.T) {
	r, _ := newTestRepo(t)
	ctx := context.Background()

	if _, err := r.GetUser(ctx, 2_000_000_000); !errors.Is(err, user.ErrNotFound) {
		t.Errorf("GetUser: got %v, want ErrNotFound", err)
	}
	if _, err := r.GetUserByEmail(ctx, "nobody@nowhere.example"); !errors.Is(err, user.ErrNotFound) {
		t.Errorf("GetUserByEmail: got %v, want ErrNotFound", err)
	}
}

func TestUsers_DuplicateEmailIsCaseInsensitive(t *testing.T) {
	r, pool := newTestRepo(t)
	ctx := context.Background()

	base := fmt.Sprintf("dup-%d@test.example", time.Now().UnixNano())
	id, err := r.CreateUser(ctx, "A", base, "h", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, id) })

	for _, dup := range []string{base, strings.ToUpper(base)} {
		if _, err := r.CreateUser(ctx, "B", dup, "h", time.Now()); !errors.Is(err, user.ErrEmailTaken) {
			t.Errorf("email %q: got %v, want ErrEmailTaken", dup, err)
		}
	}
}

func TestVehicles_CreateGetList(t *testing.T) {
	r, pool := newTestRepo(t)
	ctx := context.Background()
	uid := createTestUser(t, r, pool, "veh")
	modelID := firstModelID(t, r)

	nick := "my car"
	v1, err := r.CreateVehicle(ctx, uid, modelID, &nick)
	if err != nil {
		t.Fatalf("create with nickname: %v", err)
	}
	v2, err := r.CreateVehicle(ctx, uid, modelID, nil)
	if err != nil {
		t.Fatalf("create without nickname: %v", err)
	}

	if v1.Nickname == nil || *v1.Nickname != "my car" {
		t.Errorf("nickname: got %v", v1.Nickname)
	}
	if v2.Nickname != nil {
		t.Errorf("NULL nickname must map to nil, got %q", *v2.Nickname)
	}
	if v1.UserID != uid || v1.EVModelID != modelID || v1.EVModel.ID != modelID {
		t.Errorf("unexpected ids: %+v", v1)
	}
	if v1.EVModel.BatteryCapacityKwh <= 0 || v1.EVModel.Brand == "" || v1.EVModel.ConnectorType == "" {
		t.Errorf("model details are not loaded: %+v", v1.EVModel)
	}
	if v1.CreatedAt.IsZero() {
		t.Error("created_at must be filled by the database")
	}

	got, err := r.GetVehicle(ctx, uid, v1.ID)
	if err != nil {
		t.Fatalf("GetVehicle: %v", err)
	}
	if got.ID != v1.ID || got.EVModel.Model != v1.EVModel.Model {
		t.Errorf("GetVehicle mismatch: %+v vs %+v", got, v1)
	}

	list, err := r.ListVehicles(ctx, uid)
	if err != nil {
		t.Fatalf("ListVehicles: %v", err)
	}
	if len(list) != 2 || list[0].ID != v1.ID || list[1].ID != v2.ID {
		t.Errorf("list must contain both vehicles ordered by id, got %d items", len(list))
	}
}

func TestVehicles_OwnershipIsEnforced(t *testing.T) {
	r, pool := newTestRepo(t)
	ctx := context.Background()
	owner := createTestUser(t, r, pool, "owner")
	stranger := createTestUser(t, r, pool, "stranger")

	v, err := r.CreateVehicle(ctx, owner, firstModelID(t, r), nil)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := r.GetVehicle(ctx, stranger, v.ID); !errors.Is(err, user.ErrVehicleNotFound) {
		t.Errorf("foreign vehicle: got %v, want ErrVehicleNotFound", err)
	}
	if _, err := r.GetVehicle(ctx, owner, 2_000_000_000); !errors.Is(err, user.ErrVehicleNotFound) {
		t.Errorf("missing vehicle: got %v, want ErrVehicleNotFound", err)
	}

	list, err := r.ListVehicles(ctx, stranger)
	if err != nil || len(list) != 0 {
		t.Errorf("stranger must see no vehicles: n=%d err=%v", len(list), err)
	}
}

func TestVehicles_ForeignKeyViolationsAreMapped(t *testing.T) {
	r, pool := newTestRepo(t)
	ctx := context.Background()
	uid := createTestUser(t, r, pool, "fk")

	if _, err := r.CreateVehicle(ctx, uid, 2_000_000_000, nil); !errors.Is(err, evmodel.ErrNotFound) {
		t.Errorf("unknown model: got %v, want evmodel.ErrNotFound", err)
	}
	if _, err := r.CreateVehicle(ctx, 2_000_000_000, firstModelID(t, r), nil); !errors.Is(err, user.ErrNotFound) {
		t.Errorf("unknown user: got %v, want user.ErrNotFound", err)
	}
}

func TestVehicles_DeletingUserCascades(t *testing.T) {
	r, pool := newTestRepo(t)
	ctx := context.Background()
	uid := createTestUser(t, r, pool, "cascade")

	v, err := r.CreateVehicle(ctx, uid, firstModelID(t, r), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, uid); err != nil {
		t.Fatal(err)
	}

	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM user_vehicles WHERE id = $1`, v.ID).Scan(&n); err != nil || n != 0 {
		t.Errorf("vehicle must be removed with its owner: n=%d err=%v", n, err)
	}
}
