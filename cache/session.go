package cache

import (
	"context"
	"encoding/json"
	"time"

	"github.com/redis/go-redis/v9"
)

// SessionStore keeps sessions in Redis, so they survive restarts and are shared
// by every instance of your application. It implements session.Store.
//
// Values are stored as JSON, so numbers read back as float64; session.Int
// handles that. Store only JSON-friendly values (strings, numbers, bools, maps, slices).
type SessionStore struct {
	client *redis.Client
	prefix string
}

// NewSessionStore creates a store that keys sessions as prefix+id (default prefix "sess:").
func NewSessionStore(p *Plugin, prefix string) *SessionStore {
	if prefix == "" {
		prefix = "sess:"
	}
	return &SessionStore{client: p.Client, prefix: prefix}
}

// Load implements session.Store.
func (s *SessionStore) Load(ctx context.Context, id string) (map[string]any, bool, error) {
	raw, err := s.client.Get(ctx, s.prefix+id).Bytes()
	if err == redis.Nil {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	var data map[string]any
	if err := json.Unmarshal(raw, &data); err != nil {
		return nil, false, nil // a corrupt entry counts as no session
	}
	return data, true, nil
}

// Save implements session.Store.
func (s *SessionStore) Save(ctx context.Context, id string, data map[string]any, ttl time.Duration) error {
	raw, err := json.Marshal(data)
	if err != nil {
		return err
	}
	return s.client.Set(ctx, s.prefix+id, raw, ttl).Err()
}

// Delete implements session.Store.
func (s *SessionStore) Delete(ctx context.Context, id string) error {
	return s.client.Del(ctx, s.prefix+id).Err()
}
