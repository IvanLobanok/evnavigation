//go:build integration

package cache

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/IvanLobanok/evnavigation/internal/models/route"
	"github.com/redis/go-redis/v9"
)

// Тесты работают с настоящим Redis:
//
//	docker run --rm -p 6379:6379 redis:7-alpine
//	TEST_REDIS_URL=redis://localhost:6379/0 go test -tags=integration ./internal/provider/cache/
//
// Ключи уникальны для каждого теста и удаляются после него, поэтому базу чистить не нужно.

func newTestRedis(t *testing.T) (*redis.Client, *RedisStore) {
	t.Helper()
	url := os.Getenv("TEST_REDIS_URL")
	if url == "" {
		t.Skip("TEST_REDIS_URL is not set")
	}
	client, err := NewRedisClient(url)
	if err != nil {
		t.Fatalf("NewRedisClient: %v", err)
	}
	if err := client.Ping(context.Background()).Err(); err != nil {
		t.Fatalf("redis is not reachable at %s: %v", url, err)
	}
	t.Cleanup(func() { _ = client.Close() })
	return client, NewRedisStore(client)
}

func uniqueKey(t *testing.T) string {
	t.Helper()
	return fmt.Sprintf("test:%s:%d", t.Name(), time.Now().UnixNano())
}

func TestRedisStore_SetAndGet(t *testing.T) {
	client, store := newTestRedis(t)
	ctx := context.Background()
	key := uniqueKey(t)
	t.Cleanup(func() { client.Del(ctx, key) })

	if err := store.Set(ctx, key, []byte(`[{"m":1}]`), time.Minute); err != nil {
		t.Fatalf("Set: %v", err)
	}
	got, found, err := store.Get(ctx, key)
	if err != nil || !found || string(got) != `[{"m":1}]` {
		t.Fatalf("Get: value=%q found=%v err=%v", got, found, err)
	}
}

func TestRedisStore_MissIsNotAnError(t *testing.T) {
	_, store := newTestRedis(t)

	got, found, err := store.Get(context.Background(), uniqueKey(t))

	if err != nil || found || got != nil {
		t.Errorf("a missing key must be (nil, false, nil), got %q %v %v", got, found, err)
	}
}

func TestRedisStore_ValueExpires(t *testing.T) {
	client, store := newTestRedis(t)
	ctx := context.Background()
	key := uniqueKey(t)
	t.Cleanup(func() { client.Del(ctx, key) })

	if err := store.Set(ctx, key, []byte("x"), 150*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	if ttl := client.PTTL(ctx, key).Val(); ttl <= 0 || ttl > 150*time.Millisecond {
		t.Errorf("the key must carry the requested ttl, got %v", ttl)
	}

	time.Sleep(300 * time.Millisecond)
	if _, found, err := store.Get(ctx, key); err != nil || found {
		t.Errorf("the entry must expire: found=%v err=%v", found, err)
	}
}

func TestRedisStore_BinarySafeValues(t *testing.T) {
	client, store := newTestRedis(t)
	ctx := context.Background()
	key := uniqueKey(t)
	t.Cleanup(func() { client.Del(ctx, key) })

	want := []byte{0, 1, 2, 255, '\n', 0}
	if err := store.Set(ctx, key, want, time.Minute); err != nil {
		t.Fatal(err)
	}
	got, _, _ := store.Get(ctx, key)
	if string(got) != string(want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestNewRedisClient_RejectsBadURL(t *testing.T) {
	for _, u := range []string{"", "not a url", "http://localhost:6379", "redis://localhost:notaport"} {
		if c, err := NewRedisClient(u); err == nil {
			_ = c.Close()
			t.Errorf("url %q must be rejected", u)
		}
	}
}

func TestRedisStore_UnreachableServerFailsFast(t *testing.T) {
	// порт, на котором никого нет: запрос должен упасть быстро, а не висеть
	client, err := NewRedisClient("redis://127.0.0.1:1/0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })

	start := time.Now()
	_, _, err = NewRedisStore(client).Get(context.Background(), "k")

	if err == nil {
		t.Fatal("expected a connection error")
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Errorf("an unreachable Redis must fail fast, took %v", elapsed)
	}
}

// Весь стек: декоратор + настоящий Redis + (подсчитывающий вызовы) провайдер.
func TestProviderWithRedis_SecondRequestComesFromRedis(t *testing.T) {
	client, store := newTestRedis(t)
	ctx := context.Background()

	// уникальные координаты, чтобы не пересечься с другими прогонами
	salt := float64(time.Now().UnixNano()%1000) / 10000
	from := route.RoutePoint{Lat: 10 + salt, Lon: 20}
	to := route.RoutePoint{Lat: 11 + salt, Lon: 21}
	t.Cleanup(func() { client.Del(ctx, cacheKey(from, to)) })

	next := &countingProvider{segs: segments()}
	p, err := New(next, store, time.Minute)
	if err != nil {
		t.Fatal(err)
	}

	first, err := p.BuildRoute(ctx, from, to)
	if err != nil {
		t.Fatal(err)
	}
	second, err := p.BuildRoute(ctx, from, to)
	if err != nil {
		t.Fatal(err)
	}

	if next.calls.Load() != 1 {
		t.Errorf("provider calls: got %d, want 1", next.calls.Load())
	}
	if len(second) != len(first) || second[1] != first[1] {
		t.Errorf("a route read back from Redis must equal the original:\n%+v\n%+v", first, second)
	}
	if ttl := client.TTL(ctx, cacheKey(from, to)).Val(); ttl <= 0 || ttl > time.Minute {
		t.Errorf("the cached route must have a ttl, got %v", ttl)
	}
}

func TestProviderWithRedis_SurvivesRedisOutage(t *testing.T) {
	// Redis недоступен: маршрут всё равно строится, просто без кэша
	client, err := NewRedisClient("redis://127.0.0.1:1/0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })

	next := &countingProvider{segs: segments()}
	p, err := New(next, NewRedisStore(client), time.Minute)
	if err != nil {
		t.Fatal(err)
	}

	got, err := p.BuildRoute(context.Background(), minsk, dzerzhinsk)
	if err != nil || len(got) != 2 {
		t.Fatalf("n=%d err=%v", len(got), err)
	}
}
