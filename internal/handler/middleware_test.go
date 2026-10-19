package handler

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

var errBadToken = errors.New("bad token")

func TestAuthenticate(t *testing.T) {
	tests := []struct {
		name       string
		header     string
		wantStatus int
	}{
		{"valid bearer token", "Bearer good-token", http.StatusOK},
		{"scheme is case-insensitive", "bearer good-token", http.StatusOK},
		{"no header", "", http.StatusUnauthorized},
		{"wrong scheme", "Basic Z29vZC10b2tlbg==", http.StatusUnauthorized},
		{"token without scheme", "good-token", http.StatusUnauthorized},
		{"scheme only", "Bearer", http.StatusUnauthorized},
		{"scheme with empty token", "Bearer    ", http.StatusUnauthorized},
		{"rejected by parser", "Bearer bad-token", http.StatusUnauthorized},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var gotID int
			var gotOK, called bool
			next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				called = true
				gotID, gotOK = UserIDFromContext(r.Context())
				w.WriteHeader(http.StatusOK)
			})

			req := httptest.NewRequest(http.MethodGet, "/", nil)
			if tc.header != "" {
				req.Header.Set("Authorization", tc.header)
			}
			rec := httptest.NewRecorder()
			Authenticate(fakeTokens{})(next).ServeHTTP(rec, req)

			if rec.Code != tc.wantStatus {
				t.Fatalf("status: got %d, want %d", rec.Code, tc.wantStatus)
			}
			if tc.wantStatus == http.StatusOK {
				if !called || !gotOK || gotID != testUserID {
					t.Errorf("handler must see user %d, got id=%d ok=%v called=%v", testUserID, gotID, gotOK, called)
				}
				return
			}
			if called {
				t.Error("next handler must not run for an unauthenticated request")
			}
			if rec.Header().Get("WWW-Authenticate") == "" {
				t.Error("401 must carry WWW-Authenticate")
			}
			if code := decodeErr(t, rec).Error.Code; code != "unauthorized" {
				t.Errorf("code: got %q", code)
			}
		})
	}
}

func TestAuthenticate_DoesNotRevealWhyTokenWasRejected(t *testing.T) {
	// ответы на «просрочен», «подделан», «мусор» должны быть одинаковыми
	var bodies []string
	for _, h := range []string{"Bearer expired", "Bearer forged", "Bearer ###"} {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set("Authorization", h)
		rec := httptest.NewRecorder()
		Authenticate(fakeTokens{})(http.NotFoundHandler()).ServeHTTP(rec, req)
		bodies = append(bodies, rec.Body.String())
	}
	if bodies[0] != bodies[1] || bodies[1] != bodies[2] {
		t.Errorf("responses differ: %q", bodies)
	}
}

func TestUserIDFromContext(t *testing.T) {
	if _, ok := UserIDFromContext(context.Background()); ok {
		t.Error("empty context must have no user")
	}
	if id, ok := UserIDFromContext(withUserID(context.Background(), 42)); !ok || id != 42 {
		t.Errorf("got id=%d ok=%v, want 42/true", id, ok)
	}
	for _, bad := range []int{0, -1} {
		if _, ok := UserIDFromContext(withUserID(context.Background(), bad)); ok {
			t.Errorf("id %d must not count as authenticated", bad)
		}
	}
}

func TestUserIDKeyCannotBeForgedByOtherPackages(t *testing.T) {
	// ключ контекста - приватный тип, поэтому значение с таким же «именем» его не подменяет
	ctx := context.WithValue(context.Background(), "userID", 99) //nolint:staticcheck // намеренно строковый ключ
	ctx = context.WithValue(ctx, "user_id", 99)                  //nolint:staticcheck
	if _, ok := UserIDFromContext(ctx); ok {
		t.Error("a string key must not be mistaken for the typed key")
	}
}
