package handler

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"math"
	"net/http"
)

const maxBodyBytes = 1 << 20 // 1 МБ

type errorBody struct {
	Code    string            `json:"code"`
	Message string            `json:"message"`
	Details map[string]string `json:"details,omitempty"`
}

type errorResponse struct {
	Error       errorBody          `json:"error"`
	PartialPlan *planRouteResponse `json:"partial_plan,omitempty"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Error("failed to write response", "error", err)
	}
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, errorResponse{Error: errorBody{Code: code, Message: message}})
}

func writeValidationError(w http.ResponseWriter, details map[string]string) {
	writeJSON(w, http.StatusBadRequest, errorResponse{Error: errorBody{
		Code: "validation_error", Message: "request validation failed", Details: details,
	}})
}

// decodeJSON читает тело строго: без неизвестных полей, без хвоста после объекта, с лимитом размера.
// При ошибке сам пишет ответ и возвращает false.
func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()

	err := dec.Decode(dst)
	if err == nil {
		// после объекта не должно быть ничего, кроме пробелов
		if _, extra := dec.Token(); !errors.Is(extra, io.EOF) {
			err = errors.New("unexpected data after JSON object")
		}
	}
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			writeError(w, http.StatusRequestEntityTooLarge, "body_too_large", "request body is too large")
			return false
		}
		writeError(w, http.StatusBadRequest, "invalid_json", "request body is not valid JSON or contains unknown fields")
		return false
	}
	return true
}

// internalError логирует причину, а клиенту отдаёт общий текст: внутренности наружу не утекают.
func internalError(w http.ResponseWriter, r *http.Request, log *slog.Logger, op string, err error) {
	log.Error("request failed", "op", op, "method", r.Method, "path", r.URL.Path, "error", err)
	writeError(w, http.StatusInternalServerError, "internal_error", "internal server error")
}

// round2 округляет до сотых: в JSON не нужны хвосты вроде 14.399999999.
func round2(x float64) float64 {
	return math.Round(x*100) / 100
}
