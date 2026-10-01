package sent

import (
	"sync"
	"testing"
	"time"
)

func TestReceiverConcurrentChannels(t *testing.T) {
	tick := 3 * time.Microsecond
	r := NewReceiver(2048)
	defer r.Close()

	if err := r.AddChannel(testSpec("a", VariantA, tick)); err != nil {
		t.Fatal(err)
	}
	if err := r.AddChannel(testSpec("b", VariantB, tick)); err != nil {
		t.Fatal(err)
	}

	feed := func(name string, variant Variant) {
		sensor := NewSensor(11, SensorConfig{Variant: variant, Tick: tick})
		data := dataNibbles(0xA0A0A0)
		var start time.Duration
		for seq := 1; seq <= 20; seq++ {
			samples := sensor.Emit(seq, start, 0, data, Inject{})
			for _, s := range samples {
				if err := r.Feed(name, s); err != nil {
					t.Errorf("feed %s: %v", name, err)
					return
				}
			}
			start = samples[len(samples)-1].At
		}
	}

	counts := map[string]int{}
	var collectWg sync.WaitGroup
	collectWg.Add(1)
	collectDone := make(chan struct{})
	var collectOnce sync.Once
	go func() {
		defer collectWg.Done()
		for {
			select {
			case ev := <-r.Events():
				if ev.Kind == EvtReading {
					counts[ev.Channel]++
					if counts["a"] >= 20 && counts["b"] >= 20 {
						collectOnce.Do(func() { close(collectDone) })
						return
					}
				}
			case <-collectDone:
				return
			case <-time.After(2 * time.Second):
				t.Errorf("collector timeout, counts=%v", counts)
				collectOnce.Do(func() { close(collectDone) })
				return
			}
		}
	}()

	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); feed("a", VariantA) }()
	go func() { defer wg.Done(); feed("b", VariantB) }()
	wg.Wait()

	deadline := time.After(2 * time.Second)
	select {
	case <-collectDone:
	case <-deadline:
		t.Fatalf("timeout, counts=%v", counts)
	}
}

func TestReceiverDynamicAddRemove(t *testing.T) {
	tick := 3 * time.Microsecond
	r := NewReceiver(8)

	if err := r.AddChannel(testSpec("a", VariantA, tick)); err != nil {
		t.Fatal(err)
	}
	if err := r.AddChannel(testSpec("a", VariantA, tick)); err != ErrChannelExists {
		t.Fatalf("duplicate add = %v, want ErrChannelExists", err)
	}
	if err := r.RemoveChannel("missing"); err != ErrChannelNotFound {
		t.Fatalf("remove unknown = %v, want ErrChannelNotFound", err)
	}
	if err := r.RemoveChannel("a"); err != nil {
		t.Fatal(err)
	}

	sample := Sample{At: 0, Edge: EdgeFalling, FrameSeq: 0}
	if err := r.Feed("a", sample); err != ErrChannelNotFound {
		t.Fatalf("feed removed channel = %v, want ErrChannelNotFound", err)
	}

	// Re-adding the same name after removal works.
	if err := r.AddChannel(testSpec("a", VariantA, tick)); err != nil {
		t.Fatalf("re-add after remove: %v", err)
	}

	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestReceiverSlowConsumerDoesNotBlockOthers(t *testing.T) {
	tick := 3 * time.Microsecond
	r := NewReceiver(4)

	slow := testSpec("slow", VariantA, tick)
	fast := testSpec("fast", VariantA, tick)
	if err := r.AddChannel(slow); err != nil {
		t.Fatal(err)
	}
	if err := r.AddChannel(fast); err != nil {
		t.Fatal(err)
	}

	// Deliberately do not read from r.Events(): queues fill up. The fast
	// channel must still accept some feeds or explicitly report
	// ErrSlowConsumer; it must never deadlock waiting on the slow channel.
	sensor := NewSensor(13, SensorConfig{Variant: VariantA, Tick: tick})
	data := dataNibbles(0x112233)
	var start time.Duration
	done := make(chan struct{})
	go func() {
		defer close(done)
		for seq := 1; seq <= 200; seq++ {
			samples := sensor.Emit(seq, start, 0, data, Inject{})
			for _, s := range samples {
				_ = r.Feed("fast", s) // allowed to report ErrSlowConsumer
			}
			start = samples[len(samples)-1].At
		}
	}()

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("fast channel blocked while output was unconsumed")
	}

	// Remove the congested slow channel; it must be fully reclaimed without
	// hanging even though events were never drained.
	removed := make(chan error, 1)
	go func() { removed <- r.RemoveChannel("slow") }()
	select {
	case err := <-removed:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("RemoveChannel hung on a congested channel")
	}

	r.Close()
}

func TestReceiverCloseClosesEvents(t *testing.T) {
	tick := 3 * time.Microsecond
	r := NewReceiver(8)
	if err := r.AddChannel(testSpec("a", VariantA, tick)); err != nil {
		t.Fatal(err)
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	if err := r.Close(); err != nil {
		t.Fatalf("Close must be idempotent, got %v", err)
	}
	if _, ok := <-r.Events(); ok {
		t.Fatal("Events channel must be closed after Close")
	}
	if err := r.AddChannel(testSpec("b", VariantA, tick)); err == nil {
		t.Fatal("AddChannel after Close must fail")
	}
}
