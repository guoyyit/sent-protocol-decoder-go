package sent

import (
	"bytes"
	"testing"
	"time"
)

func buildRecording(t *testing.T) *Recording {
	t.Helper()
	tick := 3 * time.Microsecond

	build := func(seed uint64, variant Variant, n int) []Sample {
		sensor := NewSensor(seed, SensorConfig{Variant: variant, Tick: tick})
		var all []Sample
		var start time.Duration
		for seq := 1; seq <= n; seq++ {
			data := dataNibbles(uint32(0x100000 + seq))
			samples := sensor.Emit(seq, start, 0, data, Inject{})
			all = append(all, samples...)
			start = samples[len(samples)-1].At
		}
		return all
	}

	rec := NewRecording()
	rec.Add(ChannelRecording{Spec: testSpec("a", VariantA, tick), Samples: build(21, VariantA, 8)})
	rec.Add(ChannelRecording{Spec: testSpec("b", VariantB, tick), Samples: build(22, VariantB, 8)})
	return rec
}

func TestReplayDeterministic(t *testing.T) {
	rec := buildRecording(t)

	first := rec.Replay()
	for i := 0; i < 5; i++ {
		got := rec.Replay()
		if !eventsEqual(first, got) {
			t.Fatalf("replay %d differs from first", i+1)
		}
	}
}

func TestReplayRoundTrip(t *testing.T) {
	rec := buildRecording(t)

	var buf bytes.Buffer
	if err := rec.Save(&buf); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadRecording(&buf)
	if err != nil {
		t.Fatal(err)
	}
	if !eventsEqual(rec.Replay(), loaded.Replay()) {
		t.Fatal("replay after gob round-trip differs")
	}
}

func TestReplayContainsBothChannels(t *testing.T) {
	events := buildRecording(t).Replay()
	seen := map[string]bool{}
	for _, ev := range events {
		if ev.Kind == EvtReading {
			seen[ev.Channel] = true
		}
	}
	if !seen["a"] || !seen["b"] {
		t.Fatalf("expected readings from both channels, got %v", seen)
	}
}

func eventsEqual(a, b []Event) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if eventFingerprint(a[i]) != eventFingerprint(b[i]) {
			return false
		}
	}
	return true
}

func eventFingerprint(ev Event) string {
	buf := bytes.NewBuffer(nil)
	buf.WriteString(ev.Channel)
	buf.WriteByte(byte(ev.Kind))
	switch ev.Kind {
	case EvtReading:
		r := ev.Reading
		buf.WriteString(r.At.String())
		buf.WriteByte(byte(r.FrameSeq))
		buf.WriteByte(byte(r.Status))
		buf.WriteByte(byte(r.Fast >> 16))
		buf.WriteByte(byte(r.Fast >> 8))
		buf.WriteByte(byte(r.Fast))
		buf.WriteByte(byte(r.Sensor.Code))
	case EvtSlowMessage:
		buf.WriteByte(byte(ev.Slow.ID))
		buf.WriteByte(byte(ev.Slow.Data >> 8))
		buf.WriteByte(byte(ev.Slow.Data))
	case EvtFrameError, EvtVariantMismatch:
		buf.WriteByte(byte(ev.FrameErr.Kind))
		buf.WriteByte(byte(ev.FrameErr.FrameSeq))
	case EvtSlowDrop:
		buf.WriteByte(byte(ev.SlowDrop.Reason))
		buf.WriteByte(byte(ev.SlowDrop.FrameSeq))
	case EvtFaultChange:
		buf.WriteByte(byte(ev.Fault.State))
		buf.WriteByte(byte(ev.Fault.Code))
	}
	return buf.String()
}
