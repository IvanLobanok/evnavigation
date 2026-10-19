package auth

import (
	"context"
	"errors"
	"fmt"
	"net/mail"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/IvanLobanok/evnavigation/internal/models/user"
	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/bcrypt"
)

const (
	MinPasswordLen = 8
	MaxPasswordLen = 72 // ограничение bcrypt: всё, что длиннее, молча игнорируется или даёт ошибку
	MaxNameLen     = 100
	MaxEmailLen    = 254
)

var (
	ErrInvalidCredentials = errors.New("invalid email or password")
	ErrInvalidToken       = errors.New("invalid or expired token")
)

// ValidationError описывает некорректные поля регистрации.
type ValidationError struct {
	Fields map[string]string
}

func (e *ValidationError) Error() string { return "validation failed" }

// UserStore - то, что auth требует от хранилища пользователей (реализует postgres.Repository).
type UserStore interface {
	CreateUser(ctx context.Context, name, email, passwordHash string, createdAt time.Time) (int, error)
	GetUserByEmail(ctx context.Context, email string) (*user.User, error)
}

type Config struct {
	Secret     []byte        // ключ подписи HS256
	TTL        time.Duration // срок жизни access-токена
	BcryptCost int           // 0 => bcrypt.DefaultCost
}

type Service struct {
	store     UserStore
	secret    []byte
	ttl       time.Duration
	cost      int
	now       func() time.Time
	dummyHash []byte // для выравнивания времени ответа, когда пользователя нет
}

func NewService(store UserStore, cfg Config) (*Service, error) {
	if len(cfg.Secret) == 0 {
		return nil, errors.New("jwt secret must not be empty")
	}
	if cfg.TTL <= 0 {
		return nil, errors.New("jwt ttl must be positive")
	}
	cost := cfg.BcryptCost
	if cost == 0 {
		cost = bcrypt.DefaultCost
	}
	if cost < bcrypt.MinCost || cost > bcrypt.MaxCost {
		return nil, fmt.Errorf("bcrypt cost must be between %d and %d", bcrypt.MinCost, bcrypt.MaxCost)
	}

	dummy, err := bcrypt.GenerateFromPassword([]byte("timing-equalizer"), cost)
	if err != nil {
		return nil, fmt.Errorf("prepare dummy hash: %w", err)
	}

	return &Service{
		store:     store,
		secret:    cfg.Secret,
		ttl:       cfg.TTL,
		cost:      cost,
		now:       time.Now,
		dummyHash: dummy,
	}, nil
}

// ---------------------------------------------------------------------------
// Регистрация
// ---------------------------------------------------------------------------

type RegisterInput struct {
	Name     string
	Email    string
	Password string
}

func (s *Service) Register(ctx context.Context, in RegisterInput) (*user.User, error) {
	name := strings.TrimSpace(in.Name)
	email := normalizeEmail(in.Email)

	if fields := validateRegistration(name, email, in.Password); len(fields) > 0 {
		return nil, &ValidationError{Fields: fields}
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(in.Password), s.cost)
	if err != nil {
		return nil, fmt.Errorf("hash password: %w", err)
	}

	createdAt := s.now().UTC()
	id, err := s.store.CreateUser(ctx, name, email, string(hash), createdAt)
	if err != nil {
		if errors.Is(err, user.ErrEmailTaken) {
			return nil, err
		}
		return nil, fmt.Errorf("create user: %w", err)
	}

	// PasswordHash намеренно не возвращаем
	return &user.User{ID: id, Name: name, Email: email, CreatedAt: createdAt}, nil
}

func validateRegistration(name, email, password string) map[string]string {
	fields := map[string]string{}

	if name == "" || utf8.RuneCountInString(name) > MaxNameLen {
		fields["name"] = fmt.Sprintf("is required and must be at most %d characters", MaxNameLen)
	}
	if !validEmail(email) {
		fields["email"] = "must be a valid email address"
	}
	if len(password) < MinPasswordLen || len(password) > MaxPasswordLen {
		fields["password"] = fmt.Sprintf("must be between %d and %d bytes", MinPasswordLen, MaxPasswordLen)
	}
	return fields
}

func normalizeEmail(e string) string {
	return strings.ToLower(strings.TrimSpace(e))
}

func validEmail(e string) bool {
	if e == "" || len(e) > MaxEmailLen {
		return false
	}
	addr, err := mail.ParseAddress(e)
	// addr.Address == e отсекает формы вида "Name <a@b.c>"
	return err == nil && addr.Address == e
}

// ---------------------------------------------------------------------------
// Логин и токены
// ---------------------------------------------------------------------------

type Token struct {
	AccessToken string
	ExpiresIn   time.Duration
}

// Login проверяет пароль и выдаёт access-токен.
// И неизвестный email, и неверный пароль дают одну и ту же ErrInvalidCredentials.
func (s *Service) Login(ctx context.Context, email, password string) (*Token, error) {
	u, err := s.store.GetUserByEmail(ctx, normalizeEmail(email))
	if err != nil {
		if errors.Is(err, user.ErrNotFound) {
			// всё равно считаем bcrypt, чтобы по времени ответа нельзя было отличить «нет такого email»
			_ = bcrypt.CompareHashAndPassword(s.dummyHash, []byte(password))
			return nil, ErrInvalidCredentials
		}
		return nil, fmt.Errorf("get user: %w", err)
	}

	if err := bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(password)); err != nil {
		return nil, ErrInvalidCredentials
	}

	now := s.now()
	claims := jwt.RegisteredClaims{
		Subject:   strconv.Itoa(u.ID),
		IssuedAt:  jwt.NewNumericDate(now),
		ExpiresAt: jwt.NewNumericDate(now.Add(s.ttl)),
	}
	signed, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(s.secret)
	if err != nil {
		return nil, fmt.Errorf("sign token: %w", err)
	}
	return &Token{AccessToken: signed, ExpiresIn: s.ttl}, nil
}

// ParseToken проверяет подпись и срок действия токена и возвращает id пользователя.
//
// Защита от подмены алгоритма (alg confusion): принимается только HS256.
// Токены с alg=none, HS384/HS512 или асимметричными алгоритмами отвергаются.
func (s *Service) ParseToken(tokenString string) (int, error) {
	claims := &jwt.RegisteredClaims{}
	_, err := jwt.ParseWithClaims(tokenString, claims,
		func(t *jwt.Token) (any, error) {
			if t.Method != jwt.SigningMethodHS256 {
				return nil, fmt.Errorf("unexpected signing method %q", t.Method.Alg())
			}
			return s.secret, nil
		},
		jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}),
		jwt.WithExpirationRequired(),
		jwt.WithTimeFunc(s.now),
	)
	if err != nil {
		return 0, fmt.Errorf("%w: %v", ErrInvalidToken, err)
	}

	id, err := strconv.Atoi(claims.Subject)
	if err != nil || id <= 0 {
		return 0, fmt.Errorf("%w: bad subject", ErrInvalidToken)
	}
	return id, nil
}
