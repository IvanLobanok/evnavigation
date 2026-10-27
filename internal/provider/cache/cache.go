// Package cache кэширует ответы routing-провайдера (на практике — платного Яндекс.Router).
//
// Provider — декоратор: он реализует тот же route.RouteProvider и оборачивает любого другого,
// поэтому routing.Service о кэше ничего не знает. Кэш — только ускоритель: любой его сбой
// (Redis недоступен, медленный, в записи мусор) превращается в обычный вызов провайдера.
package cache

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"time"

	"github.com/IvanLobanok/evnavigation/internal/models/route"
	"golang.org/x/sync/singleflight"
)

const (
	// версия формата в ключе: при смене формата записи достаточно поднять её, и старые записи просто перестанут находиться
	keyPrefix = "route:v1:"

	defaultStoreTimeout = 250 * time.Millisecond
	defaultFetchTimeout = 15 * time.Second
)

// Store — хранилище «ключ → байты с временем жизни» (реализует RedisStore).
type Store interface {
	Get(ctx context.Context, key string) (value []byte, found bool, err error)
	Set(ctx context.Context, key string, value []byte, ttl time.Duration) error
}

type Option func(*Provider)

func WithLogger(l *slog.Logger) Option {
	return func(p *Provider) {
		if l != nil {
			p.log = l
		}
	}
}

// WithStoreTimeout ограничивает каждую операцию с хранилищем: зависший Redis не должен тормозить запросы.
func WithStoreTimeout(d time.Duration) Option {
	return func(p *Provider) {
		if d > 0 {
			p.storeTimeout = d
		}
	}
}

// WithFetchTimeout ограничивает один вызов вложенного провайдера.
func WithFetchTimeout(d time.Duration) Option {
	return func(p *Provider) {
		if d > 0 {
			p.fetchTimeout = d
		}
	}
}

type Provider struct {
	next         route.RouteProvider
	store        Store
	ttl          time.Duration
	log          *slog.Logger
	storeTimeout time.Duration
	fetchTimeout time.Duration
	group        singleflight.Group
}

var _ route.RouteProvider = (*Provider)(nil)

func New(next route.RouteProvider, store Store, ttl time.Duration, opts ...Option) (*Provider, error) {
	if next == nil {
		return nil, errors.New("cache: route provider must not be nil")
	}
	if store == nil {
		return nil, errors.New("cache: store must not be nil")
	}
	if ttl <= 0 {
		return nil, errors.New("cache: ttl must be positive")
	}

	p := &Provider{
		next:         next,
		store:        store,
		ttl:          ttl,
		log:          slog.Default(),
		storeTimeout: defaultStoreTimeout,
		fetchTimeout: defaultFetchTimeout,
	}
	for _, opt := range opts {
		opt(p)
	}
	return p, nil
}

