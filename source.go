package sent

// OpType selects an injection performed while building an edge stream.
type OpType int

const (
	OpDropFrame OpType = iota
	OpCorruptCRC
	OpIllegalNibble
	OpPulseWidth
	OpMissingSync
	OpGlitch
	OpDuplicateFrame
	OpSwapFrames
)

// InjectOp mutates the frame at FrameIndex while the encoder builds a stream.
type InjectOp struct {
	Type       OpType `json:"type"`
	FrameIndex int    `json:"frame_index"`
	// With selects the data nibble (0..5) for nibble-level injections.
	With int `json:"with"`
}

// FrameSpec is one sensor frame to transmit.
type FrameSpec struct {
	SlowBits uint8  `json:"slow_bits"`
	RawA     uint16 `json:"raw_a"`
	RawB     uint16 `json:"raw_b"`
	ErrA     bool   `json:"err_a"`
	ErrB     bool   `json:"err_b"`
}

// Encoder turns frame specifications into a jittered falling-edge time series.
type Encoder struct {
	cfg         ChannelConfig
	seed        int64
	jitterTicks float64
}

// NewEncoder creates an encoder with a fixed RNG seed for reproducibility.
func NewEncoder(cfg ChannelConfig, seed int64) *Encoder {
	return &Encoder{cfg: cfg, seed: seed, jitterTicks: 0.20}
}

// WithJitter sets the per-pulse uniform jitter magnitude in ticks.
func (e *Encoder) WithJitter(ticks float64) *Encoder {
	e.jitterTicks = ticks
	return e
}

// Build generates the edge stream for specs, applying injection operations.
func (e *Encoder) Build(specs []FrameSpec, ops []InjectOp) []Edge {
	opAt := map[int]InjectOp{}
	for _, op := range ops {
		opAt[op.FrameIndex] = op
	}

	type frameEntry struct {
		index int
		ticks []float64
	}

	buildOne := func(idx int, spec FrameSpec) frameEntry {
		seq := uint8(idx & 0x03)
		status := (seq & 0x03) | ((spec.SlowBits & 0x03) << 2)

		nibbles := packNibbles(spec.RawA, spec.RawB, spec.ErrA, spec.ErrB)
		crc := frameCRC(e.cfg.Variant, status, nibbles)

		widths := make([]float64, 0, 9)
		widths = append(widths, 56) // sync pulse
		widths = append(widths, float64(12+(status&0x0F)))
		for _, n := range nibbles {
			widths = append(widths, float64(12+int(n)))
		}
		widths = append(widths, float64(12+int(crc)))

		if op, hit := opAt[idx]; hit {
			switch op.Type {
			case OpMissingSync:
				widths[0] = 20
			case OpIllegalNibble:
				// 28..31 tick pulses are reserved illegal nibble values.
				widths[1+op.With] = 29
			case OpPulseWidth:
				widths[1+op.With] = 47
			case OpGlitch:
				widths[1+op.With] = 70
			case OpCorruptCRC:
				widths[8] = 12 + float64(int((crc+1)&0x0F))
			}
		}
		if e.cfg.PauseTicks > 0 {
			widths = append(widths, float64(e.cfg.PauseTicks))
		}
		return frameEntry{index: idx, ticks: widths}
	}

	entries := make([]frameEntry, 0, len(specs))
	for i, spec := range specs {
		if op, drop := opAt[i]; drop && op.Type == OpDropFrame {
			continue
		}
		entries = append(entries, buildOne(i, spec))
	}

	for i := 0; i < len(entries); i++ {
		op, hit := opAt[entries[i].index]
		if !hit {
			continue
		}
		switch op.Type {
		case OpDuplicateFrame:
			entries = append(entries[:i+1], entries[i:]...)
			entries[i+1] = entries[i]
			i++
		case OpSwapFrames:
			if i+1 < len(entries) {
				entries[i], entries[i+1] = entries[i+1], entries[i]
			}
			i++
		}
	}

	rng := newRng(e.seed)
	var acc float64
	// Reference falling edge at t=0; every following edge delimits one pulse
	// interval, starting with the first frame's 56-tick sync pulse.
	edges := []Edge{{T: 0}}
	for _, fr := range entries {
		// One shared clock-rate offset per frame, modeling sensor tick-rate
		// variation; the receiver absorbs it by calibrating to the sync pulse.
		rate := 1 + (rng.float64()*2-1)*e.jitterTicks/56
		for pi, w := range fr.ticks {
			phase := 0.0
			if pi > 0 {
				// The sync pulse is the timing reference and stays exact;
				// data pulses carry independent phase jitter bounded to half
				// the configured magnitude so it stays inside one nibble cell.
				phase = (rng.float64()*2 - 1) * e.jitterTicks * 0.5
			} else {
				rng.float64() // keep RNG consumption order stable
			}
			acc += w*rate + phase
			edges = append(edges, Edge{T: int64(acc*e.cfg.TickUS + 0.5)})
		}
	}
	return edges
}

// packNibbles splits two 11-bit fast channel values (plus sensor error flags
// in bit 11) into six 4-bit nibbles: d0..d2 channel A, d3..d5 channel B.
func packNibbles(rawA, rawB uint16, errA, errB bool) [6]uint8 {
	vA := rawA & 0x07FF
	vB := rawB & 0x07FF
	if errA {
		vA |= 0x0800
	}
	if errB {
		vB |= 0x0800
	}
	return [6]uint8{
		uint8((vA >> 8) & 0x0F),
		uint8((vA >> 4) & 0x0F),
		uint8(vA & 0x0F),
		uint8((vB >> 8) & 0x0F),
		uint8((vB >> 4) & 0x0F),
		uint8(vB & 0x0F),
	}
}

// xorshift64* RNG; pure and seed reproducible with no global state.
type rng struct {
	state uint64
}

func newRng(seed int64) *rng {
	s := uint64(seed)
	if s == 0 {
		s = 0x9E3779B97F4A7C15
	}
	return &rng{state: s}
}

func (r *rng) uint64v() uint64 {
	x := r.state
	x ^= x >> 12
	x ^= x << 25
	x ^= x >> 27
	r.state = x
	return x * 0x2545F4914F6CDD1D
}

func (r *rng) float64() float64 {
	return float64(r.uint64v()>>11) / float64(1<<53)
}

// Pipe is an in-memory edge channel used as a local loopback signal source.
type Pipe struct {
	edges chan Edge
}

// NewPipe creates a buffered in-memory pipe.
func NewPipe(buffer int) *Pipe {
	if buffer < 1 {
		buffer = 1
	}
	return &Pipe{edges: make(chan Edge, buffer)}
}

// Write enqueues edges for loopback delivery.
func (p *Pipe) Write(edges []Edge) {
	for _, e := range edges {
		p.edges <- e
	}
}

// Edges returns the receive side of the loopback.
func (p *Pipe) Edges() <-chan Edge {
	return p.edges
}

// Close terminates the loopback.
func (p *Pipe) Close() {
	close(p.edges)
}
