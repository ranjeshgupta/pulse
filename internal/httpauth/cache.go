package httpauth

import (
	"context"
	"encoding/base64"
	"errors"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

// Cache shares tokens between instances. Keys already carry the workspace and provider.
type Cache interface {
	Get(ctx context.Context, key string) (string, error) // "" when missing
	Set(ctx context.Context, key, val string, ttl time.Duration) error
	// DeleteIf removes key only while it still holds val (a token refused with 401).
	DeleteIf(ctx context.Context, key, val string) error
	// Lock takes a short lock (SET NX); Unlock releases it.
	Lock(ctx context.Context, key string, ttl time.Duration) (bool, error)
	Unlock(ctx context.Context, key string) error
}

// Sealer encrypts cached tokens (auth.Box).
type Sealer interface {
	Seal(plain []byte) []byte
	Open(sealed []byte) ([]byte, error)
}

// RedisCache keeps tokens in Redis, sealed with Box when set (a token is a credential).
type RedisCache struct {
	RDB redis.Cmdable
	Box Sealer
}

func (c RedisCache) Get(ctx context.Context, key string) (string, error) {
	v, err := c.RDB.Get(ctx, key).Result()
	if errors.Is(err, redis.Nil) {
		return "", nil
	} else if err != nil {
		return "", err
	}
	return c.open(v)
}

func (c RedisCache) open(v string) (string, error) {
	if c.Box == nil {
		return v, nil
	}
	b, err := base64.RawStdEncoding.DecodeString(v)
	if err != nil {
		return "", nil
	}
	plain, err := c.Box.Open(b)
	if err != nil {
		return "", nil // sealed with another key: treat as missing
	}
	return string(plain), nil
}

func (c RedisCache) seal(v string) string {
	if c.Box == nil {
		return v
	}
	return base64.RawStdEncoding.EncodeToString(c.Box.Seal([]byte(v)))
}

func (c RedisCache) Set(ctx context.Context, key, val string, ttl time.Duration) error {
	return c.RDB.Set(ctx, key, c.seal(val), ttl).Err()
}

func (c RedisCache) DeleteIf(ctx context.Context, key, val string) error {
	cur, err := c.Get(ctx, key)
	if err != nil || cur != val {
		return err
	}
	// Sealed values differ per write, so compare the stored text itself in the script.
	raw, err := c.RDB.Get(ctx, key).Result()
	if err != nil {
		return nil
	}
	return deleteIf.Run(ctx, c.RDB, []string{key}, raw).Err()
}

var deleteIf = redis.NewScript(`if redis.call('GET', KEYS[1]) == ARGV[1] then return redis.call('DEL', KEYS[1]) end return 0`)

func (c RedisCache) Lock(ctx context.Context, key string, ttl time.Duration) (bool, error) {
	return c.RDB.SetNX(ctx, key, "1", ttl).Result()
}

func (c RedisCache) Unlock(ctx context.Context, key string) error { return c.RDB.Del(ctx, key).Err() }

// MemoryCache is a Cache inside one process (tests, and servers without Redis).
type MemoryCache struct {
	mu    sync.Mutex
	vals  map[string]memEntry
	Now   func() time.Time
	Locks int // how many locks were taken (tests)
}

type memEntry struct {
	v   string
	exp time.Time
}

func (m *MemoryCache) now() time.Time {
	if m.Now != nil {
		return m.Now()
	}
	return time.Now()
}

func (m *MemoryCache) Get(_ context.Context, key string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	e, ok := m.vals[key]
	if !ok || !m.now().Before(e.exp) {
		return "", nil
	}
	return e.v, nil
}

func (m *MemoryCache) Set(_ context.Context, key, val string, ttl time.Duration) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.vals == nil {
		m.vals = map[string]memEntry{}
	}
	m.vals[key] = memEntry{val, m.now().Add(ttl)}
	return nil
}

func (m *MemoryCache) DeleteIf(_ context.Context, key, val string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if e, ok := m.vals[key]; ok && e.v == val {
		delete(m.vals, key)
	}
	return nil
}

func (m *MemoryCache) Lock(_ context.Context, key string, ttl time.Duration) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.vals == nil {
		m.vals = map[string]memEntry{}
	}
	if e, ok := m.vals[key]; ok && m.now().Before(e.exp) {
		return false, nil
	}
	m.vals[key] = memEntry{"1", m.now().Add(ttl)}
	m.Locks++
	return true, nil
}

func (m *MemoryCache) Unlock(_ context.Context, key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.vals, key)
	return nil
}

// Keys lists stored keys (tests).
func (m *MemoryCache) Keys() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []string
	for k := range m.vals {
		out = append(out, k)
	}
	return out
}
