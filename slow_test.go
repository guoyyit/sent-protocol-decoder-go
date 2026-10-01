package sent

import (
	"testing"
	"time"
)

func TestSlowShortAssembly(t *testing.T) {
	tick := 3 * time.Microsecond
	pairs := EncodeSlowShort(0xA, 0x5C)

	frames := make([]emittedFrame, 6)
	for i := range frames {
		frames[i] = emittedFrame{
			status: pairs[i], // slow bits in low two status bits
			data:   dataNibbles(uint32(0x100000 + i)),
		}
	}

	events := runSamples(testSpec("b", VariantB, tick),
		emitFrames(1, VariantB, tick, frames))

	var msgs []*SlowMessage
	for _, ev := range events {
		if ev.Kind == EvtSlowMessage {
			msgs = append(msgs, ev.Slow)
		}
	}
	if len(msgs) != 1 {
		t.Fatalf("expected 1 slow message, got %d (events=%d)", len(msgs), len(events))
	}
	if msgs[0].ID != 0xA || msgs[0].Data != 0x5C {
		t.Fatalf("message = id:%x data:%x, want a:5c", msgs[0].ID, msgs[0].Data)
	}
}

func TestSlowEnhancedAssembly(t *testing.T) {
	tick := 3 * time.Microsecond
	pairs := EncodeSlowEnhanced(VariantA, 0x7, 0xBEEF)

	frames := make([]emittedFrame, 16)
	for i := range frames {
		frames[i] = emittedFrame{
			status: pairs[i],
			data:   dataNibbles(uint32(0x200000 + i)),
		}
	}

	events := runSamples(testSpec("a", VariantA, tick),
		emitFrames(1, VariantA, tick, frames))

	var msgs []*SlowMessage
	for _, ev := range events {
		if ev.Kind == EvtSlowMessage {
			msgs = append(msgs, ev.Slow)
		}
	}
	if len(msgs) != 1 {
		t.Fatalf("expected 1 enhanced message, got %d", len(msgs))
	}
	if msgs[0].ID != 0x7 || msgs[0].Data != 0xBEEF {
		t.Fatalf("message = id:%x data:%x, want 7:beef", msgs[0].ID, msgs[0].Data)
	}
}

func TestSlowDropReasons(t *testing.T) {
	t.Run("loss", func(t *testing.T) {
		// Feed pairs 1..6 but skip sequence 4.
		pairs := EncodeSlowShort(0x3, 0x99)
		a := NewSlowAssembler(VariantB, SlowShort)
		var drops []*SlowDrop
		for seq := 1; seq <= 6; seq++ {
			if seq == 4 {
				continue
			}
			_, d := a.Observe(seq, pairs[seq-1], 0)
			if d != nil {
				drops = append(drops, d)
			}
		}
		found := false
		for _, d := range drops {
			if d.Reason == SlowDropLoss {
				found = true
			}
		}
		if !found {
			t.Fatalf("expected SlowDropLoss, got %v", drops)
		}
	})

	t.Run("duplicate", func(t *testing.T) {
		a := NewSlowAssembler(VariantB, SlowShort)
		pairs := EncodeSlowShort(0x1, 0x11)
		a.Observe(1, pairs[0], 0)
		_, d1 := a.Observe(2, pairs[1], 0)
		if d1 != nil {
			t.Fatalf("unexpected early drop: %v", d1)
		}
		_, dup := a.Observe(2, pairs[1], 0)
		if dup == nil || dup.Reason != SlowDropDuplicate {
			t.Fatalf("expected duplicate drop, got %v", dup)
		}
	})

	t.Run("out of order", func(t *testing.T) {
		a := NewSlowAssembler(VariantB, SlowShort)
		pairs := EncodeSlowShort(0x2, 0x22)
		a.Observe(1, pairs[0], 0)
		a.Observe(2, pairs[1], 0)
		a.Observe(3, pairs[2], 0)
		_, d := a.Observe(2, pairs[1], 0)
		if d == nil || d.Reason != SlowDropOutOfOrder {
			t.Fatalf("expected out-of-order drop, got %v", d)
		}
	})
}
