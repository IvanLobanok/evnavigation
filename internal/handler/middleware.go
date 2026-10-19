package handler

import (
	"context"
	"net/http"
	"strings"
)

// TokenParser проверяет access-токен и возвращает id пользователя (реализует auth.Service).
type TokenParser interface {
	ParseToken(token string) (int, error)
}

// ключ контекста - собственный тип: строковый ключ мог бы случайно совпасть с ключом другого пакета
type ctxKey int

const userIDKey ctxKey = iota

func withUserID(ctx context.Context, id int) context.Context {
	return context.WithValue(ctx, userIDKey, id)
}

// UserIDFromContext возвращает id пользователя, положенного middleware Authenticate.
func UserIDFromContext(ctx context.Context) (int, bool) {
	id, ok := ctx.Value(userIDKey).(int)
	return id, ok && id > 0
}

// Authenticate пропускает только запросы с валидным заголовком Authorization: Bearer <jwt>.
func Authenticate(tokens TokenParser) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			raw, ok := bearerToken(r)
			if !ok {
				unauthorized(w)
				return
			}
			id, err := tokens.ParseToken(raw)
			if err != nil {
				unauthorized(w) // причину (истёк, подделан...) клиенту не раскрываем
				return
			}
			next.ServeHTTP(w, r.WithContext(withUserID(r.Context(), id)))
		})
	}
}

func bearerToken(r *http.Request) (string, bool) {
	scheme, token, ok := strings.Cut(r.Header.Get("Authorization"), " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") {
		return "", false
	}
	token = strings.TrimSpace(token)
	return token, token != ""
}

func unauthorized(w http.ResponseWriter) {
	w.Header().Set("WWW-Authenticate", `Bearer realm="evnavigation"`)
	writeError(w, http.StatusUnauthorized, "unauthorized", "missing or invalid access token")
}
