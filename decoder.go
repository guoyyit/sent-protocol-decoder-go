package sent

import (
	"fmt"
	"math"
	"time"
)

// SENT low-level timing constants.
const (
	// MinNibbleTicks/MaxNibbleTicks bound a legal data/status/CRC pulse:
	// the pulse width in ticks encodes nibble value plus 12.
	MinNibbleTicks = 12
	MaxNibbleTicks = 27

	defaultSyncTicks = 56
)

// DefaultFrameConfig returns the standard timing configuration for a variant.
func DefaultFrameConfig(tick time.Duration, tolerance float64, variant Variant) FrameConfig {
	return FrameConfig{
		Tick:        tick,
		Tolerance:   tolerance,
		SyncTicks:   defaultSyncTicks,
		DataNibbles: 6,
		Variant:     variant,
	}
}

// ResultKind distinguishes reconstructed frames from timing errors.
type ResultKind int

const (
	ResultFrame ResultKind = iota
	ResultError
)

// DecoderResult is one decoder output item.
type DecoderResult struct {
	Kind  ResultKind
	Frame *RawFrame
	Err   *FrameError
}

// Decoder reconstructs frames from a tagged falling-edge pulse stream.
//
// It resynchronizes on every valid 56-tick sync pulse, so pause pulses and
// untagged noise never shift frame alignment. Pulses attributed (by the
// out-of-band capture tag) to an expected frame whose sync pulse is invalid
// are reported once as ErrSyncMissing; subsequent stray pulses of that frame
// are ignored until the next valid sync.
type Decoder struct {
	cfg FrameConfig

	waiting   bool
	frameSeq  int // sequence tag of the frame being assembled
	startAt   time.Duration
	nibbles   []uint8 // status, data..., crc
	lastSeq   int     // last fully assembled frame
	missedSeq int     // frame a missing-sync error was already raised for
}

// NewDecoder creates a pulse-to-frame decoder.
func NewDecoder(cfg FrameConfig) *Decoder {
	if cfg.SyncTicks <= 0 {
		cfg.SyncTicks = defaultSyncTicks
	}
	if cfg.DataNibbles <= 0 {
		cfg.DataNibbles = 6
	}
	return &Decoder{cfg: cfg, waiting: true}
}

func (d *Decoder) pulseError(kind ErrorKind, at time.Duration, seq int, format string, args ...any) DecoderResult {
	return DecoderResult{
		Kind: ResultError,
		Err:  &FrameError{Kind: kind, At: at, FrameSeq: seq, Detail: fmt.Sprintf(format, args...)},
	}
}

// nearestTicks rounds a pulse width to an integer number of unit ticks.
func (d *Decoder) nearestTicks(width time.Duration) int {
	return int(math.Round(float64(width) / float64(d.cfg.Tick)))
}

// deviation returns the relative deviation of width from ticks*Tick.
func (d *Decoder) deviation(width time.Duration, ticks int) float64 {
	nominal := time.Duration(ticks) * d.cfg.Tick
	return math.Abs(float64(width-nominal)) / float64(nominal)
}

func (d *Decoder) isSync(p Pulse) bool {
	ticks := d.nearestTicks(p.Width)
	return ticks == d.cfg.SyncTicks && d.deviation(p.Width, ticks) <= d.cfg.Tolerance
}

// abortFrame discards the partial frame and returns to sync hunting.
func (d *Decoder) abortFrame() {
	d.waiting = true
	d.frameSeq = 0
	d.nibbles = d.nibbles[:0]
}

// Push feeds one pulse and returns any newly completed results.
func (d *Decoder) Push(p Pulse) []DecoderResult {
	if d.waiting {
		return d.pushWaiting(p)
	}
	return d.pushFrame(p)
}

func (d *Decoder) pushWaiting(p Pulse) []DecoderResult {
	if d.isSync(p) {
		d.waiting = false
		d.frameSeq = p.FrameSeq
		d.startAt = p.At
		d.nibbles = d.nibbles[:0]
		return nil
	}

	// Stray pulses of a frame abandoned due to a data error are ignored.
	if p.FrameSeq != 0 && p.FrameSeq == d.frameSeq {
		return nil
	}
	// A non-sync pulse attributed to a not-yet-seen frame means its sync
	// pulse was missing or invalid. Report once per affected frame.
	if p.FrameSeq != 0 && p.FrameSeq > d.lastSeq && p.FrameSeq != d.missedSeq {
		d.missedSeq = p.FrameSeq
		return []DecoderResult{d.pulseError(ErrSyncMissing, p.At, p.FrameSeq,
			"frame %d has no valid %d-tick sync pulse", p.FrameSeq, d.cfg.SyncTicks)}
	}
	// Untagged noise, duplicated stray pulses and pause pulses: ignore.
	return nil
}

func (d *Decoder) pushFrame(p Pulse) []DecoderResult {
	// A new valid sync (or a later frame) always forces resynchronization.
	if p.FrameSeq != 0 && p.FrameSeq != d.frameSeq {
		if d.isSync(p) {
			d.waiting = false
			d.frameSeq = p.FrameSeq
			d.startAt = p.At
			d.nibbles = d.nibbles[:0]
			return nil
		}
		if p.FrameSeq < d.frameSeq {
			return nil // stray pulse belonging to an abandoned frame
		}
		// Later frame content without sync: leave assembly, report missing.
		d.lastSeq = d.frameSeq
		d.abortFrame()
		d.missedSeq = p.FrameSeq
		return []DecoderResult{d.pulseError(ErrSyncMissing, p.At, p.FrameSeq,
			"frame %d started without a sync pulse", p.FrameSeq)}
	}

	ticks := d.nearestTicks(p.Width)
	switch {
	case ticks < MinNibbleTicks || ticks > MaxNibbleTicks:
		d.abortFrame()
		return []DecoderResult{d.pulseError(ErrIllegalNibble, p.At, d.frameSeq,
			"pulse width %d ticks is outside legal nibble range %d..%d",
			ticks, MinNibbleTicks, MaxNibbleTicks)}
	case d.deviation(p.Width, ticks) > d.cfg.Tolerance:
		d.abortFrame()
		return []DecoderResult{d.pulseError(ErrPulseOutOfTolerance, p.At, d.frameSeq,
			"pulse deviates %.1f%% from %d ticks, tolerance %.1f%%",
			d.deviation(p.Width, ticks)*100, ticks, d.cfg.Tolerance*100)}
	}

	d.nibbles = append(d.nibbles, uint8(ticks-MinNibbleTicks))
	if len(d.nibbles) < 2+d.cfg.DataNibbles {
		return nil
	}

	frame := &RawFrame{
		FrameSeq: d.frameSeq,
		StartAt:  d.startAt,
		Status:   d.nibbles[0],
		CRC:      d.nibbles[len(d.nibbles)-1],
	}
	for i := 0; i < d.cfg.DataNibbles && i < len(frame.Data); i++ {
		frame.Data[i] = d.nibbles[1+i]
	}
	d.lastSeq = d.frameSeq
	d.waiting = true
	d.nibbles = d.nibbles[:0]
	return []DecoderResult{{Kind: ResultFrame, Frame: frame}}
}

// Flush reports an unfinished frame at end of stream.
func (d *Decoder) Flush() []DecoderResult {
	if d.waiting || len(d.nibbles) == 0 {
		return nil
	}
	seq := d.frameSeq
	at := d.startAt
	d.waiting = true
	d.nibbles = d.nibbles[:0]
	return []DecoderResult{d.pulseError(ErrSyncMissing, at, seq,
		"frame %d truncated at end of stream", seq)}
}
