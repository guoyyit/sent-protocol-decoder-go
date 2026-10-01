package sent

import "testing"

// encodeSlowBits produces the 2-bit-per-frame status payload (MSB first) for
// a complete slow message of the given variant.
func encodeSlowBits(v Variant, id uint8, data uint16) []uint8 {
	spec := slowLayout(v)
	bits := make([]byte, 0, spec.bits)
	push := func(val uint32, n int) {
		for i := n - 1; i >= 0; i-- {
			bits = append(bits, byte((val>>uint(i))&1))
		}
	}
	push(uint32(spec.tag), 2)
	push(uint32(id), 2)
	push(uint32(data), spec.dataBits)
	crc := slowCRC(v, bits)
	push(uint32(crc), 4)

	out := make([]uint8, len(bits)/2)
	for i := range out {
		out[i] = bits[2*i]<<1 | bits[2*i+1]
	}
	return out
}

func slowSpecs(v Variant, slowBits []uint8) []FrameSpec {
	specs := make([]FrameSpec, len(slowBits))
	for i, b := range slowBits {
		specs[i] = FrameSpec{SlowBits: b, RawA: 100, RawB: 200}
	}
	return specs
}

func TestSlowMessageAssembles(t *testing.T) {
	cfg := DefaultConfig("s", VariantRecommended)
	bits := encodeSlowBits(VariantRecommended, 0b10, 0xAB)
	edges := NewEncoder(cfg, 3).Build(slowSpecs(VariantRecommended, bits), nil)
	events := Decode(cfg, edges)

	var msg *SlowMessage
	for _, ev := range events {
		if ev.Kind == EventSlowMessage {
			msg = ev.Slow
		}
	}
	if msg == nil {
		t.Fatal("slow message not assembled")
	}
	if msg.ID != 0b10 || msg.Data != 0xAB {
		t.Fatalf("id=%d data=%#x", msg.ID, msg.Data)
	}
}

func TestSlowLegacyAndExtendedLengths(t *testing.T) {
	cases := []struct {
		v      Variant
		data   uint16
		frames int
	}{
		{VariantLegacy, 0x9, 6},
		{VariantExtended, 0xCAFE, 12},
	}
	for _, tc := range cases {
		cfg := DefaultConfig("s", tc.v)
		bits := encodeSlowBits(tc.v, 0b01, tc.data)
		if len(bits) != tc.frames {
			t.Fatalf("%s: %d frames of bits, want %d", tc.v, len(bits), tc.frames)
		}
		edges := NewEncoder(cfg, 3).Build(slowSpecs(tc.v, bits), nil)
		events := Decode(cfg, edges)
		found := false
		for _, ev := range events {
			if ev.Kind == EventSlowMessage && ev.Slow.Data == tc.data {
				found = true
			}
		}
		if !found {
			t.Fatalf("%s: message with data %#x not decoded", tc.v, tc.data)
		}
	}
}

func TestSlowDroppedFrame(t *testing.T) {
	cfg := DefaultConfig("s", VariantRecommended)
	bits := encodeSlowBits(VariantRecommended, 0, 1)
	specs := slowSpecs(VariantRecommended, bits)
	ops := []InjectOp{{Type: OpDropFrame, FrameIndex: 3}}
	edges := NewEncoder(cfg, 5).Build(specs, ops)
	events := Decode(cfg, edges)

	for _, ev := range events {
		if ev.Kind == EventSlowMessage {
			t.Fatal("must not assemble a message across a dropped frame")
		}
		if ev.Kind == EventSlowDiscard &&
			ev.Discard.Reason != SlowDiscardLostFrame &&
			ev.Discard.Reason != SlowDiscardOutOfOrder &&
			ev.Discard.Reason != SlowDiscardLinkCorrupt &&
			ev.Discard.Reason != SlowDiscardVariantMismatch &&
			ev.Discard.Reason != SlowDiscardCRC {
			t.Fatalf("want a loss-class discard, got %v", ev.Discard.Reason)
		}
	}

	// And verify the assembler-level single-frame gap is classified as lost.
	words := encodeSlowBits(VariantRecommended, 0, 1)
	a := NewSlowAssembler(VariantRecommended)
	sawLost := false
	for i := 0; i <= len(words); i++ {
		seq := i
		if i >= 3 {
			seq++ // skip sequence value 3: one frame vanished
		}
		if i == len(words) {
			break
		}
		f := &Frame{StartTime: int64(i), Seq: uint8(seq & 3), SlowBits: words[i]}
		if _, d := a.push(f); d != nil && d.Reason == SlowDiscardLostFrame {
			sawLost = true
		}
	}
	if !sawLost {
		t.Fatal("assembler must classify a delta-2 gap as SlowDiscardLostFrame")
	}
}

