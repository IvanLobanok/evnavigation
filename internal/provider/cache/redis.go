package cache

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// RedisStore — реализация Store поверх Redis.
type RedisStore struct {
	client *redis.Client
}

var _ Store = (*RedisStore)(nil)

func NewRedisStore(client *redis.Client) *RedisStore {
	return &RedisStore{client: client}
}

func (s *RedisStore) Get(ctx context.Context, key string) ([]byte, bool, error) {
	val, err := s.client.Get(ctx, key).Bytes()
	if errors.Is(err, redis.Nil) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return val, true, nil
}

func (s *RedisStore) Set(ctx context.Context, key string, value []byte, ttl time.Duration) error {
	return s.client.Set(ctx, key, value, ttl).Err()
}

// NewRedisClient создаёт клиент по URL вида redis://[:password@]host:6379/0.
// Таймауты короткие: кэш не стоит того, чтобы запрос пользователя ждал зависший Redis.
// Соединение лениво: если Redis сейчас недоступен, клиент подключится, когда он появится.
func NewRedisClient(rawURL string) (*redis.Client, error) {
	opts, err := redis.ParseURL(rawURL)
	if err != nil {
		return nil, fmt.Errorf("parse redis url: %w", err)
	}
	opts.DialTimeout = time.Second
	opts.ReadTimeout = 300 * time.Millisecond
	opts.WriteTimeout = 300 * time.Millisecond
	opts.MaxRetries = 1
	return redis.NewClient(opts), nil
}
