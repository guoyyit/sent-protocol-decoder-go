package sent

import "time"

// SlowAssembler reassembles cross-frame slow-channel messages.
//
// Every accepted fast frame carries two slow bits in the low bits of its
// status nibble. Message i occupies frames (i*L+1 .. (i+1)*L), where L is 6
// for SlowShort and 16 for SlowEnhanced; the frame sequence tag therefore
// determines both continuity and message alignment, so a lost, repeated or
// reordered frame can never splice content from two messages together.
type SlowAssembler struct {
	variant Variant
	format  SlowFormat
	frames  int // frames (2-bit pairs) per complete message

	lastSeq  int // sequence tag of the last consumed frame; 0 before any
	started  bool
	bits     []uint8
	startSeq int
	startAt  time.Duration
}

// NewSlowAssembler creates an assembler for the given variant's format.
func NewSlowAssembler(variant Variant, format SlowFormat) *SlowAssembler {
	frames := 6
	if format == SlowEnhanced {
		frames = 16
	}
	return &SlowAssembler{variant: variant, format: format, frames: frames}
}

func (a *SlowAssembler) drop(reason SlowDropReason, at time.Duration, seq int) *SlowDrop {
	return &SlowDrop{Reason: reason, At: at, FrameSeq: seq}
}

// clear abandons the in-progress assembly.
func (a *SlowAssembler) clear() {
	a.started = false
	a.bits = a.bits[:0]
}

// mod returns the 0-based pair position of seq within a message.
func (a *SlowAssembler) position(seq int) int {
	return (seq - 1) % a.frames
}

// Observe feeds one accepted frame's sequence tag and two slow bits (0..3).
//
// It returns either an assembled message or a classified drop report, never
// both. A duplicate or out-of-order frame is consumed only for continuity
// bookkeeping and never contributes content.
func (a *SlowAssembler) Observe(seq int, bits uint8, at time.Duration) (*SlowMessage, *SlowDrop) {
	bits &= 0x03

	if a.lastSeq != 0 {
		switch {
		case seq == a.lastSeq:
			return nil, a.drop(SlowDropDuplicate, at, seq)
		case seq < a.lastSeq:
			return nil, a.drop(SlowDropOutOfOrder, at, seq)
		case seq > a.lastSeq+1 && a.started:
			// One or more in-progress frames vanished; the partial message
			// is unrecoverable.
			a.clear()
			a.lastSeq = seq
			d := a.drop(SlowDropLoss, at, seq)
			// Fall through: the frame may still be a clean message start.
			if a.position(seq) != 0 {
				return nil, d
			}
			return a.consume(seq, bits, at, d)
		}
	}
	a.lastSeq = seq
	return a.consume(seq, bits, at, nil)
}

// consume records one pair known to be in sequence order.
func (a *SlowAssembler) consume(seq int, bits uint8, at time.Duration, prior *SlowDrop) (*SlowMessage, *SlowDrop) {
	pos := a.position(seq)

	if pos == 0 {
		if a.format == SlowEnhanced && bits != 0 {
			// Outside an enhanced message only preamble pair 00 may start.
			a.clear()
			return nil, prior
		}
		a.started = true
		a.bits = append(a.bits[:0], bits)
		a.startSeq = seq
		a.startAt = at
		return nil, prior
	}

	if !a.started {
		return nil, prior
	}

	if a.format == SlowEnhanced && pos == 1 && bits != 1 {
		// Second preamble pair must be 01, giving preamble nibble 0001.
		a.clear()
		if prior != nil {
			return nil, prior
		}
		return nil, a.drop(SlowPreambleInvalid, at, seq)
	}

	a.bits = append(a.bits, bits)
	if len(a.bits) < a.frames {
		return nil, prior
	}

	msg, d := a.finish(at)
	a.clear()
	if d != nil && prior != nil {
		return nil, prior
	}
	if msg != nil {
		msg.FrameSeq = a.startSeq
		return msg, prior
	}
	return nil, d
}

// finish validates and decodes one complete collected message.
func (a *SlowAssembler) finish(at time.Duration) (*SlowMessage, *SlowDrop) {
	var word uint32
	for _, pair := range a.bits {
		word = word<<2 | uint32(pair)
	}

	switch a.format {
	case SlowShort:
		// 12 bits: ID nibble then 8-bit data byte.
		return &SlowMessage{
			ID:         uint8(word>>8) & 0x0F,
			Data:       uint16(word) & 0xFF,
			ReceivedAt: at,
		}, nil
	case SlowEnhanced:
		// 32 bits: preamble 0001 | CRC4 | ID nibble | 16-bit data.
		if word>>28 != 0x1 {
			return nil, a.drop(SlowPreambleInvalid, at, a.startSeq)
		}
		gotCRC := uint8(word>>24) & 0x0F
		id := uint8(word>>20) & 0x0F
		data := uint16(word) & 0xFFFF
		nibbles := []uint8{
			id,
			uint8(data >> 12),
			uint8(data >> 8),
			uint8(data >> 4),
			uint8(data),
		}
		if CRC4(a.variant, nibbles) != gotCRC {
			return nil, a.drop(SlowCRCInvalid, at, a.startSeq)
		}
		return &SlowMessage{ID: id, Data: data, ReceivedAt: at}, nil
	}
	return nil, a.drop(SlowCRCInvalid, at, a.startSeq)
}

// Reset discards the in-progress assembly for an externally detected reason
// (e.g. a variant mismatch) and reports the discard.
func (a *SlowAssembler) Reset(reason SlowDropReason, at time.Duration, seq int) *SlowDrop {
	d := a.drop(reason, at, seq)
	a.clear()
	a.lastSeq = seq
	return d
}

// EncodeSlowShort returns the six ordered 2-bit pairs of a short message:
// a 4-bit ID nibble followed by an 8-bit data byte, MSB first.
func EncodeSlowShort(id uint8, data uint8) [6]uint8 {
	var word uint32 = uint32(id&0x0F)<<8 | uint32(data)
	var pairs [6]uint8
	for i := 0; i < 6; i++ {
		pairs[i] = uint8(word>>(10-2*uint(i))) & 0x03
	}
	return pairs
}

// EncodeSlowEnhanced returns the sixteen ordered 2-bit pairs of an enhanced
// message: preamble nibble 0001, CRC4, ID nibble and 16-bit data, MSB first.
func EncodeSlowEnhanced(variant Variant, id uint8, data uint16) [16]uint8 {
	nibbles := []uint8{
		id & 0x0F,
		uint8(data >> 12),
		uint8(data >> 8),
		uint8(data >> 4),
		uint8(data),
	}
	crc := CRC4(variant, nibbles)
	word := uint32(0x1)<<28 | uint32(crc)<<24 | uint32(id&0x0F)<<20 | uint32(data)
	var pairs [16]uint8
	for i := 0; i < 16; i++ {
		pairs[i] = uint8(word>>(30-2*uint(i))) & 0x03
	}
	return pairs
}
