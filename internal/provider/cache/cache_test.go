package cache

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/IvanLobanok/evnavigation/internal/models/route"
)

var (
	errBoom = errors.New("boom")

	minsk      = route.RoutePoint{Lat: 53.9006, Lon: 27.5590}
	dzerzhinsk = route.RoutePoint{Lat: 53.6833, Lon: 27.1333}
)

func segments() []route.RouteSegment {
	return []route.RouteSegment{
		{From: minsk, To: route.RoutePoint{Lat: 53.8, Lon: 27.4}, DistanceMeters: 5000},
		{From: route.RoutePoint{Lat: 53.8, Lon: 27.4}, To: dzerzhinsk, DistanceMeters: 4321.5},
	}
}

// ---------------------------------------------------------------------------
// заглушки
// ---------------------------------------------------------------------------

type fakeStore struct {
	mu   sync.Mutex
	data map[string][]byte
	ttls map[string]time.Duration

	getErr, setErr   error
	hangGet, hangSet bool // блокироваться, пока не истечёт контекст
	gets, sets       int
}

func newFakeStore() *fakeStore {
	return &fakeStore{data: map[string][]byte{}, ttls: map[string]time.Duration{}}
}

func (f *fakeStore) Get(ctx context.Context, key string) ([]byte, bool, error) {
	f.mu.Lock()
	f.gets++
	hang, err := f.hangGet, f.getErr
	v, ok := f.data[key]
	f.mu.Unlock()

	if hang {
		<-ctx.Done()
		return nil, false, ctx.Err()
	}
	if err != nil {
		return nil, false, err
	}
	return v, ok, nil
}

func (f *fakeStore) Set(ctx context.Context, key string, value []byte, ttl time.Duration) error {
	f.mu.Lock()
	f.sets++
	hang, err := f.hangSet, f.setErr
	f.mu.Unlock()

	if hang {
		<-ctx.Done()
		return ctx.Err()
	}
	if err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.data[key] = append([]byte(nil), value...)
	f.ttls[key] = ttl
	return nil
}

func (f *fakeStore) size() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.data)
}

func (f *fakeStore) only() (key string, value []byte) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for k, v := range f.data {
		return k, v
	}
	return "", nil
}

type countingProvider struct {
	calls atomic.Int32
	segs  []route.RouteSegment
	err   error

	gate    chan struct{} // если не nil, вызов ждёт, пока канал не закроют
	started chan struct{} // сюда сигналим о начале первого вызова
	once    sync.Once
}

func (c *countingProvider) BuildRoute(ctx context.Context, _, _ route.RoutePoint) ([]route.RouteSegment, error) {
	c.calls.Add(1)
	if c.started != nil {
		c.once.Do(func() { close(c.started) })
	}
	if c.gate != nil {
		<-c.gate
	}
	if err := ctx.Err(); err != nil { // настоящий провайдер тоже уважает отмену контекста
		return nil, err
	}
	if c.err != nil {
		return nil, c.err
	}
	return cloneSegments(c.segs), nil
}

func newProvider(t *testing.T, next *countingProvider, store Store, opts ...Option) *Provider {
	t.Helper()
	p, err := New(next, store, 10*time.Minute, opts...)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return p
}

func quietLogger() (*slog.Logger, *bytes.Buffer) {
	var buf bytes.Buffer
	return slog.New(slog.NewTextHandler(&buf, nil)), &buf
}

// ---------------------------------------------------------------------------
// New
// ---------------------------------------------------------------------------

func TestNew_Validation(t *testing.T) {
	next, store := &countingProvider{}, newFakeStore()

	tests := []struct {
		name  string
		next  *countingProvider
		store Store
		ttl   time.Duration
	}{
		{"nil provider", nil, store, time.Minute},
		{"nil store", next, nil, time.Minute},
		{"zero ttl", next, store, 0},
		{"negative ttl", next, store, -time.Second},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var nextIface interface {
				BuildRoute(context.Context, route.RoutePoint, route.RoutePoint) ([]route.RouteSegment, error)
			}
			if tc.next != nil {
				nextIface = tc.next
			}
			var p *Provider
			var err error
			if nextIface == nil {
				p, err = New(nil, tc.store, tc.ttl)
			} else {
				p, err = New(nextIface, tc.store, tc.ttl)
			}
			if err == nil || p != nil {
				t.Fatalf("expected an error and a nil provider, got p=%v err=%v", p, err)
			}
		})
	}
}

