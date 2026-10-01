package sent

// DiscardReason distinguishes why slow-channel data failed to assemble.
type DiscardReason int

const (
	SlowDiscardLostFrame DiscardReason = iota
	SlowDiscardDuplicate
	SlowDiscardOutOfOrder
	SlowDiscardCRC
	SlowDiscardVariantMismatch
	SlowDiscardLinkCorrupt
)

// SlowMessage is one fully assembled slow-channel message.
type SlowMessage struct {
	Variant Variant
	ID      uint8
	Data    uint16
	Time    int64
}

// SlowDiscard reports slow-channel bytes/frames that were dropped.
type SlowDiscard struct {
	Reason DiscardReason
	Time   int64
	// Seq is the status-nibble sequence counter of the offending frame.
	Seq uint8
}

// SlowAssembler reconstructs slow-channel messages across fast frames.
type SlowAssembler struct {
	variant Variant
	spec    slowSpec

	bits      []byte
	hasSeq    bool
	expected  uint8
	startTime int64
}

// NewSlowAssembler creates an assembler for the declared variant.
func NewSlowAssembler(v Variant) *SlowAssembler {
	return &SlowAssembler{variant: v, spec: slowLayout(v)}
}

func (s *SlowAssembler) reset(time int64) {
	s.bits = s.bits[:0]
	s.startTime = time
}

func (s *SlowAssembler) discard(seq uint8, time int64, reason DiscardReason) *SlowDiscard {
	return &SlowDiscard{Reason: reason, Time: time, Seq: seq}
}

// push consumes one accepted frame. It returns either a complete message, a
// discard report, or neither.
func (s *SlowAssembler) push(f *Frame) (*SlowMessage, *SlowDiscard) {
	time := f.StartTime

	if s.hasSeq {
		delta := int(f.Seq-s.expected) & 0x03
		switch delta {
		case 0:
			// Same counter as the last accepted frame: duplicate.
			return nil, s.discard(f.Seq, time, SlowDiscardDuplicate)
		case 1:
			// In order: fall through to bit assembly below.
		case 2:
			// Exactly one intervening sequence number is missing: frame loss.
			s.reset(time)
			s.expected = f.Seq
			return nil, s.discard(f.Seq, time, SlowDiscardLostFrame)
		case 3:
			// Counter is older than expected: frames arrived out of order.
			// The stale frame is rejected; the expected counter is retained
			// so the following in-order frame re-synchronizes the assembly.
			s.reset(time)
			return nil, s.discard(f.Seq, time, SlowDiscardOutOfOrder)
		}
	}
	s.expected = f.Seq
	s.hasSeq = true

	// Status-nibble bit 1 then bit 0, MSB first over the whole message.
	s.bits = append(s.bits, (f.SlowBits>>1)&1, f.SlowBits&1)
	if len(s.bits) == 1 {
		s.startTime = time
	}

	if len(s.bits) < s.spec.bits {
		return nil, nil
	}
	if len(s.bits) > s.spec.bits {
		s.reset(time)
		return nil, s.discard(f.Seq, time, SlowDiscardOutOfOrder)
	}

	tag := uint8(readBits(s.bits, 0, 2))
	if tag != s.spec.tag {
		s.reset(time)
		return nil, s.discard(f.Seq, time, SlowDiscardVariantMismatch)
	}

	crc := uint8(readBits(s.bits, s.spec.bits-4, 4))
	if slowCRC(s.variant, s.bits[:s.spec.bits-4]) != crc {
		s.reset(time)
		return nil, s.discard(f.Seq, time, SlowDiscardCRC)
	}

	msg := &SlowMessage{
		Variant: s.variant,
		ID:      uint8(readBits(s.bits, 2, 2)),
		Data:    uint16(readBits(s.bits, 4, s.spec.dataBits)),
		Time:    s.startTime,
	}
	s.reset(time)
	return msg, nil
}

// linkLoss tells the assembler that a frame was lost to a link-level failure
// (CRC/timing), so any partially assembled message must be invalidated.
func (s *SlowAssembler) linkLoss(time int64) *SlowDiscard {
	if len(s.bits) > 0 {
		seq := s.expected
		s.reset(time)
		return &SlowDiscard{Reason: SlowDiscardLinkCorrupt, Time: time, Seq: seq}
	}
	return nil
}

// readBits interprets count bits starting at offset (MSB first) as an int.
func readBits(bits []byte, offset, count int) uint32 {
	var v uint32
	for i := offset; i < offset+count && i < len(bits); i++ {
		v = (v << 1) | uint32(bits[i]&1)
	}
	return v
}
