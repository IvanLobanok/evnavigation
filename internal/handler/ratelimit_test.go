package handler

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/IvanLobanok/evnavigation/internal/service/auth"
)

// fakeClock - управляемое время для детерминированных тестов ведра.
type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) advance(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

func newClockLimiter(perMinute, burst int) (*limiter, *fakeClock) {
	l := newLimiter(RateLimit{PerMinute: perMinute, Burst: burst})
	clk := &fakeClock{t: time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC)}
	l.now = clk.now
	return l, clk
}

// ---------------------------------------------------------------------------
// Ведро
// ---------------------------------------------------------------------------

func TestLimiter_BurstThenRefuse(t *testing.T) {
	l, _ := newClockLimiter(60, 3) // 1 токен в секунду, ведро на 3

	for i := 0; i < 3; i++ {
		if ok, _ := l.allow("k"); !ok {
			t.Fatalf("request %d within the burst must pass", i+1)
		}
	}
	ok, wait := l.allow("k")
	if ok {
		t.Fatal("the 4th request must be refused")
	}
	if wait <= 0 || wait > time.Second {
		t.Errorf("wait for the next token: got %v, want within (0, 1s]", wait)
	}
}

func TestLimiter_RefillsOverTime(t *testing.T) {
	l, clk := newClockLimiter(60, 2)
	l.allow("k")
	l.allow("k")
	if ok, _ := l.allow("k"); ok {
		t.Fatal("bucket must be empty")
	}

	clk.advance(1 * time.Second) // +1 токен
	if ok, _ := l.allow("k"); !ok {
		t.Error("one token must have been refilled after 1s")
	}
	if ok, _ := l.allow("k"); ok {
		t.Error("only one token was refilled")
	}

	// ведро не переполняется выше burst: и при паузе короче интервала чистки (тут работает сам кап),
	// и при паузе в час (тут ведро ещё и вычищается как полное)
	for _, pause := range []time.Duration{30 * time.Second, time.Hour} {
		clk.advance(pause)
		for i := 0; i < 2; i++ {
			if ok, _ := l.allow("k"); !ok {
				t.Fatalf("pause %v: request %d must pass", pause, i+1)
			}
		}
		if ok, _ := l.allow("k"); ok {
			t.Errorf("pause %v: the bucket must be capped at burst, not accumulate tokens", pause)
		}
	}
}

func TestLimiter_KeysAreIndependent(t *testing.T) {
	l, _ := newClockLimiter(60, 1)
	if ok, _ := l.allow("a"); !ok {
		t.Fatal("a: first request")
	}
	if ok, _ := l.allow("a"); ok {
		t.Fatal("a: second request must be refused")
	}
	if ok, _ := l.allow("b"); !ok {
		t.Error("b must not be affected by a")
	}
}

func TestLimiter_SweepsIdleKeys(t *testing.T) {
	l, clk := newClockLimiter(60, 2)
	for i := 0; i < 50; i++ {
		l.allow(fmt.Sprintf("k%d", i))
	}
	if len(l.buckets) != 50 {
		t.Fatalf("setup: %d buckets", len(l.buckets))
	}

	clk.advance(10 * time.Minute) // все вёдра давно полны: помнить их незачем
	l.allow("fresh")
	if len(l.buckets) != 1 {
		t.Errorf("idle buckets must be dropped, %d remain", len(l.buckets))
	}
}

func TestLimiter_FailsOpenWhenTooManyKeys(t *testing.T) {
	l, _ := newClockLimiter(60, 1)
	for i := 0; i < maxLimiterKeys; i++ {
		l.buckets[fmt.Sprintf("k%d", i)] = &bucket{tokens: 0, last: l.now()} // не полные: sweep их не уберёт
	}
	for i := 0; i < 3; i++ {
		if ok, _ := l.allow("newcomer"); !ok {
			t.Fatal("a new key beyond the memory cap must be let through, not refused")
		}
	}
	if len(l.buckets) > maxLimiterKeys {
		t.Errorf("memory cap exceeded: %d keys", len(l.buckets))
	}
}

func TestLimiter_DisabledByZeroRate(t *testing.T) {
	for _, rl := range []RateLimit{{}, {PerMinute: 0, Burst: 5}, {PerMinute: -3, Burst: 5}} {
		if newLimiter(rl) != nil {
			t.Errorf("%+v must disable the limiter", rl)
		}
	}
	// а выключенный middleware ничего не меняет
	h := limitByIP(RateLimit{}, false)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	for i := 0; i < 50; i++ {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
		if rec.Code != http.StatusNoContent {
			t.Fatalf("request %d: %d", i, rec.Code)
		}
	}
}

func TestLimiter_ZeroBurstStillAllowsOneRequest(t *testing.T) {
	l, _ := newClockLimiter(60, 0)
	if ok, _ := l.allow("k"); !ok {
		t.Error("a bucket must hold at least one token")
	}
}