func TestOptions_IgnoreInvalidValues(t *testing.T) {
	p := newProvider(t, &countingProvider{}, newFakeStore(), WithLogger(nil), WithStoreTimeout(0), WithFetchTimeout(-1))

	if p.log == nil || p.storeTimeout != defaultStoreTimeout || p.fetchTimeout != defaultFetchTimeout {
		t.Errorf("invalid options must keep the defaults: %+v", p)
	}
}

// ---------------------------------------------------------------------------
// попадания и промахи
// ---------------------------------------------------------------------------

func TestBuildRoute_MissThenHit(t *testing.T) {
	next := &countingProvider{segs: segments()}
	store := newFakeStore()
	p := newProvider(t, next, store)

	first, err := p.BuildRoute(context.Background(), minsk, dzerzhinsk)
	if err != nil {
		t.Fatal(err)
	}
	second, err := p.BuildRoute(context.Background(), minsk, dzerzhinsk)
	if err != nil {
		t.Fatal(err)
	}

	if next.calls.Load() != 1 {
		t.Errorf("provider calls: got %d, want 1 (the second request must be served from the cache)", next.calls.Load())
	}
	if len(first) != 2 || len(second) != 2 {
		t.Fatalf("segments: %d and %d", len(first), len(second))
	}
	for i := range first {
		if first[i] != second[i] || first[i] != segments()[i] {
			t.Errorf("segment %d differs after the round trip through the cache:\n first  %+v\n second %+v\n want   %+v", i, first[i], second[i], segments()[i])
		}
	}

	key, _ := store.only()
	if key != "route:v1:53.9006,27.5590:53.6833,27.1333" {
		t.Errorf("key: %q", key)
	}
	if store.ttls[key] != 10*time.Minute {
		t.Errorf("ttl: got %v, want 10m", store.ttls[key])
	}
}

func TestBuildRoute_NearbyPointsShareAnEntryButDistantOnesDoNot(t *testing.T) {
	tests := []struct {
		name         string
		from, to     route.RoutePoint
		wantProvider int32
	}{
		{"the same points", minsk, dzerzhinsk, 1},
		{"a few metres away (same 4th decimal after rounding)", route.RoutePoint{Lat: 53.90061, Lon: 27.55899}, route.RoutePoint{Lat: 53.68332, Lon: 27.13328}, 1},
		{"about 100 m away", route.RoutePoint{Lat: 53.9016, Lon: 27.5590}, dzerzhinsk, 2},
		{"the opposite direction", dzerzhinsk, minsk, 2},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			next := &countingProvider{segs: segments()}
			p := newProvider(t, next, newFakeStore())

			if _, err := p.BuildRoute(context.Background(), minsk, dzerzhinsk); err != nil {
				t.Fatal(err)
			}
			if _, err := p.BuildRoute(context.Background(), tc.from, tc.to); err != nil {
				t.Fatal(err)
			}
			if got := next.calls.Load(); got != tc.wantProvider {
				t.Errorf("provider calls: got %d, want %d", got, tc.wantProvider)
			}
		})
	}
}

