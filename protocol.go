package sent

import "time"

// Variant selects the frame CRC polynomial and slow-channel message format.
type Variant int

const (
	// VariantA uses CRC polynomial 0x3 and enhanced 16-bit slow messages.
	VariantA Variant = iota
	// VariantB uses CRC polynomial 0xD and short 8-bit slow messages.
	VariantB
)

// SlowFormat selects how the two slow-channel bits per frame are assembled.
type SlowFormat int

const (
	// SlowShort is a 6-frame / 12-bit message (4-bit ID + 8-bit data).
	SlowShort SlowFormat = iota
	// SlowEnhanced is a 16-frame / 32-bit message (preamble + CRC + ID + data).
	SlowEnhanced
)

// EdgeKind identifies a signal edge.
type EdgeKind int

const (
	EdgeFalling EdgeKind = iota
	EdgeRising
)

// Sample is one captured signal edge with a stream-relative timestamp.
//
// FrameSeq is out-of-band capture metadata identifying the frame an edge was
// emitted by (1-based). Edges not attributable to a frame (glitches) carry 0.
type Sample struct {
	At       time.Duration
	Edge     EdgeKind
	FrameSeq int
}

// Pulse is the width between two falling edges, tagged like its opening edge.
type Pulse struct {
	At       time.Duration
	Width    time.Duration
	FrameSeq int
}

// FrameConfig configures frame timing, validation and format selection.
type FrameConfig struct {
	// Tick is the nominal unit-tick duration of the sensor.
	Tick time.Duration
	// Tolerance is the allowed per-pulse relative deviation (e.g. 0.10).
	Tolerance float64
	// SyncTicks is the sync pulse width in ticks (SENT: 56).
	SyncTicks int
	// DataNibbles is the number of data nibbles per frame (SENT: 6).
	DataNibbles int
	// Variant selects CRC polynomial and slow format.
	Variant Variant
}

// RawFrame is a reconstructed, not yet fully validated fast-channel frame.
type RawFrame struct {
	FrameSeq int
	StartAt  time.Duration
	Status   uint8
	Data     [6]uint8
	CRC      uint8
}

// ErrorKind classifies a frame-level failure.
type ErrorKind int

const (
	// ErrCRC: checksum nibble does not match the configured polynomial.
	ErrCRC ErrorKind = iota
	// ErrPulseOutOfTolerance: pulse width deviates beyond Tolerance.
	ErrPulseOutOfTolerance
	// ErrSyncMissing: no valid sync pulse found where a frame must start.
	ErrSyncMissing
	// ErrIllegalNibble: a data/status/CRC pulse is outside the legal range.
	ErrIllegalNibble
	// ErrVariantMismatch: frame validates under a different declared variant.
	ErrVariantMismatch
)

// FrameError is a classified frame-level or pulse-level failure report.
type FrameError struct {
	Kind     ErrorKind
	At       time.Duration
	FrameSeq int
	Detail   string
}

// SlowDropReason classifies why a slow-channel assembly was discarded.
type SlowDropReason int

const (
	// SlowDropLoss: a frame was expected by sequence but never arrived.
	SlowDropLoss SlowDropReason = iota
	// SlowDropDuplicate: the same frame sequence number arrived again.
	SlowDropDuplicate
	// SlowDropOutOfOrder: a frame older than the last accepted arrived.
	SlowDropOutOfOrder
	// SlowResetOnVariant: assembly reset after a variant-mismatch rejection.
	SlowResetOnVariant
	// SlowPreambleInvalid: enhanced preamble nibble was not 0001.
	SlowPreambleInvalid
	// SlowCRCInvalid: enhanced message slow-CRC verification failed.
	SlowCRCInvalid
)

// SlowDrop reports a discarded slow-channel assembly.
type SlowDrop struct {
	Reason   SlowDropReason
	At       time.Duration
	FrameSeq int
}

// SlowMessage is a fully assembled slow-channel message.
type SlowMessage struct {
	ID         uint8
	Data       uint16
	ReceivedAt time.Duration
	FrameSeq   int
}

// SensorStatus holds the per-frame sensor error indication.
type SensorStatus struct {
	Code       uint8
	Transient  bool
	Persistent bool
}

// FaultState is the debounced latching fault state of a channel.
type FaultState int

const (
	FaultOK FaultState = iota
	FaultTransient
	FaultPersistent
)

// FaultStatus is an observable fault-state snapshot.
type FaultStatus struct {
	State       FaultState
	Code        uint8
	Since       time.Duration
	Consecutive int
}

// ChannelReading is one timestamped, decoded fast-channel output.
type ChannelReading struct {
	FrameSeq int
	At       time.Duration
	Status   uint8
	Raw      [6]uint8
	Fast     uint32
	Physical map[string]float64
	Sensor   SensorStatus
}

// EventKind tags events emitted by a receive pipeline.
type EventKind int

const (
	EvtReading EventKind = iota
	EvtSlowMessage
	EvtFrameError
	EvtSlowDrop
	EvtVariantMismatch
	EvtFaultChange
)

// Event is a single output item of a channel or receiver.
type Event struct {
	Kind     EventKind
	Channel  string
	Reading  *ChannelReading
	Slow     *SlowMessage
	FrameErr *FrameError
	SlowDrop *SlowDrop
	Fault    *FaultStatus
}
