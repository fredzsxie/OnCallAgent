package mem_test

import (
	"context"
	"errors"
	"fmt"
	"github.com/cloudwego/eino/schema"
	"github.com/fredzsxie/OnCallAgent/backend/internal/config"
	"github.com/fredzsxie/OnCallAgent/backend/utility/mem"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func acquire(t *testing.T, s *mem.Store, id string) *mem.Turn {
	t.Helper()
	turn, err := s.Acquire(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return turn
}
func store(t *testing.T, now func() time.Time) *mem.Store {
	t.Helper()
	s, err := mem.NewStore(config.ChatMemory{MaxRounds: 3, TTL: 30 * time.Minute}, now)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestCompleteRoundsIsolationAndCopies(t *testing.T) {
	s := store(t, nil)
	for i := 1; i <= 4; i++ {
		turn := acquire(t, s, "a")
		turn.Commit(fmt.Sprint("q", i), fmt.Sprint("a", i))
		turn.Close()
		turn.Close()
	}
	turn := acquire(t, s, "a")
	history := turn.History()
	if len(history) != 6 || history[0].Content != "q2" || history[5].Content != "a4" {
		t.Fatalf("wrong window: %v", history)
	}
	for i, msg := range history {
		want := schema.User
		if i%2 == 1 {
			want = schema.Assistant
		}
		if msg.Role != want {
			t.Fatal("incomplete role pair")
		}
	}
	history[0].Content = "changed"
	history[1] = nil
	if turn.History()[0].Content != "q2" || turn.History()[1] == nil {
		t.Fatal("history exposes internal messages")
	}
	turn.Close()
	other := acquire(t, s, "A")
	defer other.Close()
	if len(other.History()) != 0 {
		t.Fatal("Ids are not isolated")
	}
}

func TestExpirationAndReadsDoNotRenew(t *testing.T) {
	var clock atomic.Int64
	clock.Store(time.Now().UnixNano())
	now := func() time.Time { return time.Unix(0, clock.Load()) }
	s := store(t, now)
	turn := acquire(t, s, "a")
	turn.Commit("q", "a")
	turn.Close()
	clock.Add(int64(29 * time.Minute))
	turn = acquire(t, s, "a")
	if len(turn.History()) != 2 {
		t.Fatal("expired early")
	}
	turn.Close()
	clock.Add(int64(time.Minute))
	turn = acquire(t, s, "a")
	if len(turn.History()) != 0 {
		t.Fatal("read/failed turn renewed expiration")
	}
	turn.Commit("new", "answer")
	turn.Close()
	clock.Add(int64(29 * time.Minute))
	turn = acquire(t, s, "a")
	defer turn.Close()
	if len(turn.History()) != 2 {
		t.Fatal("successful turn did not renew expiration")
	}
}

func TestActiveExpiredEntryKeepsItsLock(t *testing.T) {
	var clock atomic.Int64
	clock.Store(time.Now().UnixNano())
	s := store(t, func() time.Time { return time.Unix(0, clock.Load()) })
	first := acquire(t, s, "same")
	clock.Add(int64(time.Hour))
	other := acquire(t, s, "other")
	other.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if turn, err := s.Acquire(ctx, "same"); !errors.Is(err, context.DeadlineExceeded) {
		if turn != nil {
			turn.Close()
		}
		t.Fatal("expiration replaced an active lock")
	}
	first.Commit("q", "a")
	first.Close()
	next := acquire(t, s, "same")
	defer next.Close()
	if len(next.History()) != 2 {
		t.Fatal("waiting cancellation removed active history")
	}
}

func TestConcurrentTurnsAreAtomic(t *testing.T) {
	s := store(t, nil)
	var wg sync.WaitGroup
	for i := 0; i < 30; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			turn, err := s.Acquire(context.Background(), "same")
			if err != nil {
				t.Error(err)
				return
			}
			defer turn.Close()
			text := fmt.Sprint(i)
			turn.Commit(text, text)
		}(i)
	}
	wg.Wait()
	turn := acquire(t, s, "same")
	defer turn.Close()
	h := turn.History()
	if len(h) != 6 {
		t.Fatal("wrong history length")
	}
	for i := 0; i < len(h); i += 2 {
		if h[i].Content != h[i+1].Content {
			t.Fatal("interleaved turns")
		}
	}
}
