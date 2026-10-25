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

// ---------------------------------------------------------------------------
// Каталог моделей
// ---------------------------------------------------------------------------

func TestEVModels_GetAll(t *testing.T) {
	r, _ := newTestRepo(t)

	models, err := r.GetAllEVModels(context.Background())
	if err != nil {
		t.Fatalf("GetAllEVModels: %v", err)
	}
	if len(models) == 0 {
		t.Fatal("seed models are missing (is migration 000002 applied?)")
	}
	for _, m := range models {
		if m.ID <= 0 || m.Brand == "" || m.Model == "" || m.BatteryCapacityKwh <= 0 ||
			m.AvgConsumptionKwh100km <= 0 || m.ConnectorType == "" || m.MaxChargingPowerKw <= 0 {
			t.Errorf("model is not fully loaded: %+v", m)
		}
	}
	// ORDER BY brand, model
	for i := 1; i < len(models); i++ {
		a, b := models[i-1], models[i]
		if a.Brand > b.Brand || (a.Brand == b.Brand && a.Model > b.Model) {
			t.Errorf("models are not sorted by brand, model: %s %s before %s %s", a.Brand, a.Model, b.Brand, b.Model)
		}
	}
}

func TestEVModels_GetByID(t *testing.T) {
	r, _ := newTestRepo(t)
	ctx := context.Background()

	all, err := r.GetAllEVModels(ctx)
	if err != nil || len(all) == 0 {
		t.Fatalf("GetAllEVModels: %v (n=%d)", err, len(all))
	}

	for _, want := range all {
		got, err := r.GetEVModelByID(ctx, want.ID)
		if err != nil {
			t.Fatalf("GetEVModelByID(%d): %v", want.ID, err)
		}
		if *got != want {
			t.Errorf("GetEVModelByID(%d) differs from the catalog entry:\n got  %+v\n want %+v", want.ID, *got, want)
		}
	}
}

func TestEVModels_GetByIDNotFound(t *testing.T) {
	r, _ := newTestRepo(t)

	if _, err := r.GetEVModelByID(context.Background(), 2_000_000_000); !errors.Is(err, evmodel.ErrNotFound) {
		t.Errorf("got %v, want evmodel.ErrNotFound", err)
	}
}

// ---------------------------------------------------------------------------
// Станции (PostGIS)
// ---------------------------------------------------------------------------

// seededStation берёт реальную станцию из сид-миграции, чтобы тест не зависел от конкретных координат.
func seededStation(t *testing.T, pool *pgxpool.Pool) (id int64, lat, lon float64) {
	t.Helper()
	err := pool.QueryRow(context.Background(), `
		SELECT id, ST_Y(location::geometry), ST_X(location::geometry)
		FROM charging_stations WHERE is_operational ORDER BY id LIMIT 1`).Scan(&id, &lat, &lon)
	if err != nil {
		t.Fatalf("no seeded stations (is migration 000003 applied?): %v", err)
	}
	return id, lat, lon
}

func TestStations_FindNearby(t *testing.T) {
	r, pool := newTestRepo(t)
	ctx := context.Background()
	id, lat, lon := seededStation(t, pool)

	found, err := r.FindNearbyStations(ctx, lat, lon, 100)
	if err != nil {
		t.Fatalf("FindNearbyStations: %v", err)
	}
	if len(found) == 0 || found[0].ID != id {
		t.Fatalf("the station at the query point must come first, got %+v", found)
	}

	s := found[0]
	if s.Name == "" || len(s.ConnectorTypes) == 0 || s.PowerKW <= 0 || !s.IsOperational {
		t.Errorf("station fields are not loaded: %+v", s)
	}
	// ST_Y/ST_X возвращают то, что записано в location: широта и долгота не перепутаны
	if diff := s.Latitude - lat; diff > 1e-9 || diff < -1e-9 {
		t.Errorf("latitude: got %v, want %v", s.Latitude, lat)
	}
	if diff := s.Longitude - lon; diff > 1e-9 || diff < -1e-9 {
		t.Errorf("longitude: got %v, want %v", s.Longitude, lon)
	}
}

func TestStations_FindNearbyIsSortedByDistance(t *testing.T) {
	r, pool := newTestRepo(t)
	ctx := context.Background()
	_, lat, lon := seededStation(t, pool)

	found, err := r.FindNearbyStations(ctx, lat, lon, 30_000)
	if err != nil {
		t.Fatal(err)
	}
	if len(found) < 2 {
		t.Skipf("need at least 2 stations within 30 km to check ordering, got %d", len(found))
	}

	prev := -1.0
	for _, s := range found {
		var d float64
		err := pool.QueryRow(ctx, `
			SELECT ST_Distance(location, ST_SetSRID(ST_MakePoint($2,$3),4326)::geography)
			FROM charging_stations WHERE id = $1`, s.ID, lon, lat).Scan(&d)
		if err != nil {
			t.Fatal(err)
		}
		if d < prev {
			t.Fatalf("stations are not ordered by distance: %v came after %v", d, prev)
		}
		prev = d
	}
}

func TestStations_RadiusIsRespected(t *testing.T) {
	r, pool := newTestRepo(t)
	ctx := context.Background()

	// (0, 0) — Гвинейский залив: станций там нет
	if found, err := r.FindNearbyStations(ctx, 0, 0, 50_000); err != nil || len(found) != 0 {
		t.Errorf("far from every station: got %d stations, err=%v", len(found), err)
	}

	// чем больше радиус, тем не меньше станций
	_, lat, lon := seededStation(t, pool)
	small, err1 := r.FindNearbyStations(ctx, lat, lon, 100)
	large, err2 := r.FindNearbyStations(ctx, lat, lon, 50_000)
	if err1 != nil || err2 != nil {
		t.Fatalf("errors: %v %v", err1, err2)
	}
	if len(large) < len(small) {
		t.Errorf("a larger radius returned fewer stations: %d < %d", len(large), len(small))
	}
}

func TestStations_InactiveStationsAreHidden(t *testing.T) {
	r, pool := newTestRepo(t)
	ctx := context.Background()
	_, lat, lon := seededStation(t, pool)

	var inactiveID int64
	err := pool.QueryRow(ctx, `
		INSERT INTO charging_stations (name, location, connector_types, power_kw, is_operational)
		VALUES ('integration-test inactive', ST_SetSRID(ST_MakePoint($1,$2),4326)::geography, ARRAY['CCS Combo 2'], 50, false)
		RETURNING id`, lon, lat).Scan(&inactiveID)
	if err != nil {
		t.Fatalf("insert inactive station: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM charging_stations WHERE id = $1`, inactiveID) })

	found, err := r.FindNearbyStations(ctx, lat, lon, 1000)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range found {
		if s.ID == inactiveID {
			t.Fatal("a non-operational station must not be returned")
		}
	}
}
