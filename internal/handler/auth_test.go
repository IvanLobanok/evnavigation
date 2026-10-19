package handler

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/IvanLobanok/evnavigation/internal/models/user"
	"github.com/IvanLobanok/evnavigation/internal/service/auth"
)

type fakeAuth struct {
	user        *user.User
	token       *auth.Token
	registerErr error
	loginErr    error

	gotRegister    auth.RegisterInput
	gotLoginEmail  string
	gotLoginPasswd string
	calls          int
}

func (f *fakeAuth) Register(_ context.Context, in auth.RegisterInput) (*user.User, error) {
	f.calls++
	f.gotRegister = in
	if f.registerErr != nil {
		return nil, f.registerErr
	}
	if f.user != nil {
		return f.user, nil
	}
	return &user.User{ID: 1, Name: in.Name, Email: in.Email, CreatedAt: time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)}, nil
}

func (f *fakeAuth) Login(_ context.Context, email, password string) (*auth.Token, error) {
	f.calls++
	f.gotLoginEmail, f.gotLoginPasswd = email, password
	if f.loginErr != nil {
		return nil, f.loginErr
	}
	if f.token != nil {
		return f.token, nil
	}
	return &auth.Token{AccessToken: "jwt-token", ExpiresIn: time.Hour}, nil
}

func postJSON(handlerFunc http.HandlerFunc, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	rec := httptest.NewRecorder()
	handlerFunc(rec, req)
	return rec
}

const registerBody = `{"name":"Ivan","email":"ivan@example.com","password":"correct horse"}`

// ---------------------------------------------------------------------------
// POST /auth/register
// ---------------------------------------------------------------------------

func TestRegister_Success(t *testing.T) {
	svc := &fakeAuth{}
	rec := postJSON(NewAuthHandler(svc, nil).Register, registerBody)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status: got %d, want 201; body: %s", rec.Code, rec.Body)
	}
	if svc.gotRegister != (auth.RegisterInput{Name: "Ivan", Email: "ivan@example.com", Password: "correct horse"}) {
		t.Errorf("service got %+v", svc.gotRegister)
	}

	body := rec.Body.String()
	for _, want := range []string{`"id":1`, `"name":"Ivan"`, `"email":"ivan@example.com"`, `"created_at":"2026-10-17T12:00:00Z"`} {
		if !strings.Contains(body, want) {
			t.Errorf("response must contain %s, got %s", want, body)
		}
	}
}

func TestRegister_ResponseNeverLeaksSecrets(t *testing.T) {
	// даже если сервис по ошибке вернёт хеш, в ответе его быть не должно
	svc := &fakeAuth{user: &user.User{ID: 1, Name: "Ivan", Email: "ivan@example.com", PasswordHash: "$2a$10$SECRETHASH"}}
	rec := postJSON(NewAuthHandler(svc, nil).Register, registerBody)

	for _, forbidden := range []string{"SECRETHASH", "password", "hash", "correct horse"} {
		if strings.Contains(strings.ToLower(rec.Body.String()), strings.ToLower(forbidden)) {
			t.Errorf("response must not contain %q: %s", forbidden, rec.Body)
		}
	}
}

func TestRegister_Errors(t *testing.T) {
	tests := []struct {
		name     string
		body     string
		svcErr   error
		wantCode int
		wantErr  string
	}{
		{"validation error carries field details", registerBody,
			&auth.ValidationError{Fields: map[string]string{"email": "must be a valid email address"}},
			http.StatusBadRequest, "validation_error"},
		{"wrapped validation error", registerBody,
			fmt.Errorf("register: %w", &auth.ValidationError{Fields: map[string]string{"name": "x"}}),
			http.StatusBadRequest, "validation_error"},
		{"email taken", registerBody, user.ErrEmailTaken, http.StatusConflict, "email_taken"},
		{"internal error", registerBody, errors.New("pq: connection refused"), http.StatusInternalServerError, "internal_error"},
		{"invalid json", `{"name":`, nil, http.StatusBadRequest, "invalid_json"},
		{"not an object", `[]`, nil, http.StatusBadRequest, "invalid_json"},
		{"unknown field (mass assignment)", `{"name":"I","email":"i@x.io","password":"password1","is_admin":true}`, nil, http.StatusBadRequest, "invalid_json"},
		{"empty body", ``, nil, http.StatusBadRequest, "invalid_json"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			svc := &fakeAuth{registerErr: tc.svcErr}
			rec := postJSON(NewAuthHandler(svc, nil).Register, tc.body)

			if rec.Code != tc.wantCode {
				t.Fatalf("status: got %d, want %d; body: %s", rec.Code, tc.wantCode, rec.Body)
			}
			if code := decodeErr(t, rec).Error.Code; code != tc.wantErr {
				t.Errorf("code: got %q, want %q", code, tc.wantErr)
			}
			if strings.Contains(rec.Body.String(), "connection refused") {
				t.Errorf("internal error text leaked: %s", rec.Body)
			}
			if tc.wantErr == "invalid_json" && svc.calls != 0 {
				t.Error("service must not be called for malformed input")
			}
		})
	}
}