func TestCacheKey(t *testing.T) {
	tests := []struct {
		name     string
		from, to route.RoutePoint
		want     string
	}{
		{"typical", minsk, dzerzhinsk, "route:v1:53.9006,27.5590:53.6833,27.1333"},
		{"rounds to 4 decimals", route.RoutePoint{Lat: 53.90064, Lon: 27.55896}, route.RoutePoint{Lat: 1, Lon: 2}, "route:v1:53.9006,27.5590:1.0000,2.0000"},
		{"negative coordinates", route.RoutePoint{Lat: -33.9, Lon: -70.7}, route.RoutePoint{Lat: -34.6, Lon: -58.4}, "route:v1:-33.9000,-70.7000:-34.6000,-58.4000"},
		{"negative zero is normalised", route.RoutePoint{Lat: -0.00001, Lon: 0}, route.RoutePoint{Lat: 0.00001, Lon: -0.00004}, "route:v1:0.0000,0.0000:0.0000,0.0000"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := cacheKey(tc.from, tc.to); got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestEncodeDecodeRoundTrip(t *testing.T) {
	in := segments()
	raw, err := encodeSegments(in)
	if err != nil {
		t.Fatal(err)
	}
	out, err := decodeSegments(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != len(in) {
		t.Fatalf("length: %d vs %d", len(out), len(in))
	}
	for i := range in {
		if in[i] != out[i] {
			t.Errorf("segment %d: %+v vs %+v", i, in[i], out[i])
		}
	}
}

// ---------------------------------------------------------------------------
// сбои кэша не ломают запрос
// ---------------------------------------------------------------------------

func TestStoreReadErrorFallsBackToTheProvider(t *testing.T) {
	logger, logs := quietLogger()
	next := &countingProvider{segs: segments()}
	store := newFakeStore()
	store.getErr = errors.New("redis: connection refused")
	p := newProvider(t, next, store, WithLogger(logger))

	got, err := p.BuildRoute(context.Background(), minsk, dzerzhinsk)

	if err != nil || len(got) != 2 {
		t.Fatalf("a cache failure must not fail the request: n=%d err=%v", len(got), err)
	}
	if next.calls.Load() != 1 {
		t.Errorf("provider calls: %d", next.calls.Load())
	}
	if !strings.Contains(logs.String(), "route cache read failed") || !strings.Contains(logs.String(), "connection refused") {
		t.Errorf("the failure must be logged: %s", logs.String())
	}
}

func TestStoreWriteErrorStillReturnsTheRoute(t *testing.T) {
	logger, logs := quietLogger()
	next := &countingProvider{segs: segments()}
	store := newFakeStore()
	store.setErr = errors.New("redis: OOM")
	p := newProvider(t, next, store, WithLogger(logger))

	got, err := p.BuildRoute(context.Background(), minsk, dzerzhinsk)

	if err != nil || len(got) != 2 {
		t.Fatalf("n=%d err=%v", len(got), err)
	}
	if !strings.Contains(logs.String(), "route cache write failed") {
		t.Errorf("the failure must be logged: %s", logs.String())
	}
}

func TestCorruptEntriesAreRefetchedAndOverwritten(t *testing.T) {
	corrupt := map[string]string{
		"not json":          `{{{`,
		"empty list":        `[]`,
		"negative length":   `[{"a":1,"b":2,"c":3,"d":4,"m":-5}]`,
		"wrong type":        `"string"`,
		"empty value":       ``,
		"truncated payload": `[{"a":1,"b":2`,
	}
	for name, raw := range corrupt {
		t.Run(name, func(t *testing.T) {
			logger, logs := quietLogger()
			next := &countingProvider{segs: segments()}
			store := newFakeStore()
			store.data[cacheKey(minsk, dzerzhinsk)] = []byte(raw)
			p := newProvider(t, next, store, WithLogger(logger))

			got, err := p.BuildRoute(context.Background(), minsk, dzerzhinsk)

			if err != nil || len(got) != 2 {
				t.Fatalf("n=%d err=%v", len(got), err)
			}
			if next.calls.Load() != 1 {
				t.Errorf("a corrupt entry must trigger a provider call, got %d", next.calls.Load())
			}
			if !strings.Contains(logs.String(), "corrupt") {
				t.Errorf("corruption must be logged: %s", logs.String())
			}
			// запись починена: следующий запрос берёт её из кэша
			if _, err := p.BuildRoute(context.Background(), minsk, dzerzhinsk); err != nil || next.calls.Load() != 1 {
				t.Errorf("the repaired entry must be served from the cache: calls=%d err=%v", next.calls.Load(), err)
			}
		})
	}
}

func TestHangingStoreDoesNotBlockRequests(t *testing.T) {
	next := &countingProvider{segs: segments()}
	store := newFakeStore()
	store.hangGet, store.hangSet = true, true
	p := newProvider(t, next, store, WithStoreTimeout(30*time.Millisecond), WithLogger(slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil))))

	start := time.Now()
	got, err := p.BuildRoute(context.Background(), minsk, dzerzhinsk)
	elapsed := time.Since(start)

	if err != nil || len(got) != 2 {
		t.Fatalf("n=%d err=%v", len(got), err)
	}
	// два обращения к зависшему хранилищу (чтение до вызова, проверка внутри очереди) и запись: по 30 мс
	if elapsed > time.Second {
		t.Errorf("a hanging cache must be cut off by its own timeout, took %v", elapsed)
	}
}

// ---------------------------------------------------------------------------
// ошибки провайдера и пустые ответы
// ---------------------------------------------------------------------------

func TestProviderErrorIsReturnedAndNotCached(t *testing.T) {
	next := &countingProvider{err: errBoom}
	store := newFakeStore()
	p := newProvider(t, next, store)

	if _, err := p.BuildRoute(context.Background(), minsk, dzerzhinsk); !errors.Is(err, errBoom) {
		t.Fatalf("got %v, want errBoom", err)
	}
	if store.size() != 0 {
		t.Error("an error must not be cached")
	}

	// провайдер «починился»: следующий запрос обязан сходить к нему снова, а не получить закэшированную ошибку
	next.err, next.segs = nil, segments()
	if got, err := p.BuildRoute(context.Background(), minsk, dzerzhinsk); err != nil || len(got) != 2 {
		t.Fatalf("n=%d err=%v", len(got), err)
	}
	if next.calls.Load() != 2 || store.size() != 1 {
		t.Errorf("calls=%d entries=%d, want 2 and 1", next.calls.Load(), store.size())
	}
}

func TestEmptyRouteIsNotCached(t *testing.T) {
	next := &countingProvider{segs: nil}
	store := newFakeStore()
	p := newProvider(t, next, store)

	got, err := p.BuildRoute(context.Background(), minsk, dzerzhinsk)

	if err != nil || len(got) != 0 {
		t.Fatalf("n=%d err=%v", len(got), err)
	}
	if store.sets != 0 {
		t.Error("an empty route must not be written to the cache")
	}
}

func TestReturnedSegmentsAreACopy(t *testing.T) {
	next := &countingProvider{segs: segments()}
	p := newProvider(t, next, newFakeStore())

	first, _ := p.BuildRoute(context.Background(), minsk, dzerzhinsk)
	first[0].DistanceMeters = 999999 // вызывающий портит свой срез

	second, _ := p.BuildRoute(context.Background(), minsk, dzerzhinsk)
	if second[0].DistanceMeters != 5000 {
		t.Errorf("mutating a result must not affect later responses, got %v", second[0].DistanceMeters)
	}
}

// ---------------------------------------------------------------------------
// параллельные запросы
// ---------------------------------------------------------------------------

func TestConcurrentIdenticalRequestsCollapseIntoOneProviderCall(t *testing.T) {
	next := &countingProvider{segs: segments(), gate: make(chan struct{}), started: make(chan struct{})}
	p := newProvider(t, next, newFakeStore())

	const callers = 20
	results := make([][]route.RouteSegment, callers)
	errs := make([]error, callers)
	var wg sync.WaitGroup
	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[i], errs[i] = p.BuildRoute(context.Background(), minsk, dzerzhinsk)
		}()
	}

	<-next.started                    // провайдер вызван, и он удерживается на gate
	time.Sleep(50 * time.Millisecond) // даём остальным дойти до ожидания
	close(next.gate)
	wg.Wait()

	if got := next.calls.Load(); got != 1 {
		t.Errorf("provider calls: got %d, want 1", got)
	}
	for i := range results {
		if errs[i] != nil || len(results[i]) != 2 {
			t.Fatalf("caller %d: n=%d err=%v", i, len(results[i]), errs[i])
		}
	}
	// у каждого свой срез: запись в один не видна в другом
	results[0][0].DistanceMeters = -1
	if results[1][0].DistanceMeters != 5000 {
		t.Error("callers must not share the same slice")
	}
}

