package sent

import "testing"

func TestPausePulseDecodes(t *testing.T) {
	cfg := testConfig()
	cfg.PauseTicks = 20
	specs := cleanSpecs(6)
	edges := NewEncoder(cfg, 4).Build(specs, nil)
	events := Decode(cfg, edges)
	if got := countReadings(events); got != 6 {
		t.Fatalf("pause-pulse frames decoded = %d, want 6", got)
	}
	if firstError(events) != nil {
		t.Fatalf("pause pulse must not be flagged: %+v", firstError(events))
	}
}

func TestConsecutiveSlowMessages(t *testing.T) {
	m1 := encodeSlowBits(VariantLegacy, 0, 0xA)
	m2 := encodeSlowBits(VariantLegacy, 1, 0xB)
	words := append(append([]uint8{}, m1...), m2...)

	// Drive via the assembler directly and collect every emitted message.
	a := NewSlowAssembler(VariantLegacy)
	var msgs []*SlowMessage
	for i, w := range words {
		f := &Frame{StartTime: int64(i), Seq: uint8(i & 3), SlowBits: w}
		m, _ := a.push(f)
		if m != nil {
			msgs = append(msgs, m)
		}
	}
	if len(msgs) != 2 {
		t.Fatalf("want 2 messages, got %d", len(msgs))
	}
	if msgs[0].Data != 0xA || msgs[1].Data != 0xB {
		t.Fatalf("data = %#x,%#x", msgs[0].Data, msgs[1].Data)
	}
}

func TestJitterWithinTolerance(t *testing.T) {
	cfg := testConfig()
	cfg.WidthTol = 0.6
	specs := cleanSpecs(10)
	enc := NewEncoder(cfg, 2024).WithJitter(0.4)
	edges := enc.Build(specs, nil)
	if got := countReadings(Decode(cfg, edges)); got != 10 {
		t.Fatalf("jittered-in-tolerance stream decoded %d frames, want 10", got)
	}
}

func TestErrorFlagsPassThrough(t *testing.T) {
	cfg := testConfig()
	specs := []FrameSpec{{SlowBits: 0, RawA: 10, RawB: 20, ErrA: true, ErrB: false}}
	edges := NewEncoder(cfg, 1).Build(specs, nil)
	events := Decode(cfg, edges)
	for _, ev := range events {
		if ev.Kind == EventReading {
			if !ev.Reading.ErrA || ev.Reading.ErrB {
				t.Fatalf("error flags not preserved: %+v", ev.Reading)
			}
			// Error flag lives in bit 11; the 11-bit measurement is intact.
			if ev.Reading.RawA != 10 {
				t.Fatalf("raw measurement corrupted by error flag: %d", ev.Reading.RawA)
			}
		}
	}
}
