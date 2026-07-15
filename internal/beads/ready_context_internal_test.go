package beads

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gastownhall/gascity/internal/testutil"
)

type observedErrContext struct {
	context.Context
	once    sync.Once
	checked chan struct{}
}

type cancelAfterChecksContext struct {
	context.Context
	cancelAt int64
	checks   atomic.Int64
}

func (c *cancelAfterChecksContext) Err() error {
	if c.checks.Add(1) >= c.cancelAt {
		return context.Canceled
	}
	return nil
}

func TestCachingStoreCountContextCancelsWhileWaitingForLock(t *testing.T) {
	store := NewCachingStoreForTest(NewMemStore(), nil)
	store.mu.Lock()
	locked := true
	defer func() {
		if locked {
			store.mu.Unlock()
		}
	}()

	base, cancel := context.WithCancel(context.Background())
	ctx := &observedErrContext{Context: base, checked: make(chan struct{})}
	done := make(chan error, 1)
	go func() {
		_, err := store.Count(ctx, ListQuery{Status: "open"})
		done <- err
	}()

	select {
	case <-ctx.checked:
	case <-time.After(testutil.GoroutineRaceTimeout):
		store.mu.Unlock()
		locked = false
		select {
		case <-done:
		case <-time.After(testutil.GoroutineRaceTimeout):
		}
		t.Fatal("Count did not check context before waiting for the cache lock")
	}
	cancel()

	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Count error = %v, want context.Canceled", err)
		}
	case <-time.After(testutil.GoroutineRaceTimeout):
		store.mu.Unlock()
		locked = false
		select {
		case <-done:
		case <-time.After(testutil.GoroutineRaceTimeout):
		}
		t.Fatal("Count waited for the cache lock after context cancellation")
	}
}

func TestSortBeadsReadyOrderContextStopsAfterCancellation(t *testing.T) {
	rows := make([]Bead, 128)
	for i := range rows {
		priority := len(rows) - i
		rows[i] = Bead{ID: fmt.Sprintf("gc-%03d", i), Priority: &priority}
	}
	ctx := &cancelAfterChecksContext{Context: context.Background(), cancelAt: 8}

	err := sortBeadsReadyOrderContext(ctx, rows)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("sortBeadsReadyOrderContext error = %v, want context.Canceled", err)
	}
}

func (c *observedErrContext) Err() error {
	err := c.Context.Err()
	c.once.Do(func() { close(c.checked) })
	return err
}

func TestMemStoreReadyContextCancelsWhileWaitingForLock(t *testing.T) {
	store := NewMemStore()
	store.mu.Lock()
	locked := true
	defer func() {
		if locked {
			store.mu.Unlock()
		}
	}()

	base, cancel := context.WithCancel(context.Background())
	ctx := &observedErrContext{Context: base, checked: make(chan struct{})}
	done := make(chan error, 1)
	go func() {
		_, err := store.ReadyContext(ctx)
		done <- err
	}()
	select {
	case <-ctx.checked: // the first pre-lock context check observed an active context
	case <-time.After(testutil.GoroutineRaceTimeout):
		store.mu.Unlock()
		locked = false
		select {
		case <-done:
		case <-time.After(testutil.GoroutineRaceTimeout):
		}
		t.Fatal("ReadyContext did not check context before waiting for the lock")
	}
	cancel()

	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("ReadyContext error = %v, want context.Canceled", err)
		}
	case <-time.After(testutil.GoroutineRaceTimeout):
		store.mu.Unlock()
		locked = false
		select {
		case <-done:
		case <-time.After(testutil.GoroutineRaceTimeout):
		}
		t.Fatal("ReadyContext waited for the lock after context cancellation")
	}
}

func TestFileStoreReadyContextReportsUnsupported(t *testing.T) {
	store := &FileStore{MemStore: NewMemStore()}
	rows, err := store.ReadyContext(context.Background())
	if !errors.Is(err, ErrReadyContextUnsupported) {
		t.Fatalf("ReadyContext error = %v, want ErrReadyContextUnsupported", err)
	}
	if len(rows) != 0 {
		t.Fatalf("ReadyContext rows = %+v, want none for context-blind file refresh", rows)
	}
}
