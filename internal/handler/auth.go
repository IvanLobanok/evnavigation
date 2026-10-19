package handler

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/IvanLobanok/evnavigation/internal/models/user"
	"github.com/IvanLobanok/evnavigation/internal/service/auth"
)

// Authenticator - регистрация и вход (реализует auth.Service).
type Authenticator interface {
	Register(ctx context.Context, in auth.RegisterInput) (*user.User, error)
	Login(ctx context.Context, email, password string) (*auth.Token, error)
}

type AuthHandler struct {
	svc Authenticator
	log *slog.Logger
}

func NewAuthHandler(svc Authenticator, log *slog.Logger) *AuthHandler {
	if log == nil {
		log = slog.Default()
	}
	return &AuthHandler{svc: svc, log: log}
}

type registerRequest struct {
	Name     string `json:"name"`
	Email    string `json:"email"`
	Password string `json:"password"`
}

type userResponse struct {
	ID        int       `json:"id"`
	Name      string    `json:"name"`
	Email     string    `json:"email"`
	CreatedAt time.Time `json:"created_at"`
}

type loginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

type tokenResponse struct {
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
	ExpiresIn   int    `json:"expires_in"` // секунды
}

// Register - POST /auth/register
func (h *AuthHandler) Register(w http.ResponseWriter, r *http.Request) {
	var req registerRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	u, err := h.svc.Register(r.Context(), auth.RegisterInput{Name: req.Name, Email: req.Email, Password: req.Password})
	if err != nil {
		var ve *auth.ValidationError
		switch {
		case errors.As(err, &ve):
			writeValidationError(w, ve.Fields)
		case errors.Is(err, user.ErrEmailTaken):
			writeError(w, http.StatusConflict, "email_taken", "a user with this email already exists")
		default:
			internalError(w, r, h.log, "register", err)
		}
		return
	}

	writeJSON(w, http.StatusCreated, userResponse{ID: u.ID, Name: u.Name, Email: u.Email, CreatedAt: u.CreatedAt})
}

// Login - POST /auth/login
func (h *AuthHandler) Login(w http.ResponseWriter, r *http.Request) {
	var req loginRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	details := map[string]string{}
	if req.Email == "" {
		details["email"] = "is required"
	}
	if req.Password == "" {
		details["password"] = "is required"
	}
	if len(details) > 0 {
		writeValidationError(w, details)
		return
	}

	tok, err := h.svc.Login(r.Context(), req.Email, req.Password)
	if err != nil {
		if errors.Is(err, auth.ErrInvalidCredentials) {
			writeError(w, http.StatusUnauthorized, "invalid_credentials", "invalid email or password")
			return
		}
		internalError(w, r, h.log, "login", err)
		return
	}

	writeJSON(w, http.StatusOK, tokenResponse{
		AccessToken: tok.AccessToken,
		TokenType:   "Bearer",
		ExpiresIn:   int(tok.ExpiresIn.Seconds()),
	})
}