func TestSlowDuplicateFrame(t *testing.T) {
	cfg := DefaultConfig("s", VariantRecommended)
	bits := encodeSlowBits(VariantRecommended, 0, 2)
	specs := slowSpecs(VariantRecommended, bits)
	ops := []InjectOp{{Type: OpDuplicateFrame, FrameIndex: 2}}
	edges := NewEncoder(cfg, 5).Build(specs, ops)
	events := Decode(cfg, edges)

	sawDup := false
	for _, ev := range events {
		if ev.Kind == EventSlowMessage {
			// A message assembled after the duplicate reset is only allowed
			// if it comes from a complete clean window (verified by CRC).
			if ev.Slow.Data != 2 {
				t.Fatalf("spliced message with wrong data %#x", ev.Slow.Data)
			}
		}
		if ev.Kind == EventSlowDiscard {
			if ev.Discard.Reason == SlowDiscardDuplicate {
				sawDup = true
			}
		}
	}
	if !sawDup {
		t.Fatal("want a duplicate discard reason")
	}
}

func TestSlowOutOfOrderFrame(t *testing.T) {
	cfg := DefaultConfig("s", VariantRecommended)
	bits := encodeSlowBits(VariantRecommended, 0, 3)
	specs := slowSpecs(VariantRecommended, bits)
	ops := []InjectOp{{Type: OpSwapFrames, FrameIndex: 2}}
	edges := NewEncoder(cfg, 5).Build(specs, ops)
	events := Decode(cfg, edges)

	sawReorder := false
	for _, ev := range events {
		if ev.Kind == EventSlowMessage {
			t.Fatal("reordered frames must not produce a spliced message")
		}
		if ev.Kind == EventSlowDiscard && ev.Discard.Reason == SlowDiscardOutOfOrder {
			sawReorder = true
		}
	}
	if !sawReorder {
		t.Fatal("want an out-of-order discard reason")
	}
}

// pushSlowWords feeds 2-bit slow words through a fresh assembler with the
// normal 0,1,2,3 status sequence counter.
func pushSlowWords(v Variant, words []uint8) (*SlowMessage, *SlowDiscard) {
	a := NewSlowAssembler(v)
	var lastDisc *SlowDiscard
	var msg *SlowMessage
	for i, w := range words {
		f := &Frame{StartTime: int64(i), Seq: uint8(i & 3), SlowBits: w}
		m, d := a.push(f)
		if m != nil {
			msg = m
		}
		if d != nil {
			lastDisc = d
		}
	}
	return msg, lastDisc
}

func TestSlowCRCCorrupt(t *testing.T) {
	bits := encodeSlowBits(VariantRecommended, 0, 4)
	bits[1] ^= 0x01
	msg, disc := pushSlowWords(VariantRecommended, bits)
	if msg != nil {
		t.Fatal("bad slow CRC must not emit a message")
	}
	if disc == nil || disc.Reason != SlowDiscardCRC {
		t.Fatalf("want slow-CRC discard, got %+v", disc)
	}
}

func TestSlowLinkCorruptInvalidatesPartial(t *testing.T) {
	cfg := DefaultConfig("s", VariantRecommended)
	bits := encodeSlowBits(VariantRecommended, 0, 5)
	specs := slowSpecs(VariantRecommended, bits)
	ops := []InjectOp{{Type: OpCorruptCRC, FrameIndex: 3}}
	edges := NewEncoder(cfg, 5).Build(specs, ops)
	events := Decode(cfg, edges)

	sawLink := false
	for _, ev := range events {
		if ev.Kind == EventSlowMessage {
			t.Fatal("message cannot survive a corrupt fast frame")
		}
		if ev.Kind == EventSlowDiscard && ev.Discard.Reason == SlowDiscardLinkCorrupt {
			sawLink = true
		}
	}
	if !sawLink {
		t.Fatal("want link-corrupt discard for partial message")
	}
}

func TestVariantMismatchReported(t *testing.T) {
	// Declared recommended; an arriving stream carries a legacy tag (01) in a
	// complete, otherwise well-formed message.
	bits := encodeSlowBits(VariantLegacy, 0, 1)

	// The recommended layout is 16 bits; pad the 12-bit legacy payload to 8
	// words so the recommended assembler reaches a full assembly boundary.
	a := NewSlowAssembler(VariantRecommended)
	var mismatch *SlowDiscard
	for i := 0; i < 8; i++ {
		w := uint8(0)
		if i < len(bits) {
			w = bits[i]
		}
		f := &Frame{StartTime: int64(i), Seq: uint8(i & 3), SlowBits: w}
		if _, d := a.push(f); d != nil && d.Reason == SlowDiscardVariantMismatch {
			mismatch = d
		}
	}
	if mismatch == nil {
		t.Fatal("want variant-mismatch discard on first detected foreign format")
	}
}
