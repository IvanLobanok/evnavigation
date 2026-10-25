#!/usr/bin/env bash
# Сквозная проверка работающего API: регистрация -> логин -> машина -> маршрут.
# Каждый раз создаёт нового пользователя (smoke-<время>@example.com), поэтому повторные запуски безопасны.
#
#   API_URL=http://localhost:8080 ./scripts/smoke.sh
set -euo pipefail

API_URL="${API_URL:-http://localhost:8080}"
EMAIL="smoke-$(date +%s%N)@example.com"
PASSWORD="smoke-password-123"

BODY=""   # тело последнего ответа

# call METHOD PATH EXPECTED_STATUS [JSON_BODY] [TOKEN]
call() {
	local method="$1" path="$2" expected="$3" data="${4:-}" token="${5:-}"
	local args=(-sS -X "$method" -o /tmp/smoke_body.$$ -w '%{http_code}' "$API_URL$path")
	[[ -n "$data" ]] && args+=(-H 'Content-Type: application/json' -d "$data")
	[[ -n "$token" ]] && args+=(-H "Authorization: Bearer $token")

	local status
	status=$(curl "${args[@]}")
	BODY=$(cat /tmp/smoke_body.$$)
	rm -f /tmp/smoke_body.$$

	if [[ "$status" != "$expected" ]]; then
		echo "FAIL  $method $path: HTTP $status, ожидали $expected"
		echo "      $BODY"
		exit 1
	fi
	echo "ok    $method $path -> $status"
}

# первое значение поля "key": число | "строка"
json_first() { echo "$BODY" | grep -o "\"$1\":[^,}]*" | head -1 | cut -d: -f2- | tr -d '"'; }

call GET  /healthz 200
call GET  /vehicles 401                      # без токена нельзя

call POST /auth/register 201 "{\"name\":\"Smoke\",\"email\":\"$EMAIL\",\"password\":\"$PASSWORD\"}"
call POST /auth/register 409 "{\"name\":\"Smoke\",\"email\":\"$EMAIL\",\"password\":\"$PASSWORD\"}"
call POST /auth/login    401 "{\"email\":\"$EMAIL\",\"password\":\"wrong-password\"}"
call POST /auth/login    200 "{\"email\":\"$EMAIL\",\"password\":\"$PASSWORD\"}"
TOKEN=$(json_first access_token)
[[ -n "$TOKEN" ]] || { echo "FAIL  токен не получен"; exit 1; }

call GET  /ev-models 200
MODEL_ID=$(json_first id)
echo "      выбрана модель id=$MODEL_ID"

# станции — публичный эндпоинт. Вызываем ПОСЛЕ того, как забрали MODEL_ID: json_first читает последний ответ
call GET  "/stations/nearby?lat=53.9006&lon=27.5590&radius_km=3&limit=3" 200
call GET  "/stations/nearby?lat=999&lon=27.5590" 400

call POST /vehicles 201 "{\"ev_model_id\":$MODEL_ID,\"nickname\":\"smoke car\"}" "$TOKEN"
VEHICLE_ID=$(json_first id)
call GET  "/vehicles/$VEHICLE_ID" 200 "" "$TOKEN"

# 60% заряда: до Дзержинска доезжаем без остановок
call POST /routes 200 "{\"from\":{\"lat\":53.9006,\"lon\":27.5590},\"to\":{\"lat\":53.6833,\"lon\":27.1333},\"vehicle_id\":$VEHICLE_ID,\"current_charge_percent\":60}" "$TOKEN"
echo "      $(echo "$BODY" | grep -o '"stops":\[[^]]*\]' | head -c 80)"

# 17% заряда: нужна остановка на зарядку внутри Минска
call POST /routes 200 "{\"from\":{\"lat\":53.82,\"lon\":27.35},\"to\":{\"lat\":53.95,\"lon\":27.70},\"vehicle_id\":$VEHICLE_ID,\"current_charge_percent\":17}" "$TOKEN"
echo "      $(echo "$BODY" | grep -o '"stops":\[{[^}]*}' | head -c 120)..."

echo "smoke: всё хорошо"