// BuildRoute отдаёт маршрут из кэша или, если его там нет, строит его вложенным провайдером и запоминает.
//
// Координаты в ключе округляются до 4 знаков (около 11 м): близкие точки делят одну запись.
// Цена этого — маршрут из кэша может начинаться в паре метров от запрошенной точки, для расчёта заряда это ничтожно.
// Направление важно: A→B и B→A — разные записи (односторонние улицы).
func (p *Provider) BuildRoute(ctx context.Context, from, to route.RoutePoint) ([]route.RouteSegment, error) {
	key := cacheKey(from, to)

	if segs, ok := p.lookup(ctx, key); ok {
		return segs, nil
	}

	// Одинаковые запросы, пришедшие одновременно, схлопываются в один вызов провайдера.
	// Вызов отвязан от отмены контекста первого клиента: иначе его уход обрубил бы запрос всем, кто ждёт тот же маршрут.
	// Сам клиент при этом по-прежнему выходит по своему ctx.
	ch := p.group.DoChan(key, func() (any, error) {
		fetchCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), p.fetchTimeout)
		defer cancel()

		// пока мы стояли в очереди за предыдущим вызовом, он мог уже положить ответ в кэш: платный вызов тогда не нужен
		if segs, ok := p.lookup(fetchCtx, key); ok {
			return segs, nil
		}

		segs, err := p.next.BuildRoute(fetchCtx, from, to)
		if err != nil {
			return nil, err // ошибки не кэшируем: следующий запрос должен попробовать снова
		}
		p.save(fetchCtx, key, segs)
		return segs, nil
	})

	select {
	case res := <-ch:
		if res.Err != nil {
			return nil, res.Err
		}
		// каждому вызывающему — своя копия: общий срез никто не испортит
		return cloneSegments(res.Val.([]route.RouteSegment)), nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (p *Provider) lookup(ctx context.Context, key string) ([]route.RouteSegment, bool) {
	sctx, cancel := context.WithTimeout(ctx, p.storeTimeout)
	defer cancel()

	raw, found, err := p.store.Get(sctx, key)
	if err != nil {
		p.log.Warn("route cache read failed, falling back to the provider", "error", err)
		return nil, false
	}
	if !found {
		return nil, false
	}

	segs, err := decodeSegments(raw)
	if err != nil {
		p.log.Warn("route cache entry is corrupt, refetching", "key", key, "error", err)
		return nil, false
	}
	return segs, true
}

func (p *Provider) save(ctx context.Context, key string, segs []route.RouteSegment) {
	if len(segs) == 0 {
		return // пустой маршрут — не ответ, который стоит помнить
	}
	raw, err := encodeSegments(segs)
	if err != nil {
		p.log.Warn("route cache encode failed", "error", err)
		return
	}

	sctx, cancel := context.WithTimeout(ctx, p.storeTimeout)
	defer cancel()
	if err := p.store.Set(sctx, key, raw, p.ttl); err != nil {
		p.log.Warn("route cache write failed", "error", err)
	}
}

// ---------------------------------------------------------------------------
// ключ и формат записи
// ---------------------------------------------------------------------------

func cacheKey(from, to route.RoutePoint) string {
	return keyPrefix + coord(from.Lat) + "," + coord(from.Lon) + ":" + coord(to.Lat) + "," + coord(to.Lon)
}

func coord(v float64) string {
	s := strconv.FormatFloat(v, 'f', 4, 64)
	if s == "-0.0000" { // малые отрицательные значения не должны давать отдельный ключ
		return "0.0000"
	}
	return s
}

// segmentRecord — компактный формат записи; route.RouteSegment наружу не светим, чтобы его изменения не ломали кэш молча.
type segmentRecord struct {
	FromLat float64 `json:"a"`
	FromLon float64 `json:"b"`
	ToLat   float64 `json:"c"`
	ToLon   float64 `json:"d"`
	Meters  float64 `json:"m"`
}

func encodeSegments(segs []route.RouteSegment) ([]byte, error) {
	recs := make([]segmentRecord, len(segs))
	for i, s := range segs {
		recs[i] = segmentRecord{s.From.Lat, s.From.Lon, s.To.Lat, s.To.Lon, s.DistanceMeters}
	}
	return json.Marshal(recs)
}

func decodeSegments(raw []byte) ([]route.RouteSegment, error) {
	var recs []segmentRecord
	if err := json.Unmarshal(raw, &recs); err != nil {
		return nil, err
	}
	if len(recs) == 0 {
		return nil, errors.New("entry has no segments")
	}
	segs := make([]route.RouteSegment, len(recs))
	for i, r := range recs {
		if r.Meters < 0 {
			return nil, fmt.Errorf("segment %d has a negative length", i)
		}
		segs[i] = route.RouteSegment{
			From:           route.RoutePoint{Lat: r.FromLat, Lon: r.FromLon},
			To:             route.RoutePoint{Lat: r.ToLat, Lon: r.ToLon},
			DistanceMeters: r.Meters,
		}
	}
	return segs, nil
}

func cloneSegments(in []route.RouteSegment) []route.RouteSegment {
	out := make([]route.RouteSegment, len(in))
	copy(out, in)
	return out
}
