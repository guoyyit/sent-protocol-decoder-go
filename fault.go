package sent

// FaultLevel distinguishes transient sensor errors from persistent faults.
type FaultLevel int

const (
	FaultTransient FaultLevel = iota
	FaultPersistent
	FaultRecovered
)

// FaultReport describes a sensor error-status transition.
type FaultReport struct {
	Level       FaultLevel
	ErrA        bool
	ErrB        bool
	Consecutive int
	Time        int64
}

// FaultTracker debounces the per-frame sensor error flags.
type FaultTracker struct {
	threshold  int
	consec     int
	persistent bool
}

// NewFaultTracker creates a tracker with the persistence threshold.
func NewFaultTracker(threshold int) *FaultTracker {
	if threshold < 1 {
		threshold = 1
	}
	return &FaultTracker{threshold: threshold}
}

// observe consumes the sensor error flags carried by one accepted frame and
// returns a report only when the fault state transitions:
//
//   - first erroring frame after a healthy run  -> FaultTransient,
//   - threshold consecutive erroring frames      -> FaultPersistent,
//   - first healthy frame after an error run     -> FaultRecovered.
//
// Frames that keep the state stable produce no report.
func (t *FaultTracker) observe(time int64, errA, errB bool) *FaultReport {
	err := errA || errB
	if !err {
		if t.consec == 0 {
			return nil
		}
		rep := &FaultReport{
			Level:       FaultRecovered,
			Consecutive: t.consec,
			Time:        time,
		}
		t.consec = 0
		t.persistent = false
		return rep
	}

	t.consec++
	if t.consec >= t.threshold && !t.persistent {
		t.persistent = true
		return &FaultReport{
			Level:       FaultPersistent,
			ErrA:        errA,
			ErrB:        errB,
			Consecutive: t.consec,
			Time:        time,
		}
	}
	if t.consec == 1 {
		return &FaultReport{
			Level:       FaultTransient,
			ErrA:        errA,
			ErrB:        errB,
			Consecutive: 1,
			Time:        time,
		}
	}
	return nil
}
