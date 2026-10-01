package sent

import (
	"testing"
	"time"
)

func TestSmokeFastFrame(t *testing.T) {
	tick := 3 * time.Microsecond
	sensor := NewSensor(42, SensorConfig{Variant: VariantA, Tick: tick})
	spec := ChannelSpec{
		Name:  "ch0",
		Frame: DefaultFrameConfig(tick, 0.15, VariantA),
	}
	p := newPipeline(spec)

	status := uint8(0x0)
	data := [6]uint8{1, 2, 3, 4, 5, 6}
	samples := sensor.Emit(1, 0, status, data, Inject{})
	var events []Event
	for _, s := range samples {
		events = append(events, p.processSample(s)...)
	}

	var readings int
	for _, ev := range events {
		if ev.Kind == EvtReading {
			readings++
			if ev.Reading.Fast != FastValue(data) {
				t.Fatalf("fast value = %06x, want %06x", ev.Reading.Fast, FastValue(data))
			}
		}
		if ev.Kind == EvtFrameError {
			t.Fatalf("unexpected frame error: %v", ev.FrameErr.Detail)
		}
	}
	if readings != 1 {
		t.Fatalf("readings = %d, want 1 (events: %v)", readings, events)
	}
}
