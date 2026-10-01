package sent

import "time"

type emittedFrame struct {
	status uint8
	data   [6]uint8
	inj    Inject
}

// emitFrames emits a deterministic back-to-back stream through a sensor and
// returns all falling-edge samples.
func emitFrames(seed uint64, variant Variant, tick time.Duration, frames []emittedFrame) []Sample {
	sensor := NewSensor(seed, SensorConfig{Variant: variant, Tick: tick})
	var all []Sample
	start := time.Duration(0)
	for i, fr := range frames {
		samples := sensor.Emit(i+1, start, fr.status, fr.data, fr.inj)
		all = append(all, samples...)
		if len(samples) > 0 {
			start = samples[len(samples)-1].At
		}
	}
	return all
}

// runSamples drives one pipeline with samples and returns its events.
func runSamples(spec ChannelSpec, samples []Sample) []Event {
	p := newPipeline(spec)
	var events []Event
	for _, s := range samples {
		events = append(events, p.processSample(s)...)
	}
	events = append(events, p.flush()...)
	return events
}

func testSpec(name string, variant Variant, tick time.Duration) ChannelSpec {
	return ChannelSpec{
		Name:     name,
		Frame:    DefaultFrameConfig(tick, 0.15, variant),
		Physical: PhysicalConfig{},
		Fault:    FaultTrackerConfig{PersistThreshold: 3, RecoverThreshold: 2},
	}
}

func countEvents(events []Event, kind EventKind) int {
	n := 0
	for _, ev := range events {
		if ev.Kind == kind {
			n++
		}
	}
	return n
}

func dataNibbles(v uint32) [6]uint8 {
	return [6]uint8{
		uint8(v>>20) & 0x0F,
		uint8(v>>16) & 0x0F,
		uint8(v>>12) & 0x0F,
		uint8(v>>8) & 0x0F,
		uint8(v>>4) & 0x0F,
		uint8(v) & 0x0F,
	}
}
