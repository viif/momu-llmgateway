package cache

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/stretchr/testify/require"
)

func newTestRedis(t *testing.T) (*RedisStore, *miniredis.Miniredis) {
	t.Helper()
	mr := miniredis.RunT(t)
	store, err := NewRedisStore(mr.Addr(), "", 0)
	require.NoError(t, err)
	return store, mr
}

func TestRedisStoreSaveAndLoad(t *testing.T) {
	store, _ := newTestRedis(t)
	defer func() { _ = store.Close() }()

	v := []float64{0.1, 0.2, 0.3}
	resp := []byte(`{"id":"test"}`)
	require.NoError(t, store.Save(context.Background(), "gpt-4o", "key1", v, resp, time.Hour))

	entries, err := store.LoadAll(context.Background(), "gpt-4o")
	require.NoError(t, err)
	require.Len(t, entries, 1)
	require.Equal(t, "gpt-4o", entries[0].Model)
	require.Equal(t, "key1", entries[0].Key)
	require.Equal(t, resp, entries[0].ResponseJSON)
	require.InDeltaSlice(t, v, entries[0].Vector, 0.0001)
}

func TestRedisStoreLoadAllEmpty(t *testing.T) {
	store, _ := newTestRedis(t)
	defer func() { _ = store.Close() }()
	entries, err := store.LoadAll(context.Background(), "nonexistent")
	require.NoError(t, err)
	require.Empty(t, entries)
}

func TestRedisStoreTTLExpiry(t *testing.T) {
	store, mr := newTestRedis(t)
	defer func() { _ = store.Close() }()

	require.NoError(t, store.Save(context.Background(), "gpt-4o", "key1", []float64{1}, []byte("v"), 100*time.Millisecond))
	mr.FastForward(200 * time.Millisecond)

	entries, err := store.LoadAll(context.Background(), "gpt-4o")
	require.NoError(t, err)
	require.Empty(t, entries)
}

func TestRedisStoreMultipleEntries(t *testing.T) {
	store, _ := newTestRedis(t)
	defer func() { _ = store.Close() }()

	require.NoError(t, store.Save(context.Background(), "gpt-4o", "k1", []float64{1}, []byte("a"), time.Hour))
	require.NoError(t, store.Save(context.Background(), "gpt-4o", "k2", []float64{2}, []byte("b"), time.Hour))

	entries, err := store.LoadAll(context.Background(), "gpt-4o")
	require.NoError(t, err)
	require.Len(t, entries, 2)
}
