package handler

import (
	"math"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// RateLimit - ограничение частоты запросов на один ключ (IP или пользователя) по схеме «токенового ведра»:
// в ведре помещается Burst запросов, а пополняется оно со скоростью PerMinute запросов в минуту.
// PerMinute <= 0 выключает ограничение.
type RateLimit struct {
	PerMinute int
	Burst     int
}

// RateLimits собирает ограничения для групп эндпоинтов. Нулевое значение выключает все ограничения.
type RateLimits struct {
	Auth   RateLimit // по IP: POST /auth/register и /auth/login (внутри каждого запроса - bcrypt)
	Public RateLimit // по IP: GET /ev-models и /stations/nearby (без авторизации)
	Routes RateLimit // по пользователю: POST /routes (самый дорогой запрос: вызовы провайдера и поиск станций)

	// TrustProxyHeaders: брать IP клиента из последнего значения X-Forwarded-For.
	// Включайте, только если сервис стоит за собственным прокси, который этот заголовок дописывает:
	// иначе клиент подставит любой IP и обойдёт лимит. Без прокси держите выключенным.
	// Если сервис за прокси, а флаг выключен, все клиенты делят один IP прокси и один общий лимит.
	TrustProxyHeaders bool
}

const (
	// maxLimiterKeys ограничивает память: больше ключей не отслеживаем (см. allow).
	maxLimiterKeys = 100_000
	sweepInterval  = time.Minute
)

type bucket struct {
	tokens float64
	last   time.Time
}

// limiter - набор вёдер по ключам. Горутин не запускает: простаивающие ключи вычищаются при обращениях.
type limiter struct {
	mu        sync.Mutex
	buckets   map[string]*bucket
	rate      float64 // токенов в секунду
	burst     float64
	now       func() time.Time
	lastSweep time.Time
}

// newLimiter возвращает nil, если ограничение выключено.
func newLimiter(rl RateLimit) *limiter {
	if rl.PerMinute <= 0 {
		return nil
	}
	burst := rl.Burst
	if burst < 1 {
		burst = 1
	}
	return &limiter{
		buckets: make(map[string]*bucket),
		rate:    float64(rl.PerMinute) / 60,
		burst:   float64(burst),
		now:     time.Now,
	}
}

// allow расходует один токен ключа. Если токенов нет, возвращает false и время до следующего токена.
func (l *limiter) allow(key string) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := l.now()
	l.sweep(now)

	b, ok := l.buckets[key]
	if !ok {
		if len(l.buckets) >= maxLimiterKeys {
			// Памяти больше не даём: лучше пропустить запрос без учёта, чем отказать всем новым клиентам.
			return true, 0
		}
		b = &bucket{tokens: l.burst, last: now}
		l.buckets[key] = b
	}

	if elapsed := now.Sub(b.last).Seconds(); elapsed > 0 {
		b.tokens = math.Min(l.burst, b.tokens+elapsed*l.rate)
		b.last = now
	}
	if b.tokens >= 1 {
		b.tokens--
		return true, 0
	}
	return false, time.Duration((1 - b.tokens) / l.rate * float64(time.Second))
}

// sweep удаляет вёдра, которые успели наполниться доверху: они равнозначны новому ведру.
func (l *limiter) sweep(now time.Time) {
	if now.Sub(l.lastSweep) < sweepInterval && len(l.buckets) < maxLimiterKeys {
		return
	}
	l.lastSweep = now
	for k, b := range l.buckets {
		if b.tokens+now.Sub(b.last).Seconds()*l.rate >= l.burst {
			delete(l.buckets, k)
		}
	}
}

// limitBy возвращает middleware, который пропускает запрос, пока у его ключа есть токены, иначе отвечает 429.
// Если ключ определить не удалось (key вернул false), запрос пропускается: лимит не должен ломать запросы.
func limitBy(rl RateLimit, key func(*http.Request) (string, bool)) func(http.Handler) http.Handler {
	l := newLimiter(rl)
	return func(next http.Handler) http.Handler {
		if l == nil {
			return next
		}
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			k, ok := key(r)
			if !ok {
				next.ServeHTTP(w, r)
				return
			}
			if allowed, wait := l.allow(k); !allowed {
				w.Header().Set("Retry-After", strconv.Itoa(int(math.Ceil(wait.Seconds()))))
				writeError(w, http.StatusTooManyRequests, "rate_limited", "too many requests, slow down")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// limitByIP ограничивает по IP клиента.
func limitByIP(rl RateLimit, trustProxy bool) func(http.Handler) http.Handler {
	return limitBy(rl, func(r *http.Request) (string, bool) { return clientIP(r, trustProxy), true })
}

// limitByUser ограничивает по id пользователя; ставится после Authenticate.
func limitByUser(rl RateLimit) func(http.Handler) http.Handler {
	return limitBy(rl, func(r *http.Request) (string, bool) {
		id, ok := UserIDFromContext(r.Context())
		return strconv.Itoa(id), ok
	})
}

// clientIP определяет адрес клиента. IPv6 сворачивается до сети /64: у одного абонента их обычно сколько угодно,
// и иначе лимит обходился бы сменой адреса.
func clientIP(r *http.Request, trustProxy bool) string {
	if trustProxy {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			// последнее значение дописал наш прокси, всё левее - слова клиента
			parts := strings.Split(xff, ",")
			if ip := net.ParseIP(strings.TrimSpace(parts[len(parts)-1])); ip != nil {
				return normalizeIP(ip)
			}
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	if ip := net.ParseIP(host); ip != nil {
		return normalizeIP(ip)
	}
	return host
}

func normalizeIP(ip net.IP) string {
	if v4 := ip.To4(); v4 != nil {
		return v4.String()
	}
	return ip.Mask(net.CIDRMask(64, 128)).String()
}