func TestRegister_ValidationDetailsAreForwarded(t *testing.T) {
	svc := &fakeAuth{registerErr: &auth.ValidationError{Fields: map[string]string{
		"email": "must be a valid email address", "password": "too short",
	}}}
	rec := postJSON(NewAuthHandler(svc, nil).Register, registerBody)

	d := decodeErr(t, rec).Error.Details
	if d["email"] == "" || d["password"] == "" || len(d) != 2 {
		t.Errorf("details: got %v", d)
	}
}

// ---------------------------------------------------------------------------
// POST /auth/login
// ---------------------------------------------------------------------------

func TestLogin_Success(t *testing.T) {
	svc := &fakeAuth{token: &auth.Token{AccessToken: "abc.def.ghi", ExpiresIn: 90 * time.Minute}}
	rec := postJSON(NewAuthHandler(svc, nil).Login, `{"email":"ivan@example.com","password":"correct horse"}`)

	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d; body: %s", rec.Code, rec.Body)
	}
	if svc.gotLoginEmail != "ivan@example.com" || svc.gotLoginPasswd != "correct horse" {
		t.Errorf("service got email=%q password=%q", svc.gotLoginEmail, svc.gotLoginPasswd)
	}
	body := rec.Body.String()
	for _, want := range []string{`"access_token":"abc.def.ghi"`, `"token_type":"Bearer"`, `"expires_in":5400`} {
		if !strings.Contains(body, want) {
			t.Errorf("response must contain %s, got %s", want, body)
		}
	}
}

func TestLogin_MissingFields(t *testing.T) {
	tests := []struct {
		name string
		body string
		want []string
	}{
		{"nothing", `{}`, []string{"email", "password"}},
		{"no password", `{"email":"a@b.c"}`, []string{"password"}},
		{"no email", `{"password":"x"}`, []string{"email"}},
		{"empty strings", `{"email":"","password":""}`, []string{"email", "password"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			svc := &fakeAuth{}
			rec := postJSON(NewAuthHandler(svc, nil).Login, tc.body)

			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status: got %d, want 400", rec.Code)
			}
			details := decodeErr(t, rec).Error.Details
			for _, f := range tc.want {
				if _, ok := details[f]; !ok {
					t.Errorf("details must mention %q: %v", f, details)
				}
			}
			if svc.calls != 0 {
				t.Error("service must not be called")
			}
		})
	}
}

func TestLogin_InvalidCredentials(t *testing.T) {
	for _, err := range []error{auth.ErrInvalidCredentials, fmt.Errorf("login: %w", auth.ErrInvalidCredentials)} {
		rec := postJSON(NewAuthHandler(&fakeAuth{loginErr: err}, nil).Login, `{"email":"a@b.c","password":"hunter2hunter2"}`)

		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("status: got %d, want 401", rec.Code)
		}
		if code := decodeErr(t, rec).Error.Code; code != "invalid_credentials" {
			t.Errorf("code: got %q", code)
		}
		if strings.Contains(rec.Body.String(), "hunter2") {
			t.Error("the response must not echo the password")
		}
	}
}

func TestLogin_InfrastructureFailureIs500NotBadCredentials(t *testing.T) {
	rec := postJSON(NewAuthHandler(&fakeAuth{loginErr: errors.New("db down: secret-dsn")}, nil).Login,
		`{"email":"a@b.c","password":"password123"}`)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status: got %d, want 500", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "secret-dsn") {
		t.Errorf("internal error text leaked: %s", rec.Body)
	}
}

func TestLogin_BadJSON(t *testing.T) {
	for _, body := range []string{``, `nope`, `{"email":"a@b.c","password":"x","remember":true}`} {
		rec := postJSON(NewAuthHandler(&fakeAuth{}, nil).Login, body)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("body %q: got %d, want 400", body, rec.Code)
		}
	}
}
