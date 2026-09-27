package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// ErrNotFound 键不存在
var ErrNotFound = errors.New("not found")

// Store Redis 存储封装
type Store struct {
	R *redis.Client
}

func New(addr, password string, db int) *Store {
	return &Store{R: redis.NewClient(&redis.Options{Addr: addr, Password: password, DB: db})}
}

func (s *Store) Ping(ctx context.Context) error { return s.R.Ping(ctx).Err() }

func (s *Store) SetJSON(ctx context.Context, key string, v interface{}) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return s.R.Set(ctx, key, b, 0).Err()
}

func (s *Store) GetJSON(ctx context.Context, key string, out interface{}) error {
	b, err := s.R.Get(ctx, key).Bytes()
	if errors.Is(err, redis.Nil) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	return json.Unmarshal(b, out)
}

// NextID 生成业务 ID，如 P000001
func (s *Store) NextID(ctx context.Context, seq, prefix string) (string, error) {
	n, err := s.R.Incr(ctx, "seq:"+seq).Result()
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%s%06d", prefix, n), nil
}

// Lock 简单分布式锁，返回 token；ok=false 表示已被占用
func (s *Store) Lock(ctx context.Context, name string, ttl time.Duration) (token string, ok bool, err error) {
	token = fmt.Sprintf("%d", time.Now().UnixNano())
	ok, err = s.R.SetNX(ctx, "lock:"+name, token, ttl).Result()
	return token, ok, err
}

func (s *Store) Unlock(ctx context.Context, name, token string) {
	script := redis.NewScript(`if redis.call("GET", KEYS[1]) == ARGV[1] then return redis.call("DEL", KEYS[1]) else return 0 end`)
	_ = script.Run(ctx, s.R, []string{"lock:" + name}, token).Err()
}
