package mem

import (
	"context"
	"sync"
	"time"

	"github.com/cloudwego/eino/schema"
	"github.com/fredzsxie/OnCallAgent/backend/internal/config"
)

type Store struct {
	mu       sync.Mutex
	sessions map[string]*entry
	cfg      config.ChatMemory
	now      func() time.Time
}

type entry struct {
	gate    chan struct{}
	users   int
	history []*schema.Message
	expires time.Time
}

// Turn owns the session gate until Close; its history and commit methods are used under that lease.
type Turn struct {
	store *Store
	entry *entry
	once  sync.Once
}

func NewStore(cfg config.ChatMemory, now func() time.Time) (*Store, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	if now == nil {
		now = time.Now
	}
	return &Store{sessions: make(map[string]*entry), cfg: cfg, now: now}, nil
}

func (s *Store) Acquire(ctx context.Context, id string) (*Turn, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	now := s.now()
	// Pinned entries include waiters, so expiration cannot create a second lock for an active Id.
	for key, e := range s.sessions {
		if e.users == 0 && !now.Before(e.expires) {
			delete(s.sessions, key)
		}
	}
	e := s.sessions[id]
	if e == nil {
		e = &entry{gate: make(chan struct{}, 1), expires: now.Add(s.cfg.TTL)}
		e.gate <- struct{}{}
		s.sessions[id] = e
	}
	e.users++
	s.mu.Unlock()
	select {
	case <-ctx.Done():
		s.mu.Lock()
		e.users--
		s.mu.Unlock()
		return nil, ctx.Err()
	case <-e.gate:
	}
	t := &Turn{store: s, entry: e}
	if err := ctx.Err(); err != nil {
		t.Close()
		return nil, err
	}
	if !s.now().Before(e.expires) {
		e.history = nil
	}
	return t, nil
}

func (t *Turn) History() []*schema.Message {
	result := make([]*schema.Message, 0, len(t.entry.history))
	for _, msg := range t.entry.history {
		copy := *msg
		result = append(result, &copy)
	}
	return result
}

func (t *Turn) Commit(question, answer string) {
	e := t.entry
	e.history = append(e.history, schema.UserMessage(question), schema.AssistantMessage(answer, nil))
	// Trim complete pairs; failed requests never append either half of a turn.
	if rounds := len(e.history) / 2; rounds > t.store.cfg.MaxRounds {
		e.history = e.history[(rounds-t.store.cfg.MaxRounds)*2:]
	}
	e.expires = t.store.now().Add(t.store.cfg.TTL)
}

func (t *Turn) Close() {
	t.once.Do(func() {
		t.store.mu.Lock()
		defer t.store.mu.Unlock()
		t.entry.users--
		t.entry.gate <- struct{}{}
	})
}
