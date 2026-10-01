// Package sent implements a hardware-free SENT (Single Edge Nibble
// Transmission, SAE J2716 style) fast/slow channel receiver and diagnostic
// engine. Pulse edges are supplied as an in-memory time series; no real
// hardware or external services are required.
package sent

// Edge is one observed falling signal edge. Its timestamp T is expressed in
// microseconds relative to the beginning of the stream. SENT data is encoded
// in the distance between consecutive falling edges.
type Edge struct {
	T int64 `json:"t"`
}

// Variant selects the checksum polynomial/coverage and the slow-channel
// message layout of a sensor stream.
type Variant string

const (
	// VariantRecommended: CRC poly x^4+x+1 (0x13), init 5, checksum covers
	// status + six data nibbles; slow messages are 16 bits (8 frames).
	VariantRecommended Variant = "recommended"
	// VariantLegacy: CRC poly x^4+x^3+x^2+1 (0x1D), init 5, checksum covers
	// only the six data nibbles; slow messages are 12 bits (6 frames).
	VariantLegacy Variant = "legacy"
	// VariantExtended: CRC poly x^4+x^2+x+1 (0x07), init 0, checksum covers
	// status + six data nibbles; slow messages are 24 bits (12 frames).
	VariantExtended Variant = "extended"
)

// FastScale maps a normalized 11-bit fast channel measurement to a physical
// value: Phys = Min + Raw/2047 * (Max-Min).
type FastScale struct {
	Min  float64 `json:"min"`
	Max  float64 `json:"max"`
	Unit string  `json:"unit"`
}

// ChannelConfig is the declared format and timing of one sensor channel.
type ChannelConfig struct {
	ID         string  `json:"id"`
	Variant    Variant `json:"variant"`
	TickUS     float64 `json:"tick_us"`
	SyncTol    float64 `json:"sync_tol"`
	WidthTol   float64 `json:"width_tol"`
	PauseTicks int     `json:"pause_ticks"`

	// PersistThreshold is the number of consecutive frames carrying a sensor
	// error flag before a transient error is promoted to a persistent fault.
	PersistThreshold int `json:"persist_threshold"`

	ScaleA FastScale `json:"scale_a"`
	ScaleB FastScale `json:"scale_b"`
}

// Frame is one reassembled, checksum-valid fast-channel frame.
type Frame struct {
	ChannelID string
	Index     int64
	StartTime int64
	TickUS    float64
	Status    uint8
	Seq       uint8
	SlowBits  uint8
	Nibbles   [6]uint8
	CRC       uint8
}

// Reading is the decoded payload of one frame.
type Reading struct {
	Frame *Frame
	RawA  uint16
	RawB  uint16
	ErrA  bool
	ErrB  bool
	PhysA float64
	PhysB float64
	UnitA string
	UnitB string
}

// EventKind enumerates everything a decoder can emit, in stream order.
type EventKind int

const (
	EventReading EventKind = iota
	EventFrameError
	EventSlowMessage
	EventSlowDiscard
	EventSensorFault
)

// FrameErrorClass classifies frame/timing level failures.
type FrameErrorClass int

const (
	FrameErrSyncMissing FrameErrorClass = iota
	FrameErrPulseWidth
	FrameErrIllegalNibble
	FrameErrCRC
	FrameErrNoiseGlitch
)

// FrameError describes one rejected frame or illegal pulse.
type FrameError struct {
	Class FrameErrorClass
	Time  int64
	// Detail holds the offending measured width in ticks (0 when not applicable).
	WidthTicks float64
}

// Event is a single decoder output record. Only the field matching Kind is set.
type Event struct {
	ChannelID string
	Time      int64
	Kind      EventKind

	Reading  *Reading     `json:",omitempty"`
	FrameErr *FrameError  `json:",omitempty"`
	Slow     *SlowMessage `json:",omitempty"`
	Discard  *SlowDiscard `json:",omitempty"`
	Fault    *FaultReport `json:",omitempty"`
}

// DefaultConfig returns a usable configuration for the given channel using
// the recommended variant: 3 us unit tick, 2% sync tolerance, 0.6-tick
// per-pulse tolerance and a 3-frame persistence threshold.
func DefaultConfig(id string, variant Variant) ChannelConfig {
	return ChannelConfig{
		ID:               id,
		Variant:          variant,
		TickUS:           3.0,
		SyncTol:          0.02,
		WidthTol:         0.6,
		PersistThreshold: 3,
		ScaleA:           FastScale{Min: 0, Max: 100, Unit: "kPa"},
		ScaleB:           FastScale{Min: -40, Max: 150, Unit: "C"},
	}
}

// valid reports whether the variant is one the decoder understands.
func (v Variant) valid() bool {
	switch v {
	case VariantRecommended, VariantLegacy, VariantExtended:
		return true
	}
	return false
}
