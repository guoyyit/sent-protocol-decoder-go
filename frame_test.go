package sent

import "testing"

func cleanSpecs(n int) []FrameSpec {
	specs := make([]FrameSpec, n)
	for i := range specs {
		specs[i] = FrameSpec{SlowBits: uint8(i & 0x03), RawA: 1000, RawB: 500}
	}
	return specs
}

func firstError(events []Event) *FrameError {
	for _, ev := range events {
		if ev.Kind == EventFrameError {
			return ev.FrameErr
		}
	}
	return nil
}

func countReadings(events []Event) int {
	n := 0
	for _, ev := range events {
		if ev.Kind == EventReading {
			n++
		}
	}
	return n
}

func TestCorruptCRCRejectsFrame(t *testing.T) {
	cfg := testConfig()
	specs := cleanSpecs(6)
	ops := []InjectOp{{Type: OpCorruptCRC, FrameIndex: 2}}
	edges := NewEncoder(cfg, 1).Build(specs, ops)
	events := Decode(cfg, edges)

	ferr := firstError(events)
	if ferr == nil || ferr.Class != FrameErrCRC {
		t.Fatalf("want CRC error, got %+v", ferr)
	}
	if countReadings(events) != 5 {
		t.Fatalf("want 5 valid readings, got %d", countReadings(events))
	}
}

func TestIllegalNibbleRejected(t *testing.T) {
	cfg := testConfig()
	specs := cleanSpecs(4)
	ops := []InjectOp{{Type: OpIllegalNibble, FrameIndex: 1, With: 3}}
	edges := NewEncoder(cfg, 7).Build(specs, ops)
	events := Decode(cfg, edges)

	ferr := firstError(events)
	if ferr == nil || ferr.Class != FrameErrIllegalNibble {
		t.Fatalf("want illegal-nibble error, got %+v", ferr)
	}
}

func TestPulseWidthOutOfTolerance(t *testing.T) {
	cfg := testConfig()
	specs := cleanSpecs(4)
	ops := []InjectOp{{Type: OpPulseWidth, FrameIndex: 1, With: 2}}
	edges := NewEncoder(cfg, 7).Build(specs, ops)
	events := Decode(cfg, edges)

	ferr := firstError(events)
	if ferr == nil || ferr.Class != FrameErrPulseWidth {
		t.Fatalf("want pulse-width error, got %+v", ferr)
	}
}

func TestMissingSyncDetected(t *testing.T) {
	cfg := testConfig()
	specs := cleanSpecs(4)
	ops := []InjectOp{{Type: OpMissingSync, FrameIndex: 1}}
	edges := NewEncoder(cfg, 7).Build(specs, ops)
	events := Decode(cfg, edges)

	var found bool
	for _, ev := range events {
		if ev.Kind == EventFrameError &&
			(ev.FrameErr.Class == FrameErrSyncMissing || ev.FrameErr.Class == FrameErrIllegalNibble) {
			found = true
		}
	}
	if !found {
		t.Fatalf("want sync-missing classification, events=%v", events)
	}
}

func TestNoiseGlitchDetected(t *testing.T) {
	cfg := testConfig()
	specs := cleanSpecs(4)
	ops := []InjectOp{{Type: OpGlitch, FrameIndex: 1, With: 1}}
	edges := NewEncoder(cfg, 7).Build(specs, ops)
	events := Decode(cfg, edges)

	ferr := firstError(events)
	if ferr == nil {
		t.Fatalf("want a frame error for glitch pulse")
	}
	if ferr.Class != FrameErrNoiseGlitch && ferr.Class != FrameErrPulseWidth {
		t.Fatalf("want glitch/pulse-width error, got %v", ferr.Class)
	}
}

func TestPhysicalValuesAndTimestamps(t *testing.T) {
	cfg := testConfig()
	specs := []FrameSpec{{SlowBits: 0, RawA: 0, RawB: 2047}}
	edges := NewEncoder(cfg, 9).Build(specs, nil)
	events := Decode(cfg, edges)

	var r *Reading
	for _, ev := range events {
		if ev.Kind == EventReading {
			r = ev.Reading
		}
	}
	if r == nil {
		t.Fatal("missing reading")
	}
	if r.RawA != 0 || r.RawB != 2047 {
		t.Fatalf("raw mismatch a=%d b=%d", r.RawA, r.RawB)
	}
	if r.PhysA != cfg.ScaleA.Min {
		t.Fatalf("physA min = %v", r.PhysA)
	}
	if r.PhysB < cfg.ScaleB.Max-0.01 {
		t.Fatalf("physB max = %v", r.PhysB)
	}
	if r.Frame.StartTime <= 0 || r.Frame.TickUS <= 0 {
		t.Fatalf("frame timestamp/tick not populated: %+v", r.Frame)
	}
}
