package sent

import "testing"

func testConfig() ChannelConfig {
	return DefaultConfig("s1", VariantRecommended)
}

func TestSmokeDecode(t *testing.T) {
	cfg := testConfig()
	specs := make([]FrameSpec, 20)
	for i := range specs {
		specs[i] = FrameSpec{
			SlowBits: uint8(i & 0x03),
			RawA:     1000,
			RawB:     500,
		}
	}
	edges := NewEncoder(cfg, 42).Build(specs, nil)
	events := Decode(cfg, edges)

	readings := 0
	for _, ev := range events {
		if ev.Kind == EventReading {
			readings++
		}
		if ev.Kind == EventFrameError {
			t.Fatalf("unexpected frame error: %+v width=%.2f", ev.FrameErr.Class, ev.FrameErr.WidthTicks)
		}
	}
	if readings != 20 {
		t.Fatalf("readings = %d, want 20", readings)
	}
	r := events[0].Reading
	if r.RawA != 1000 || r.RawB != 500 {
		t.Fatalf("raw mismatch: %+v", r)
	}
	if r.PhysA < 48.8 || r.PhysA > 48.9 {
		t.Fatalf("physA = %v", r.PhysA)
	}
}