func TestLimiter_ConcurrentUse(t *testing.T) {
	l := newLimiter(RateLimit{PerMinute: 60, Burst: 100})
	var wg sync.WaitGroup
	var mu sync.Mutex
	passed := 0
	for i := 0; i < 400; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if ok, _ := l.allow("shared"); ok {
				mu.Lock()
				passed++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	// ведро на 100 токенов и 1 токен/с: за мгновение пройти может немного больше 100, но не 400
	if passed < 100 || passed > 102 {
		t.Errorf("passed %d of 400, want about 100", passed)
	}
}

// ---------------------------------------------------------------------------
// IP клиента
// ---------------------------------------------------------------------------

func TestClientIP(t *testing.T) {
	tests := []struct {
		name       string
		remote     string
		xff        string
		trustProxy bool
		want       string
	}{
		{"plain ipv4", "203.0.113.7:51000", "", false, "203.0.113.7"},
		{"xff is ignored without trust (it is client-controlled)", "203.0.113.7:51000", "1.2.3.4", false, "203.0.113.7"},
		{"trusted proxy: last xff entry", "10.0.0.2:4444", "9.9.9.9, 198.51.100.5", true, "198.51.100.5"},
		{"trusted proxy: forged left entries are ignored", "10.0.0.2:4444", "6.6.6.6, 7.7.7.7, 198.51.100.5", true, "198.51.100.5"},
		{"trusted proxy: garbage xff falls back to the peer", "10.0.0.2:4444", "not-an-ip", true, "10.0.0.2"},
		{"trusted proxy: no xff falls back to the peer", "10.0.0.2:4444", "", true, "10.0.0.2"},
		{"ipv6 is folded to /64", "[2001:db8:1:2:aaaa:bbbb:cccc:dddd]:80", "", false, "2001:db8:1:2::"},
		{"another address of the same /64 is the same client", "[2001:db8:1:2:1111:2222:3333:4444]:80", "", false, "2001:db8:1:2::"},
		{"ipv4-mapped ipv6 is treated as ipv4", "[::ffff:203.0.113.7]:80", "", false, "203.0.113.7"},
		{"remote without a port", "203.0.113.7", "", false, "203.0.113.7"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			req.RemoteAddr = tc.remote
			if tc.xff != "" {
				req.Header.Set("X-Forwarded-For", tc.xff)
			}
			if got := clientIP(req, tc.trustProxy); got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Через роутер
// ---------------------------------------------------------------------------

// twoUsers выдаёт токены двух разных пользователей.
type twoUsers struct{}

func (twoUsers) ParseToken(token string) (int, error) {
	switch token {
	case "tok-a":
		return 1, nil
	case "tok-b":
		return 2, nil
	}
	return 0, errBadToken
}

func limitedRouter(limits RateLimits) http.Handler {
	vehicles := &fakeVehicles{details: testVehicle}
	return NewRouter(Handlers{
		Auth:     NewAuthHandler(&fakeAuth{}, nil),
		Vehicles: NewVehicleHandler(&fakeVehicleService{}, nil),
		Stations: NewStationHandler(&fakeStationFinder{stations: manyStations(2)}, nil),
		Routes:   NewRoutesHandler(&fakePlanner{plan: samplePlan()}, vehicles, &fakeRecorder{}, nil),
		History:  NewHistoryHandler(&fakeHistory{}, nil),
		Tokens:   twoUsers{},
		Limits:   limits,
	})
}

func do(h http.Handler, method, path, body, token, remote string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if remote != "" {
		req.RemoteAddr = remote
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestRouter_AuthLimitIsPerIPAndAnswers429(t *testing.T) {
	h := limitedRouter(RateLimits{Auth: RateLimit{PerMinute: 60, Burst: 2}})
	login := `{"email":"i@x.io","password":"password123"}`

	for i := 0; i < 2; i++ {
		if rec := do(h, http.MethodPost, "/auth/login", login, "", "203.0.113.1:1000"); rec.Code != http.StatusOK {
			t.Fatalf("login %d: %d", i+1, rec.Code)
		}
	}
	rec := do(h, http.MethodPost, "/auth/login", login, "", "203.0.113.1:1001") // другой порт - тот же клиент
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("3rd login from one IP: got %d, want 429", rec.Code)
	}
	if rec.Header().Get("Retry-After") == "" {
		t.Error("429 must carry Retry-After")
	}
	var body errorResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || body.Error.Code != "rate_limited" {
		t.Errorf("error body: %s (err=%v)", rec.Body, err)
	}

	// register делит ведро с login: оба упираются в bcrypt
	if rec := do(h, http.MethodPost, "/auth/register", registerBody, "", "203.0.113.1:1002"); rec.Code != http.StatusTooManyRequests {
		t.Errorf("register from the same IP: got %d, want 429", rec.Code)
	}
	// другой клиент не пострадал
	if rec := do(h, http.MethodPost, "/auth/login", login, "", "203.0.113.2:1000"); rec.Code != http.StatusOK {
		t.Errorf("another IP: got %d, want 200", rec.Code)
	}
}

func TestRouter_RoutesLimitIsPerUser(t *testing.T) {
	h := limitedRouter(RateLimits{Routes: RateLimit{PerMinute: 60, Burst: 2}})

	for i := 0; i < 2; i++ {
		if rec := do(h, http.MethodPost, "/routes", validBody, "tok-a", "203.0.113.1:1"); rec.Code != http.StatusOK {
			t.Fatalf("user A request %d: %d: %s", i+1, rec.Code, rec.Body)
		}
	}
	if rec := do(h, http.MethodPost, "/routes", validBody, "tok-a", "203.0.113.99:1"); rec.Code != http.StatusTooManyRequests {
		t.Errorf("user A 3rd request (from another IP): got %d, want 429: the limit follows the user, not the address", rec.Code)
	}
	if rec := do(h, http.MethodPost, "/routes", validBody, "tok-b", "203.0.113.1:1"); rec.Code != http.StatusOK {
		t.Errorf("user B shares an IP with A but must have their own bucket: got %d", rec.Code)
	}
}

func TestRouter_RoutesLimitDoesNotHideAuthErrors(t *testing.T) {
	// анонимные запросы получают 401 и не расходуют чужие ведра
	h := limitedRouter(RateLimits{Routes: RateLimit{PerMinute: 60, Burst: 1}})
	for i := 0; i < 5; i++ {
		if rec := do(h, http.MethodPost, "/routes", validBody, "", ""); rec.Code != http.StatusUnauthorized {
			t.Fatalf("anonymous request %d: got %d, want 401", i+1, rec.Code)
		}
	}
}

func TestRouter_PublicLimitAppliesToPublicReadsOnly(t *testing.T) {
	h := limitedRouter(RateLimits{Public: RateLimit{PerMinute: 60, Burst: 1}})

	if rec := do(h, http.MethodGet, "/stations/nearby?lat=53.9&lon=27.5", "", "", "203.0.113.1:1"); rec.Code != http.StatusOK {
		t.Fatalf("first: %d", rec.Code)
	}
	if rec := do(h, http.MethodGet, "/ev-models", "", "", "203.0.113.1:2"); rec.Code != http.StatusTooManyRequests {
		t.Errorf("/ev-models shares the public bucket: got %d, want 429", rec.Code)
	}
	for i := 0; i < 5; i++ {
		if rec := do(h, http.MethodGet, "/healthz", "", "", "203.0.113.1:3"); rec.Code != http.StatusOK {
			t.Fatalf("/healthz must never be limited: %d", rec.Code)
		}
	}
}

func TestRouter_NoLimitsByDefault(t *testing.T) {
	h := limitedRouter(RateLimits{})
	for i := 0; i < 100; i++ {
		if rec := do(h, http.MethodPost, "/auth/login", `{"email":"i@x.io","password":"password123"}`, "", "203.0.113.1:1"); rec.Code != http.StatusOK {
			t.Fatalf("request %d: %d", i+1, rec.Code)
		}
	}
}

func TestRouter_TrustedProxyHeaderSeparatesClients(t *testing.T) {
	login := `{"email":"i@x.io","password":"password123"}`
	send := func(h http.Handler, client string) int {
		req := httptest.NewRequest(http.MethodPost, "/auth/login", strings.NewReader(login))
		req.RemoteAddr = "10.0.0.2:5555" // всегда один и тот же прокси
		req.Header.Set("X-Forwarded-For", "6.6.6.6, "+client)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code
	}

	trusted := limitedRouter(RateLimits{Auth: RateLimit{PerMinute: 60, Burst: 1}, TrustProxyHeaders: true})
	if send(trusted, "198.51.100.1") != http.StatusOK || send(trusted, "198.51.100.2") != http.StatusOK {
		t.Error("with a trusted proxy, different clients behind it must have separate buckets")
	}
	if send(trusted, "198.51.100.1") != http.StatusTooManyRequests {
		t.Error("the same client behind the proxy must be limited")
	}

	untrusted := limitedRouter(RateLimits{Auth: RateLimit{PerMinute: 60, Burst: 1}})
	if send(untrusted, "198.51.100.1") != http.StatusOK || send(untrusted, "198.51.100.2") != http.StatusTooManyRequests {
		t.Error("without trust, every client behind the proxy shares the proxy's bucket")
	}
}

// ---------------------------------------------------------------------------
// 503 при перегрузке bcrypt
// ---------------------------------------------------------------------------

func TestAuth_BusyAnswers503WithRetryAfter(t *testing.T) {
	busy := &fakeAuth{registerErr: auth.ErrBusy, loginErr: auth.ErrBusy}
	h := NewAuthHandler(busy, nil)

	for name, rec := range map[string]*httptest.ResponseRecorder{
		"register": postJSON(h.Register, registerBody),
		"login":    postJSON(h.Login, `{"email":"i@x.io","password":"password123"}`),
	} {
		if rec.Code != http.StatusServiceUnavailable {
			t.Errorf("%s: got %d, want 503", name, rec.Code)
		}
		if rec.Header().Get("Retry-After") == "" {
			t.Errorf("%s: 503 must carry Retry-After", name)
		}
		if !strings.Contains(rec.Body.String(), "service_busy") {
			t.Errorf("%s: body %s", name, rec.Body)
		}
	}
}
