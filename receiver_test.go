package sent

import (
	"sync"
	"testing"
	"time"
)

func drain(t *testing.T, out <-chan Event, done chan struct{}) []Event {
	t.Helper()
	var got []Event
	for {
		select {
		case ev := <-out:
			got = append(got, ev)
		case <-done:
			return got
		case <-time.After(2 * time.Second):
			t.Fatal("timeout draining events")
			return got
		}
	}
}

func TestConcurrentChannelsIsolation(t *testing.T) {
	out := make(chan Event, 256)
	mgr := NewManager(out)

	cfgs := []ChannelConfig{
		DefaultConfig("a", VariantRecommended),
		DefaultConfig("b", VariantLegacy),
		DefaultConfig("c", VariantExtended),
	}
	for _, c := range cfgs {
		if err := mgr.Add(c); err != nil {
			t.Fatal(err)
		}
	}

	var wg sync.WaitGroup
	for idx, c := range cfgs {
		wg.Add(1)
		go func(i int, cfg ChannelConfig) {
			defer wg.Done()
			specs := cleanSpecs(6)
			if i == 1 {
				// Channel b injects corrupt frames; a/c must be unaffected.
				specs = specs[:6]
			}
			edges := NewEncoder(cfg, int64(i+1)).Build(specs, nil)
			_ = mgr.Push(cfg.ID, edges)
		}(idx, c)
	}
	wg.Wait()

	seen := map[string]int{}
	deadline := time.After(500 * time.Millisecond)
loop:
	for {
		select {
		case ev := <-out:
			seen[ev.ChannelID]++
		case <-deadline:
			break loop
		}
	}

	// Each channel delivers independently even while others carry faults.
	for _, c := range cfgs {
		if seen[c.ID] < 6 {
			t.Fatalf("channel %s delivered %d events, want >= 6", c.ID, seen[c.ID])
		}
	}
	if err := mgr.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestAddRemoveReclaimsState(t *testing.T) {
	out := make(chan Event, 16)
	mgr := NewManager(out)

	cfg := DefaultConfig("temp", VariantRecommended)
	if err := mgr.Add(cfg); err != nil {
		t.Fatal(err)
	}
	if err := mgr.Add(cfg); err == nil {
		t.Fatal("duplicate Add must fail")
	}

	edges := NewEncoder(cfg, 1).Build(cleanSpecs(3), nil)
	if err := mgr.Push("temp", edges); err != nil {
		t.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond)

	if err := mgr.Remove("temp"); err != nil {
		t.Fatal(err)
	}
	if err := mgr.Remove("temp"); err == nil {
		t.Fatal("double Remove must fail")
	}
	if _, ok := mgr.Stats()["temp"]; ok {
		t.Fatal("removed channel state must be fully reclaimed")
	}
	if err := mgr.Push("temp", edges); err == nil {
		t.Fatal("push to removed channel must fail")
	}
	if err := mgr.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestSlowConsumerDoesNotBlockOthers(t *testing.T) {
	// Tiny output channel: nobody reads it. Receivers must keep functioning
	// (overflow counted) instead of blocking other channels.
	out := make(chan Event, 1)
	mgr := NewManager(out)
	defer mgr.Close()

	c1 := DefaultConfig("fast1", VariantRecommended)
	c2 := DefaultConfig("fast2", VariantRecommended)
	if err := mgr.Add(c1); err != nil {
		t.Fatal(err)
	}
	if err := mgr.Add(c2); err != nil {
		t.Fatal(err)
	}

	edges := NewEncoder(c1, 1).Build(cleanSpecs(4), nil)
	_ = mgr.Push("fast1", edges)
	_ = mgr.Push("fast2", edges)
	time.Sleep(200 * time.Millisecond)

	stats := mgr.Stats()
	if stats["fast1"].Frames+stats["fast2"].Frames != 8 {
		t.Fatalf("both channels should decode all 8 frames, stats=%v", stats)
	}
	if stats["fast1"].Overflow+stats["fast2"].Overflow == 0 {
		t.Fatal("unread output should accumulate overflow counters")
	}
}
