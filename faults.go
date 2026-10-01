package sent

import "time"

// Per-frame sensor error codes carried in the status nibble.
const (
	SensorOK        uint8 = 0 // status bits 00: normal
	SensorTransient uint8 = 1 // status bits 01: self-healing transient error
	SensorHard      uint8 = 2 // status bits 10: serious pending error
	SensorPersist   uint8 = 3 // status bits 11: persistent failure
)

// FaultTrackerConfig configures latching/recovery debouncing.
type FaultTrackerConfig struct {
	// PersistThreshold consecutive hard-error codes latch a persistent fault.
	PersistThreshold int
	// RecoverThreshold consecutive normal codes clear a latched fault.
	RecoverThreshold int
}

// FaultTracker debounces per-frame sensor error codes into fault states.
type FaultTracker struct {
	cfg         FaultTrackerConfig
	state       FaultState
	code        uint8
	since       time.Duration
	consecutive int
}

// NewFaultTracker creates a tracker with the given thresholds.
func NewFaultTracker(cfg FaultTrackerConfig) *FaultTracker {
	if cfg.PersistThreshold <= 0 {
		cfg.PersistThreshold = 3
	}
	if cfg.RecoverThreshold <= 0 {
		cfg.RecoverThreshold = 3
	}
	return &FaultTracker{cfg: cfg}
}

// Observe consumes one frame's error code and returns the current status.
//
// The boolean is true only when the fault state changed on this frame;
// transient codes are observable per frame through ChannelReading.Sensor.
func (t *FaultTracker) Observe(code uint8, at time.Duration) (FaultStatus, bool) {
	prev := t.state

	switch code {
	case SensorOK:
		if t.state == FaultTransient {
			// A transient, self-healing indication clears on the first
			// healthy frame so recovery is promptly observable.
			t.state = FaultOK
			t.code = SensorOK
			t.since = at
			t.consecutive = 0
			break
		}
		t.consecutive++
		if t.state != FaultOK && t.consecutive >= t.cfg.RecoverThreshold {
			t.state = FaultOK
			t.code = SensorOK
			t.since = at
			t.consecutive = 0
		} else if t.state == FaultOK {
			t.consecutive = 0
		}
	case SensorTransient:
		// A transient indication never latches, is observable per frame,
		// and resets any run of hard-error or recovery counting.
		if t.state != FaultTransient {
			t.state = FaultTransient
			t.code = code
			t.since = at
		}
		t.consecutive = 0
	case SensorHard, SensorPersist:
		// A persistent indication latches immediately; a hard indication
		// latches only after PersistThreshold consecutive hard frames.
		if code == SensorPersist {
			t.state = FaultPersistent
			t.code = code
			t.since = at
			t.consecutive = 0
			break
		}
		if t.state == FaultPersistent {
			t.consecutive = 0
			break
		}
		// Leaving a transient/other indication starts a fresh hard run.
		if t.code != SensorHard {
			t.state = FaultTransient
			t.code = code
			t.since = at
			t.consecutive = 0
		}
		t.consecutive++
		if t.consecutive >= t.cfg.PersistThreshold {
			t.state = FaultPersistent
			t.code = code
			t.since = at
			t.consecutive = 0
		}
	}

	status := FaultStatus{
		State:       t.state,
		Code:        t.code,
		Since:       t.since,
		Consecutive: t.consecutive,
	}
	return status, t.state != prev
}
