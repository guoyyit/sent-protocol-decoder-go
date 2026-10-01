package sent

// Decoder is a stateful, deterministic frame reassembler for one channel.
// It consumes falling-edge timestamps and emits readings, frame errors,
// slow-channel messages/discards and sensor fault reports in stream order.
type Decoder struct {
	cfg    ChannelConfig
	slow   *SlowAssembler
	faults *FaultTracker

	lastEdge   int64
	haveEdge   bool
	frameIndex int64

	locked      bool
	expectSync  bool
	expectPause bool

	tickUS    float64
	startTime int64
	status    uint8
	nibbles   [6]uint8
	slot      int
}

// NewDecoder creates a decoder for the declared channel configuration.
func NewDecoder(cfg ChannelConfig) *Decoder {
	if cfg.PersistThreshold < 1 {
		cfg.PersistThreshold = 3
	}
	if cfg.TickUS <= 0 {
		cfg.TickUS = 3.0
	}
	if cfg.SyncTol <= 0 {
		cfg.SyncTol = 0.02
	}
	if cfg.WidthTol <= 0 {
		cfg.WidthTol = 0.6
	}
	return &Decoder{
		cfg:        cfg,
		slow:       NewSlowAssembler(cfg.Variant),
		faults:     NewFaultTracker(cfg.PersistThreshold),
		expectSync: true,
	}
}

// PushEdges feeds a contiguous batch of edges into the decoder and returns
// the events produced by them.
func (d *Decoder) PushEdges(edges []Edge) []Event {
	var out []Event
	for _, e := range edges {
		if !d.haveEdge {
			d.lastEdge = e.T
			d.haveEdge = true
			continue
		}
		widthUS := float64(e.T - d.lastEdge)
		d.lastEdge = e.T
		out = append(out, d.consumeWidth(widthUS, e.T)...)
	}
	return out
}

// Flush returns events for state held at the end of a stream.
func (d *Decoder) Flush() []Event {
	return nil
}

// Decode is the pure one-shot form: the same configuration and edge series
// always yields exactly the same events.
func Decode(cfg ChannelConfig, edges []Edge) []Event {
	return NewDecoder(cfg).PushEdges(edges)
}

// consumeWidth processes one inter-edge interval expressed in microseconds.
func (d *Decoder) consumeWidth(widthUS float64, atTime int64) []Event {
	refTick := d.cfg.TickUS
	if d.tickUS > 0 {
		refTick = d.tickUS
	}
	ticks := widthUS / refTick

	if d.expectPause {
		d.expectPause = false
		// The fixed pause/pad interval carries no data; tolerate jitter but
		// still flag gross corruption.
		if raw, ok := quantizeTicks(ticks, d.cfg.WidthTol); !ok || raw != d.cfg.PauseTicks {
			return d.abortFrame(FrameErrPulseWidth, ticks, atTime)
		}
		d.expectSync = true
		return nil
	}

	if d.expectSync {
		return d.consumeSync(widthUS, ticks, atTime)
	}

	// Frame body slot 0 is the status nibble, slots 1..6 are data, slot 7 CRC.
	raw, ok := quantizeTicks(ticks, d.cfg.WidthTol)
	if !ok {
		// Does not snap to an integer pulse width: corrupted/noise pulse.
		return d.abortFrame(FrameErrNoiseGlitch, ticks, atTime)
	}

	switch {
	case raw >= 28 && raw <= 31:
		// 28..31 ticks is a reserved/illegal data nibble value.
		return d.abortFrame(FrameErrIllegalNibble, ticks, atTime)
	case raw >= 12 && raw <= 27:
		if d.slot > 7 || d.slot < 0 {
			// Unexpected extra pulse before the next sync: timing corruption.
			return d.abortFrame(FrameErrPulseWidth, ticks, atTime)
		}
		n := uint8(raw - 12)
		switch d.slot {
		case 0:
			d.status = n
		case 7:
			return d.finishFrame(n, atTime)
		default:
			d.nibbles[d.slot-1] = n
		}
		d.slot++
		return nil
	case raw < 12:
		// Too short to be any legal SENT pulse: noise glitch.
		return d.abortFrame(FrameErrNoiseGlitch, ticks, atTime)
	default:
		// 32+ ticks inside the frame body: the pulse width left tolerance.
		return d.abortFrame(FrameErrPulseWidth, ticks, atTime)
	}
}

// consumeSync looks for the 56-tick synchronization pulse. widthUS is the
// measured interval in microseconds; ticks is the same interval in tick units.
func (d *Decoder) consumeSync(widthUS, ticks float64, atTime int64) []Event {
	// Detect the sync pulse by its ratio to the nominal tick time, allowing
	// the frame's clock rate to differ within SyncTol.
	if within(ticks, 56, 56*d.cfg.SyncTol) {
		measured := widthUS / 56
		if !within(measured, d.cfg.TickUS, d.cfg.TickUS*d.cfg.SyncTol) {
			return []Event{d.frameError(FrameErrSyncMissing, ticks, atTime)}
		}
		d.tickUS = measured
		d.locked = true
		d.startFrame(atTime)
		return nil
	}

	raw, ok := quantizeTicks(ticks, d.cfg.WidthTol)
	if !ok {
		// Does not snap to any integer pulse width within tolerance: noise.
		return []Event{d.frameError(FrameErrNoiseGlitch, ticks, atTime)}
	}

	switch {
	case d.cfg.PauseTicks > 0 && raw == d.cfg.PauseTicks:
		// Pad interval left behind by a rejected frame; carry no data.
		return nil
	case raw >= 12 && raw <= 27:
		// A legal nibble-width pulse where the sync pulse should be.
		if d.locked {
			return []Event{d.frameError(FrameErrSyncMissing, ticks, atTime)}
		}
		// Still searching: silently pass over legal-width idle pulses.
		return nil
	case raw >= 28 && raw <= 31:
		return []Event{d.frameError(FrameErrIllegalNibble, ticks, atTime)}
	case raw >= 32 && raw <= 55:
		// Wider than any legal nibble, not wide enough for a sync: this is a
		// pulse whose timing left the legal window.
		return []Event{d.frameError(FrameErrPulseWidth, ticks, atTime)}
	case raw < 0 || raw > 64:
		return []Event{d.frameError(FrameErrNoiseGlitch, ticks, atTime)}
	default:
		// 57..64: close to a sync but not one: the sync pulse is malformed.
		return []Event{d.frameError(FrameErrSyncMissing, ticks, atTime)}
	}
}