func TestDifferentRoutesAreFetchedIndependently(t *testing.T) {
	next := &countingProvider{segs: segments()}
	p := newProvider(t, next, newFakeStore())

	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			to := route.RoutePoint{Lat: 50 + float64(i), Lon: 20}
			if _, err := p.BuildRoute(context.Background(), minsk, to); err != nil {
				t.Errorf("route %d: %v", i, err)
			}
		}()
	}
	wg.Wait()

	if got := next.calls.Load(); got != 5 {
		t.Errorf("provider calls: got %d, want 5", got)
	}
}

func TestCallerCancellationDoesNotBreakOthers(t *testing.T) {
	next := &countingProvider{segs: segments(), gate: make(chan struct{}), started: make(chan struct{})}
	store := newFakeStore()
	p := newProvider(t, next, store)

	// первый вызывающий — тот, чей запрос реально уходит к провайдеру — потом уходит
	ctxA, cancelA := context.WithCancel(context.Background())
	errA := make(chan error, 1)
	go func() {
		_, err := p.BuildRoute(ctxA, minsk, dzerzhinsk)
		errA <- err
	}()
	<-next.started

	// второй ждёт тот же маршрут
	type result struct {
		segs []route.RouteSegment
		err  error
	}
	resB := make(chan result, 1)
	go func() {
		segs, err := p.BuildRoute(context.Background(), minsk, dzerzhinsk)
		resB <- result{segs, err}
	}()
	time.Sleep(50 * time.Millisecond)

	cancelA()
	if err := <-errA; !errors.Is(err, context.Canceled) {
		t.Fatalf("the caller that left must get its own context error, got %v", err)
	}

	close(next.gate) // провайдер отвечает уже после ухода первого
	got := <-resB
	if got.err != nil || len(got.segs) != 2 {
		t.Fatalf("the waiting caller must still get the route: n=%d err=%v", len(got.segs), got.err)
	}
	if store.size() != 1 {
		t.Error("the route fetched for the departed caller must still be cached")
	}
	if next.calls.Load() != 1 {
		t.Errorf("provider calls: %d", next.calls.Load())
	}
}

