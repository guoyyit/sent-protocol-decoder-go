package sent

import (
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestRemoveChannelReclaimsGoroutines(t *testing.T) {
	tick := 3 * time.Microsecond
	r := NewReceiver(4)

	for _, name := range []string{"x", "y", "z"} {
		if err := r.AddChannel(testSpec(name, VariantA, tick)); err != nil {
			t.Fatal(err)
		}
	}

	// Generate some activity, then remove every channel without draining.
	sensor := NewSensor(5, SensorConfig{Variant: VariantA, Tick: tick})
	for seq := 1; seq <= 40; seq++ {
		samples := sensor.Emit(seq, 0, 0, dataNibbles(0x123456), Inject{})
		for _, name := range []string{"x", "y", "z"} {
			for _, s := range samples {
				_ = r.Feed(name, s)
			}
		}
	}

	for _, name := range []string{"x", "y", "z"} {
		if err := r.RemoveChannel(name); err != nil {
			t.Fatal(err)
		}
	}

	// Force goroutine teardown to be observable.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		runtime.GC()
		if !leaksReceiverGoroutines(r) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("receiver goroutines (runProcessor/runForwarder) still present after removal")
}

func leaksReceiverGoroutines(r *Receiver) bool {
	buf := make([]byte, 1<<20)
	n := runtime.Stack(buf, true)
	stacks := string(buf[:n])
	return strings.Contains(stacks, "runProcessor") || strings.Contains(stacks, "runForwarder")
}

func TestEndToEndSlowLoss(t *testing.T) {
	pairs := EncodeSlowShort(0x4, 0xE1)

	// Feed the assembler directly simulating a frame 3 gap at pipeline level
	// by skipping one sequence while retaining ordering.
	a := NewSlowAssembler(VariantB, SlowShort)
	var drops []SlowDropReason
	for seq := 1; seq <= 12; seq++ {
		if seq == 3 {
			continue // frame 3 lost
		}
		_, d := a.Observe(seq, pairs[(seq-1)%6], time.Duration(seq)*time.Millisecond)
		if d != nil {
			drops = append(drops, d.Reason)
		}
	}

	foundLoss := false
	for _, reason := range drops {
		if reason == SlowDropLoss {
			foundLoss = true
		}
	}
	if !foundLoss {
		t.Fatalf("expected a loss drop, got %v", drops)
	}
}
