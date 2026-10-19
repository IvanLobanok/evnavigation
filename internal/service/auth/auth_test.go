package auth

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/IvanLobanok/evnavigation/internal/models/user"
	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/bcrypt"
)

var (
	testSecret = []byte("0123456789abcdef0123456789abcdef")
	fixedNow   = time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)
	errBoom    = errors.New("boom")
)

// memStore — хранилище пользователей в памяти.
type memStore struct {
	users     map[string]*user.User // ключ — email
	nextID    int
	createErr error
	getErr    error

	createCalls int
	lastEmail   string
	lastName    string
	lastHash    string
}

func newMemStore() *memStore { return &memStore{users: map[string]*user.User{}, nextID: 1} }

func (m *memStore) CreateUser(_ context.Context, name, email, hash string, at time.Time) (int, error) {
	m.createCalls++
	m.lastEmail, m.lastName, m.lastHash = email, name, hash
	if m.createErr != nil {
		return 0, m.createErr
	}
	if _, exists := m.users[email]; exists {
		return 0, user.ErrEmailTaken
	}
	id := m.nextID
	m.nextID++
	m.users[email] = &user.User{ID: id, Name: name, Email: email, PasswordHash: hash, CreatedAt: at}
	return id, nil
}

func (m *memStore) GetUserByEmail(_ context.Context, email string) (*user.User, error) {
	if m.getErr != nil {
		return nil, m.getErr
	}
	u, ok := m.users[email]
	if !ok {
		return nil, user.ErrNotFound
	}
	return u, nil
}

func newTestService(t *testing.T, store *memStore) *Service {
	t.Helper()
	svc, err := NewService(store, Config{Secret: testSecret, TTL: time.Hour, BcryptCost: bcrypt.MinCost})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	svc.now = func() time.Time { return fixedNow }
	return svc
}

// ---------------------------------------------------------------------------
// NewService
// ---------------------------------------------------------------------------

