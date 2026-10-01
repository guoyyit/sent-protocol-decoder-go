package sent

import (
	"testing"
	"time"
)

func TestFrameErrorClassification(t *testing.T) {
	tick := 3 * time.Microsecond
	tight := DefaultFrameConfig(tick, 0.02, VariantA)

	single := func(inj Inject) []Event {
		spec := testSpec("c", VariantA, tick)
		spec.Frame = tight
		return runSamples(spec,
			emitFrames(7, VariantA, tick, []emittedFrame{makeBad(inj)}))
	}

	checkNoReading := func(t *testing.T, events []Event) {
		t.Helper()
		for _, ev := range events {
			if ev.Kind == EvtReading {
				t.Fatalf("corrupt frame must not produce a reading")
			}
		}
	}

	t.Run("crc", func(t *testing.T) {
		events := single(Inject{CorruptCRC: true})
		if !hasError(events, ErrCRC) {
			t.Fatalf("expected ErrCRC, events: %v", events)
		}
		checkNoReading(t, events)
	})

	t.Run("bad pulse", func(t *testing.T) {
		events := single(Inject{BadPulse: 3, Jitter: 0.20})
		if !hasError(events, ErrPulseOutOfTolerance) {
			t.Fatalf("expected ErrPulseOutOfTolerance, events: %v", events)
		}
		checkNoReading(t, events)
	})

	t.Run("illegal width", func(t *testing.T) {
		events := single(Inject{BadPulse: 4, IllegalWidth: true})
		if !hasError(events, ErrIllegalNibble) {
			t.Fatalf("expected ErrIllegalNibble, events: %v", events)
		}
		checkNoReading(t, events)
	})

	t.Run("missing sync", func(t *testing.T) {
		events := single(Inject{DropSync: true})
		if !hasError(events, ErrSyncMissing) {
			t.Fatalf("expected ErrSyncMissing, events: %v", events)
		}
		checkNoReading(t, events)
	})
}

func hasError(events []Event, kind ErrorKind) bool {
	for _, ev := range events {
		if ev.Kind == EvtFrameError && ev.FrameErr.Kind == kind {
			return true
		}
	}
	return false
}

func makeBad(inj Inject) emittedFrame {
	return emittedFrame{status: 0x0, data: dataNibbles(0x654321), inj: inj}
}
