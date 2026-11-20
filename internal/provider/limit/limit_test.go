package limit

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/IvanLobanok/evnavigation/internal/models/route"
)

type probe struct {
	mu            sync.Mutex
	running, peak int
	calls         int
	hold          time.Duration
	err           error
	panicNow      bool
}

func (p *probe) BuildRoute(_ context.Context, _, _ route.RoutePoint) ([]route.RouteSegment, error) {
	p.mu.Lock()
	p.calls++
	p.running++
	p.peak = max(p.peak, p.running)
	hold, err, boom := p.hold, p.err, p.panicNow
	p.mu.Unlock()

	defer func() {
		p.mu.Lock()
		p.running--
		p.mu.Unlock()
	}()
	if boom {
		panic("provider bug")
	}
	time.Sleep(hold)
	return []route.RouteSegment{{DistanceMeters: 1}}, err
}

func TestNew_NonPositiveMeansNoLimit(t *testing.T) {
	base := &probe{}
	for _, n := range []int{0, -1} {
		if got := New(base, n); got != route.RouteProvider(base) {
			t.Errorf("max=%d: the provider must be returned as is", n)
		}
	}
}

func TestProvider_LimitsConcurrency(t *testing.T) {
	base := &probe{hold: 15 * time.Millisecond}
	p := New(base, 3)

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := p.BuildRoute(context.Background(), route.RoutePoint{}, route.RoutePoint{}); err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		}()
	}
	wg.Wait()

	if base.calls != 20 {
		t.Errorf("every call must eventually reach the provider: %d of 20", base.calls)
	}
	if base.peak > 3 {
		t.Errorf("up to %d calls ran at once, limit is 3", base.peak)
	}
	if base.peak < 3 {
		t.Errorf("the slots must be fully usable: peak was %d", base.peak)
	}
}

func TestProvider_WaitingCallGivesUpWithContext(t *testing.T) {
	base := &probe{hold: 300 * time.Millisecond}
	p := New(base, 1)

	started := make(chan struct{})
	go func() {
		close(started)
		_, _ = p.BuildRoute(context.Background(), route.RoutePoint{}, route.RoutePoint{})
	}()
	<-started
	time.Sleep(30 * time.Millisecond) // первый вызов занял единственный слот

	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := p.BuildRoute(ctx, route.RoutePoint{}, route.RoutePoint{})

	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("got %v, want context.DeadlineExceeded", err)
	}
	if time.Since(start) > 200*time.Millisecond {
		t.Error("a waiting call must give up when its context expires, not wait for the slot")
	}
	base.mu.Lock()
	calls := base.calls
	base.mu.Unlock()
	if calls != 1 {
		t.Errorf("the abandoned call must not reach the provider, provider calls: %d", calls)
	}
}

func TestProvider_PassesResultAndErrorThrough(t *testing.T) {
	boom := errors.New("boom")
	p := New(&probe{err: boom}, 2)

	segs, err := p.BuildRoute(context.Background(), route.RoutePoint{}, route.RoutePoint{})
	if !errors.Is(err, boom) {
		t.Errorf("got %v, want the provider's error", err)
	}
	if len(segs) != 1 {
		t.Errorf("segments must be passed through, got %d", len(segs))
	}
}

func TestProvider_SlotIsReleasedAfterPanic(t *testing.T) {
	base := &probe{panicNow: true}
	p := New(base, 1)

	func() {
		defer func() { _ = recover() }()
		_, _ = p.BuildRoute(context.Background(), route.RoutePoint{}, route.RoutePoint{})
	}()

	base.mu.Lock()
	base.panicNow = false
	base.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err := p.BuildRoute(ctx, route.RoutePoint{}, route.RoutePoint{}); err != nil {
		t.Errorf("the slot leaked after a panic: %v", err)
	}
}
