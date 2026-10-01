package sent

import (
	"testing"
	"time"
)

func TestVariantMismatchDetectedAndRejected(t *testing.T) {
	tick := 3 * time.Microsecond

	// Emit frames produced by a VariantB sensor, decode with VariantA.
	samples := emitFrames(9, VariantB, tick, []emittedFrame{
		{status: 0, data: dataNibbles(0xABCDEF)},
		{status: 0, data: dataNibbles(0x123456)},
	})

	events := runSamples(testSpec("a", VariantA, tick), samples)

	var mismatchEvents, variantErrors int
	for _, ev := range events {
		switch {
		case ev.Kind == EvtVariantMismatch:
			mismatchEvents++
		case ev.Kind == EvtFrameError && ev.FrameErr.Kind == ErrVariantMismatch:
			variantErrors++
		case ev.Kind == EvtReading:
			t.Fatalf("variant-mismatched frames must not produce readings")
		}
	}

	// First detection is reported distinctly; every mismatching frame is
	// still rejected per-frame.
	if mismatchEvents != 1 {
		t.Fatalf("expected exactly 1 first-detection event, got %d", mismatchEvents)
	}
	if variantErrors != 2 {
		t.Fatalf("expected 2 per-frame rejections, got %d", variantErrors)
	}
}

func TestVariantResetDropsSlowAssembly(t *testing.T) {
	tick := 3 * time.Microsecond

	// Build VariantB frames carrying a short slow message across 6 frames,
	// but decode as VariantA. The mismatch must prevent splicing.
	pairs := EncodeSlowShort(0xF, 0x77)
	frames := make([]emittedFrame, 6)
	for i := range frames {
		frames[i] = emittedFrame{status: pairs[i], data: dataNibbles(uint32(i))}
	}
	samples := emitFrames(3, VariantB, tick, frames)
	events := runSamples(testSpec("a", VariantA, tick), samples)

	for _, ev := range events {
		if ev.Kind == EvtSlowMessage {
			t.Fatalf("no slow message may assemble across mismatched variants")
		}
	}
}

func TestBothVariantsDecodeTheirOwn(t *testing.T) {
	tick := 3 * time.Microsecond
	for _, variant := range []Variant{VariantA, VariantB} {
		samples := emitFrames(5, variant, tick, []emittedFrame{
			{status: 0, data: dataNibbles(0x13579B)},
		})
		events := runSamples(testSpec("v", variant, tick), samples)
		if countEvents(events, EvtReading) != 1 {
			t.Fatalf("variant %v must decode its own frames, events: %v", variant, events)
		}
	}
}

var _ = time.Microsecond