func TestAlreadyCancelledContextReturnsPromptly(t *testing.T) {
	next := &countingProvider{segs: segments(), gate: make(chan struct{})}
	defer close(next.gate)
	p := newProvider(t, next, newFakeStore())

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	done := make(chan error, 1)
	go func() {
		_, err := p.BuildRoute(ctx, minsk, dzerzhinsk)
		done <- err
	}()

	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("got %v, want context.Canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("a cancelled caller must not wait for a slow provider")
	}
}

// firstGetMissesStore притворяется, что первое чтение ничего не нашло, хотя запись уже есть:
// так выглядит ситуация, когда другой вызов (или другой экземпляр приложения) успел
// положить ответ в кэш между нашей первой проверкой и походом к провайдеру.
type firstGetMissesStore struct {
	*fakeStore
	mu   sync.Mutex
	seen int
}

func (s *firstGetMissesStore) Get(ctx context.Context, key string) ([]byte, bool, error) {
	s.mu.Lock()
	s.seen++
	first := s.seen == 1
	s.mu.Unlock()
	if first {
		return nil, false, nil
	}
	return s.fakeStore.Get(ctx, key)
}

func TestSecondLookupBeforeTheProviderCallAvoidsAPaidRequest(t *testing.T) {
	inner := newFakeStore()
	raw, err := encodeSegments(segments())
	if err != nil {
		t.Fatal(err)
	}
	inner.data[cacheKey(minsk, dzerzhinsk)] = raw

	next := &countingProvider{segs: segments()}
	p := newProvider(t, next, &firstGetMissesStore{fakeStore: inner})

	got, err := p.BuildRoute(context.Background(), minsk, dzerzhinsk)

	if err != nil || len(got) != 2 {
		t.Fatalf("n=%d err=%v", len(got), err)
	}
	if calls := next.calls.Load(); calls != 0 {
		t.Errorf("the entry appeared in the cache before the provider call, so the paid request must be skipped (calls=%d)", calls)
	}
}