// startFrame resets frame-body assembly after a sync pulse was accepted.
func (d *Decoder) startFrame(atTime int64) {
	d.expectSync = false
	d.slot = 0
	d.status = 0
	d.nibbles = [6]uint8{}
	d.startTime = atTime
}

// abortFrame emits a timing/legality error, invalidates any partial slow
// assembly and re-enters sync search.
func (d *Decoder) abortFrame(class FrameErrorClass, ticks float64, atTime int64) []Event {
	d.expectSync = true
	out := []Event{d.frameError(class, ticks, atTime)}
	if d.locked {
		if disc := d.slow.linkLoss(atTime); disc != nil {
			out = append(out, d.discardEvent(disc))
		}
	}
	return out
}

// finishFrame validates CRC and emits the frame's reading, slow-channel and
// fault events, or rejects the frame on checksum mismatch.
func (d *Decoder) finishFrame(gotCRC uint8, atTime int64) []Event {
	wantCRC := frameCRC(d.cfg.Variant, d.status, d.nibbles)
	if gotCRC != wantCRC {
		d.expectSync = true
		out := []Event{d.frameError(FrameErrCRC, float64(12+gotCRC), atTime)}
		if disc := d.slow.linkLoss(atTime); disc != nil {
			out = append(out, d.discardEvent(disc))
		}
		return out
	}
	if d.cfg.PauseTicks > 0 {
		d.expectPause = true
		d.expectSync = false
	} else {
		d.expectSync = true
	}

	seq := d.status & 0x03
	slowBits := (d.status >> 2) & 0x03
	frame := &Frame{
		ChannelID: d.cfg.ID,
		Index:     d.frameIndex,
		StartTime: d.startTime,
		TickUS:    d.tickUS,
		Status:    d.status,
		Seq:       seq,
		SlowBits:  slowBits,
		Nibbles:   d.nibbles,
		CRC:       gotCRC,
	}
	d.frameIndex++

	reading := decodeReading(d.cfg, frame)
	out := []Event{{
		ChannelID: d.cfg.ID,
		Time:      atTime,
		Kind:      EventReading,
		Reading:   reading,
	}}

	if msg, disc := d.slow.push(frame); msg != nil {
		out = append(out, Event{
			ChannelID: d.cfg.ID,
			Time:      msg.Time,
			Kind:      EventSlowMessage,
			Slow:      msg,
		})
	} else if disc != nil {
		out = append(out, d.discardEvent(disc))
	}

	if rep := d.faults.observe(atTime, reading.ErrA, reading.ErrB); rep != nil {
		out = append(out, Event{
			ChannelID: d.cfg.ID,
			Time:      rep.Time,
			Kind:      EventSensorFault,
			Fault:     rep,
		})
	}

	return out
}

func (d *Decoder) frameError(class FrameErrorClass, ticks float64, atTime int64) Event {
	return Event{
		ChannelID: d.cfg.ID,
		Time:      atTime,
		Kind:      EventFrameError,
		FrameErr: &FrameError{
			Class:      class,
			Time:       atTime,
			WidthTicks: ticks,
		},
	}
}

func (d *Decoder) discardEvent(disc *SlowDiscard) Event {
	return Event{
		ChannelID: d.cfg.ID,
		Time:      disc.Time,
		Kind:      EventSlowDiscard,
		Discard:   disc,
	}
}

// decodeReading extracts raw/error bits and converts them to physical values.
func decodeReading(cfg ChannelConfig, f *Frame) *Reading {
	n := f.Nibbles
	rawA := uint16(n[0])<<8 | uint16(n[1])<<4 | uint16(n[2])
	rawB := uint16(n[3])<<8 | uint16(n[4])<<4 | uint16(n[5])

	errA := rawA&0x0800 != 0
	errB := rawB&0x0800 != 0
	vA := rawA & 0x07FF
	vB := rawB & 0x07FF

	return &Reading{
		Frame: f,
		RawA:  vA,
		RawB:  vB,
		ErrA:  errA,
		ErrB:  errB,
		PhysA: scaleValue(vA, cfg.ScaleA),
		PhysB: scaleValue(vB, cfg.ScaleB),
		UnitA: cfg.ScaleA.Unit,
		UnitB: cfg.ScaleB.Unit,
	}
}

func scaleValue(raw uint16, s FastScale) float64 {
	return s.Min + float64(raw)/2047.0*(s.Max-s.Min)
}

// quantizeTicks snaps a measured interval to its nearest integer tick. It
// rejects intervals that lie more than tol ticks away from that integer, so
// normal jitter decodes cleanly while a deliberately off-width pulse is caught.
func quantizeTicks(ticks float64, tol float64) (int, bool) {
	nearest := int(ticks + 0.5)
	diff := ticks - float64(nearest)
	if diff < 0 {
		diff = -diff
	}
	return nearest, diff <= tol
}

func within(v, target, tol float64) bool {
	diff := v - target
	if diff < 0 {
		diff = -diff
	}
	return diff <= tol
}