func TestNewService_ConfigValidation(t *testing.T) {
	tests := []struct {
		name    string
		cfg     Config
		wantErr bool
	}{
		{"valid", Config{Secret: testSecret, TTL: time.Hour, BcryptCost: bcrypt.MinCost}, false},
		{"zero cost means default", Config{Secret: testSecret, TTL: time.Hour}, false},
		{"empty secret", Config{TTL: time.Hour}, true},
		{"zero ttl", Config{Secret: testSecret}, true},
		{"negative ttl", Config{Secret: testSecret, TTL: -time.Second}, true},
		{"cost too low", Config{Secret: testSecret, TTL: time.Hour, BcryptCost: bcrypt.MinCost - 1}, true},
		{"cost too high", Config{Secret: testSecret, TTL: time.Hour, BcryptCost: bcrypt.MaxCost + 1}, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			svc, err := NewService(newMemStore(), tc.cfg)
			if (err != nil) != tc.wantErr {
				t.Fatalf("error: got %v, wantErr %v", err, tc.wantErr)
			}
			if tc.wantErr && svc != nil {
				t.Error("service must be nil on error")
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Register
// ---------------------------------------------------------------------------

func TestRegister_Success(t *testing.T) {
	store := newMemStore()
	svc := newTestService(t, store)

	u, err := svc.Register(context.Background(), RegisterInput{
		Name: "  Иван  ", Email: "  Ivan@Example.COM ", Password: "correct horse",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if store.lastEmail != "ivan@example.com" {
		t.Errorf("email must be trimmed and lower-cased, stored %q", store.lastEmail)
	}
	if store.lastName != "Иван" {
		t.Errorf("name must be trimmed, stored %q", store.lastName)
	}
	if store.lastHash == "correct horse" || strings.Contains(store.lastHash, "correct horse") {
		t.Fatal("password must never be stored in plain text")
	}
	if err := bcrypt.CompareHashAndPassword([]byte(store.lastHash), []byte("correct horse")); err != nil {
		t.Errorf("stored value is not a valid bcrypt hash of the password: %v", err)
	}

	if u.ID != 1 || u.Email != "ivan@example.com" || u.Name != "Иван" {
		t.Errorf("unexpected user: %+v", u)
	}
	if u.PasswordHash != "" {
		t.Error("returned user must not carry the password hash")
	}
	if !u.CreatedAt.Equal(fixedNow) {
		t.Errorf("created_at: got %v, want %v", u.CreatedAt, fixedNow)
	}
}

func TestRegister_Validation(t *testing.T) {
	good := RegisterInput{Name: "Ivan", Email: "ivan@example.com", Password: "password123"}
	with := func(f func(*RegisterInput)) RegisterInput { in := good; f(&in); return in }

	tests := []struct {
		name      string
		in        RegisterInput
		wantField string
	}{
		{"empty name", with(func(i *RegisterInput) { i.Name = "" }), "name"},
		{"blank name", with(func(i *RegisterInput) { i.Name = "   " }), "name"},
		{"name too long", with(func(i *RegisterInput) { i.Name = strings.Repeat("я", MaxNameLen+1) }), "name"},
		{"empty email", with(func(i *RegisterInput) { i.Email = "" }), "email"},
		{"no at sign", with(func(i *RegisterInput) { i.Email = "ivan.example.com" }), "email"},
		{"display name form", with(func(i *RegisterInput) { i.Email = "Ivan <ivan@example.com>" }), "email"},
		{"email with space inside", with(func(i *RegisterInput) { i.Email = "iv an@example.com" }), "email"},
		{"email too long", with(func(i *RegisterInput) { i.Email = strings.Repeat("a", MaxEmailLen) + "@example.com" }), "email"},
		{"short password", with(func(i *RegisterInput) { i.Password = "1234567" }), "password"},
		{"empty password", with(func(i *RegisterInput) { i.Password = "" }), "password"},
		{"password above bcrypt limit", with(func(i *RegisterInput) { i.Password = strings.Repeat("a", MaxPasswordLen+1) }), "password"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			store := newMemStore()
			u, err := newTestService(t, store).Register(context.Background(), tc.in)

			var ve *ValidationError
			if !errors.As(err, &ve) {
				t.Fatalf("expected *ValidationError, got %v", err)
			}
			if _, ok := ve.Fields[tc.wantField]; !ok {
				t.Errorf("fields must mention %q, got %v", tc.wantField, ve.Fields)
			}
			if u != nil {
				t.Error("user must be nil on validation error")
			}
			if store.createCalls != 0 {
				t.Error("store must not be called for invalid input")
			}
		})
	}
}

func TestRegister_ReportsAllInvalidFields(t *testing.T) {
	_, err := newTestService(t, newMemStore()).Register(context.Background(), RegisterInput{})

	var ve *ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("expected *ValidationError, got %v", err)
	}
	for _, f := range []string{"name", "email", "password"} {
		if _, ok := ve.Fields[f]; !ok {
			t.Errorf("missing problem for %q: %v", f, ve.Fields)
		}
	}
}

func TestRegister_PasswordLengthBoundaries(t *testing.T) {
	tests := []struct {
		length  int
		wantErr bool
	}{{MinPasswordLen - 1, true}, {MinPasswordLen, false}, {MaxPasswordLen, false}, {MaxPasswordLen + 1, true}}

	for _, tc := range tests {
		in := RegisterInput{Name: "Ivan", Email: "ivan@example.com", Password: strings.Repeat("x", tc.length)}
		_, err := newTestService(t, newMemStore()).Register(context.Background(), in)
		if (err != nil) != tc.wantErr {
			t.Errorf("password length %d: got err=%v, wantErr=%v", tc.length, err, tc.wantErr)
		}
	}
}

func TestRegister_DuplicateEmail(t *testing.T) {
	store := newMemStore()
	svc := newTestService(t, store)
	in := RegisterInput{Name: "Ivan", Email: "ivan@example.com", Password: "password123"}

	if _, err := svc.Register(context.Background(), in); err != nil {
		t.Fatal(err)
	}
	in.Email = "IVAN@example.com" // тот же адрес в другом регистре
	if _, err := svc.Register(context.Background(), in); !errors.Is(err, user.ErrEmailTaken) {
		t.Fatalf("got %v, want ErrEmailTaken", err)
	}
}

func TestRegister_StoreErrorIsWrapped(t *testing.T) {
	store := newMemStore()
	store.createErr = errBoom

	_, err := newTestService(t, store).Register(context.Background(),
		RegisterInput{Name: "Ivan", Email: "ivan@example.com", Password: "password123"})

	if !errors.Is(err, errBoom) {
		t.Fatalf("got %v, want wrapped errBoom", err)
	}
	if errors.Is(err, user.ErrEmailTaken) {
		t.Error("a generic store failure must not look like a duplicate email")
	}
}

// ---------------------------------------------------------------------------
// Login
// ---------------------------------------------------------------------------

func registered(t *testing.T) (*Service, *memStore) {
	t.Helper()
	store := newMemStore()
	svc := newTestService(t, store)
	if _, err := svc.Register(context.Background(),
		RegisterInput{Name: "Ivan", Email: "ivan@example.com", Password: "correct horse"}); err != nil {
		t.Fatal(err)
	}
	return svc, store
}

func TestLogin_Success(t *testing.T) {
	svc, _ := registered(t)

	// email нормализуется и при входе
	tok, err := svc.Login(context.Background(), "  IVAN@example.com ", "correct horse")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if tok.AccessToken == "" {
		t.Fatal("empty token")
	}
	if tok.ExpiresIn != time.Hour {
		t.Errorf("ExpiresIn: got %v, want 1h", tok.ExpiresIn)
	}

	id, err := svc.ParseToken(tok.AccessToken)
	if err != nil {
		t.Fatalf("issued token must be accepted: %v", err)
	}
	if id != 1 {
		t.Errorf("token subject: got %d, want 1", id)
	}
}

func TestLogin_TokenContents(t *testing.T) {
	svc, _ := registered(t)
	tok, err := svc.Login(context.Background(), "ivan@example.com", "correct horse")
	if err != nil {
		t.Fatal(err)
	}

	claims := &jwt.RegisteredClaims{}
	parsed, _, err := jwt.NewParser().ParseUnverified(tok.AccessToken, claims)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Method.Alg() != "HS256" {
		t.Errorf("alg: got %s, want HS256", parsed.Method.Alg())
	}
	if !claims.ExpiresAt.Equal(fixedNow.Add(time.Hour)) {
		t.Errorf("exp: got %v, want %v", claims.ExpiresAt, fixedNow.Add(time.Hour))
	}
	if !claims.IssuedAt.Equal(fixedNow) {
		t.Errorf("iat: got %v, want %v", claims.IssuedAt, fixedNow)
	}
	if claims.Subject != "1" {
		t.Errorf("sub: got %q", claims.Subject)
	}
}

func TestLogin_BadCredentialsAreIndistinguishable(t *testing.T) {
	svc, _ := registered(t)

	_, errWrongPass := svc.Login(context.Background(), "ivan@example.com", "wrong password")
	_, errNoUser := svc.Login(context.Background(), "nobody@example.com", "correct horse")

	if !errors.Is(errWrongPass, ErrInvalidCredentials) {
		t.Errorf("wrong password: got %v", errWrongPass)
	}
	if !errors.Is(errNoUser, ErrInvalidCredentials) {
		t.Errorf("unknown email: got %v", errNoUser)
	}
	if errWrongPass.Error() != errNoUser.Error() {
		t.Error("the two failures must be indistinguishable to the caller")
	}
}

func TestLogin_StoreErrorIsNotInvalidCredentials(t *testing.T) {
	svc, store := registered(t)
	store.getErr = errBoom

	_, err := svc.Login(context.Background(), "ivan@example.com", "correct horse")
	if !errors.Is(err, errBoom) {
		t.Fatalf("got %v, want wrapped errBoom", err)
	}
	if errors.Is(err, ErrInvalidCredentials) {
		t.Error("an infrastructure failure must not be reported as bad credentials (it would be a 401 instead of 500)")
	}
}

// ---------------------------------------------------------------------------
// ParseToken
// ---------------------------------------------------------------------------

func signWith(t *testing.T, method jwt.SigningMethod, key any, claims jwt.RegisteredClaims) string {
	t.Helper()
	s, err := jwt.NewWithClaims(method, claims).SignedString(key)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	return s
}

func validClaims() jwt.RegisteredClaims {
	return jwt.RegisteredClaims{
		Subject:   "42",
		IssuedAt:  jwt.NewNumericDate(fixedNow),
		ExpiresAt: jwt.NewNumericDate(fixedNow.Add(time.Hour)),
	}
}

func TestParseToken(t *testing.T) {
	hs256 := func(c jwt.RegisteredClaims) string { return signWith(t, jwt.SigningMethodHS256, testSecret, c) }
	mutate := func(f func(*jwt.RegisteredClaims)) jwt.RegisteredClaims { c := validClaims(); f(&c); return c }

	valid := hs256(validClaims())
	parts := strings.Split(valid, ".")
	otherPayload := signWith(t, jwt.SigningMethodHS256, testSecret, mutate(func(c *jwt.RegisteredClaims) { c.Subject = "1" }))
	tampered := parts[0] + "." + strings.Split(otherPayload, ".")[1] + "." + parts[2] // чужая нагрузка, старая подпись

	tests := []struct {
		name    string
		token   string
		wantID  int
		wantErr bool
	}{
		{"valid token", valid, 42, false},
		{"expired", hs256(mutate(func(c *jwt.RegisteredClaims) { c.ExpiresAt = jwt.NewNumericDate(fixedNow.Add(-time.Second)) })), 0, true},
		{"no exp claim", hs256(mutate(func(c *jwt.RegisteredClaims) { c.ExpiresAt = nil })), 0, true},
		{"wrong secret", signWith(t, jwt.SigningMethodHS256, []byte("another-secret-another-secret-123"), validClaims()), 0, true},
		{"payload swapped, signature kept", tampered, 0, true},
		{"alg none", signWith(t, jwt.SigningMethodNone, jwt.UnsafeAllowNoneSignatureType, validClaims()), 0, true},
		{"HS512 signed with the right secret", signWith(t, jwt.SigningMethodHS512, testSecret, validClaims()), 0, true},
		{"HS384 signed with the right secret", signWith(t, jwt.SigningMethodHS384, testSecret, validClaims()), 0, true},
		{"non-numeric subject", hs256(mutate(func(c *jwt.RegisteredClaims) { c.Subject = "admin" })), 0, true},
		{"empty subject", hs256(mutate(func(c *jwt.RegisteredClaims) { c.Subject = "" })), 0, true},
		{"zero subject", hs256(mutate(func(c *jwt.RegisteredClaims) { c.Subject = "0" })), 0, true},
		{"negative subject", hs256(mutate(func(c *jwt.RegisteredClaims) { c.Subject = "-5" })), 0, true},
		{"empty string", "", 0, true},
		{"garbage", "not.a.jwt", 0, true},
		{"two segments", parts[0] + "." + parts[1], 0, true},
	}

	svc := newTestService(t, newMemStore())
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			id, err := svc.ParseToken(tc.token)

			if tc.wantErr {
				if !errors.Is(err, ErrInvalidToken) {
					t.Fatalf("got err=%v, want ErrInvalidToken", err)
				}
				if id != 0 {
					t.Errorf("id must be 0 on error, got %d", id)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if id != tc.wantID {
				t.Errorf("id: got %d, want %d", id, tc.wantID)
			}
		})
	}
}

func TestParseToken_ExpiryBoundary(t *testing.T) {
	svc := newTestService(t, newMemStore())
	token := signWith(t, jwt.SigningMethodHS256, testSecret, validClaims()) // exp = fixedNow + 1h

	svc.now = func() time.Time { return fixedNow.Add(time.Hour - time.Second) }
	if _, err := svc.ParseToken(token); err != nil {
		t.Errorf("token must still be valid one second before exp: %v", err)
	}

	svc.now = func() time.Time { return fixedNow.Add(time.Hour + time.Second) }
	if _, err := svc.ParseToken(token); !errors.Is(err, ErrInvalidToken) {
		t.Errorf("token must be rejected after exp, got %v", err)
	}
}
