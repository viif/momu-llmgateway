package cache

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

type RedisStore struct {
	client *redis.Client
}

func NewRedisStore(addr, password string, db int) (*RedisStore, error) {
	client := redis.NewClient(&redis.Options{
		Addr:     addr,
		Password: password,
		DB:       db,
	})
	if err := client.Ping(context.Background()).Err(); err != nil {
		return nil, fmt.Errorf("redis connect: %w", err)
	}
	return &RedisStore{client: client}, nil
}

func (r *RedisStore) Save(ctx context.Context, model, key string, vector []float64, respJSON []byte, ttl time.Duration) error {
	vectorData := EncodeVector(vector)
	pipe := r.client.Pipeline()
	pipe.Set(ctx, fmt.Sprintf("sc:v:%s:%s", model, key), vectorData, ttl)
	pipe.Set(ctx, fmt.Sprintf("sc:r:%s:%s", model, key), respJSON, ttl)
	pipe.ZAdd(ctx, fmt.Sprintf("sc:idx:%s", model), redis.Z{
		Score:  float64(time.Now().Unix()),
		Member: key,
	})
	pipe.Expire(ctx, fmt.Sprintf("sc:idx:%s", model), ttl)
	_, err := pipe.Exec(ctx)
	return err
}

func (r *RedisStore) LoadAll(ctx context.Context, model string) ([]CacheEntry, error) {
	keys, err := r.client.ZRange(ctx, fmt.Sprintf("sc:idx:%s", model), 0, -1).Result()
	if err != nil {
		return nil, err
	}
	if len(keys) == 0 {
		return nil, nil
	}

	pipe := r.client.Pipeline()
	for _, key := range keys {
		pipe.Get(ctx, fmt.Sprintf("sc:v:%s:%s", model, key))
		pipe.Get(ctx, fmt.Sprintf("sc:r:%s:%s", model, key))
	}
	cmds, err := pipe.Exec(ctx)
	if err != nil && err != redis.Nil {
		return nil, err
	}

	entries := make([]CacheEntry, 0, len(keys))
	for i, key := range keys {
		vectorCmd := cmds[i*2].(*redis.StringCmd)
		respCmd := cmds[i*2+1].(*redis.StringCmd)

		vectorBytes, err := vectorCmd.Bytes()
		if err != nil {
			continue
		}
		respBytes, err := respCmd.Bytes()
		if err != nil {
			continue
		}
		entries = append(entries, CacheEntry{
			Model:        model,
			Key:          key,
			Vector:       DecodeVector(vectorBytes),
			ResponseJSON: respBytes,
		})
	}
	return entries, nil
}

func (r *RedisStore) Close() error {
	return r.client.Close()
}

func (r *RedisStore) Ping(ctx context.Context) error {
	return r.client.Ping(ctx).Err()
}
