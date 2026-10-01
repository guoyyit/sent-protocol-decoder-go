package sent

import (
	"math/rand/v2"
	"time"
)

// Pipe is an in-memory ordered edge stream acting as a local loopback
// between an edge producer and a receiver.
type Pipe struct {
	ch     chan Sample
	closed bool
}

// NewPipe creates an in-memory edge pipe with the given channel capacity.
func NewPipe(buffer int) *Pipe {
	if buffer < 0 {
		buffer = 0
	}
	return &Pipe{ch: make(chan Sample, buffer)}
}

// Push appends one edge sample, blocking until a slot is available.
func (p *Pipe) Push(s Sample) {
	p.ch <- s
}

// TryPush appends one edge sample without blocking. It reports success.
func (p *Pipe) TryPush(s Sample) bool {
	if p.closed {
		return false
	}
	select {
	case p.ch <- s:
		return true
	default:
		return false
	}
}

// Samples returns the receive side of the pipe.
func (p *Pipe) Samples() <-chan Sample { return p.ch }

// Close marks the stream as finished; receivers see channel closure.
func (p *Pipe) Close() {
	if !p.closed {
		p.closed = true
		close(p.ch)
	}
}

// SensorConfig configures the simulated SENT sensor.
type SensorConfig struct {
	// Variant selects CRC polynomial and slow-channel format.
	Variant Variant
	// Tick is the emitted unit-tick duration.
	Tick time.Duration
	// Jitter is the default relative per-pulse jitter (0 = perfect timing).
	Jitter float64
	// PauseTicks inserts a pause pulse (>= 12 ticks) after each frame; 0
	// disables the pause.
	PauseTicks int
}

// Inject selects optional corruptions for one emitted frame.
type Inject struct {
	// CorruptCRC flips one bit of the checksum nibble.
	CorruptCRC bool
	// BadPulse, when > 0, applies over-tolerance jitter to pulse index
	// 1..8 (1 = status, 2..7 = data, 8 = crc); 0 disables. Corrupt the
	// sync pulse with DropSync.
	BadPulse int
	// IllegalWidth replaces the BadPulse-index pulse with a 30-tick width,
	// which is outside the legal nibble range.
	IllegalWidth bool
	// DropSync suppresses the sync pulse entirely.
	DropSync bool
	// Jitter overrides the configured jitter for this frame (< 0 = default).
	Jitter float64
	// Glitches adds untagged narrow noise pulses before the frame data.
	Glitches int
}

// Sensor emits deterministic SENT falling-edge sequences from a seeded RNG.
type Sensor struct {
	cfg  SensorConfig
	rand *rand.Rand
}

// NewSensor creates a simulated sensor. Equal seeds and inputs produce
// byte-identical edge timing.
func NewSensor(seed uint64, cfg SensorConfig) *Sensor {
	return &Sensor{cfg: cfg, rand: rand.New(rand.NewPCG(seed, seed))}
}

// Variant reports the sensor's configured variant.
func (s *Sensor) Variant() Variant { return s.cfg.Variant }

// Emit encodes one frame (status + 6 data nibbles) as a time-ordered slice
// of falling-edge samples. Every pulse of the frame carries the same 1-based
// FrameSeq tag; injected noise carries tag 0.
func (s *Sensor) Emit(seq int, start time.Duration, status uint8, data [6]uint8, inj Inject) []Sample {
	jitter := s.cfg.Jitter

	nibbles := make([]uint8, 0, 8)
	nibbles = append(nibbles, status&0x0F)
	for _, nibble := range data {
		nibbles = append(nibbles, nibble&0x0F)
	}
	crc := CRC4(s.cfg.Variant, nibbles)
	if inj.CorruptCRC {
		crc ^= 0x1
	}
	nibbles = append(nibbles, crc)

	pulseTicks := func(index int, ticks int) time.Duration {
		switch {
		case inj.BadPulse >= 1 && inj.BadPulse <= 8 && inj.IllegalWidth && index == inj.BadPulse:
			ticks = MaxNibbleTicks + 3
		}
		noise := 0.0
		isBad := inj.BadPulse >= 1 && inj.BadPulse <= 8 && index == inj.BadPulse
		if jitter > 0 && !isBad {
			noise = (s.rand.Float64()*2 - 1) * jitter
		}
		if isBad && inj.Jitter > 0 {
			// Keep the rounded tick count intact while pushing the width
			// beyond a tight tolerance (about -3.5% from the tick center).
			noise = -0.49 / float64(ticks)
		}
		return time.Duration(float64(ticks) * (1 + noise) * float64(s.cfg.Tick))
	}

	at := start
	out := make([]Sample, 0, 10+inj.Glitches)

	// Reference edge at the frame start: it carries no pulse (the first
	// real pulse opens with the following edge) but lets a receiver compute
	// the sync pulse width even when start is not zero.
	out = append(out, Sample{At: at, Edge: EdgeFalling, FrameSeq: 0})

	add := func(width time.Duration, tagged bool) {
		at += width
		tag := 0
		if tagged {
			tag = seq
		}
		out = append(out, Sample{At: at, Edge: EdgeFalling, FrameSeq: tag})
	}

	for g := 0; g < inj.Glitches; g++ {
		add(time.Duration(3+g%2)*s.cfg.Tick, false)
	}

	if !inj.DropSync {
		add(pulseTicks(0, defaultSyncTicks), true)
	} else {
		// Keep the frame's temporal position advancing so following pulses
		// remain attributable to the same frame tag.
		at += time.Duration(defaultSyncTicks) * s.cfg.Tick
	}

	for index, ticks := range nibbles {
		add(pulseTicks(index+1, MinNibbleTicks+int(ticks)), true)
	}

	if s.cfg.PauseTicks > 0 {
		add(time.Duration(s.cfg.PauseTicks)*s.cfg.Tick, false)
	}
	return out
}

// NextFrameStart estimates the timestamp where the following frame begins,
// useful for deterministic back-to-back emission by callers.
func (s *Sensor) NextFrameStart(samples []Sample) time.Duration {
	if len(samples) == 0 {
		return 0
	}
	return samples[len(samples)-1].At
}

// FrameDuration returns the minimum (all-nibble-zero, jitter-free) frame
// length including the configured pause.
func (s *Sensor) FrameDuration() time.Duration {
	frame := defaultSyncTicks + 8*MinNibbleTicks + s.cfg.PauseTicks
	return time.Duration(frame) * s.cfg.Tick
}
