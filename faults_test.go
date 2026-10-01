package sent

import "testing"

func TestFaultTransientDoesNotLatch(t *testing.T) {
	ft := NewFaultTracker(FaultTrackerConfig{PersistThreshold: 3, RecoverThreshold: 2})

	status, changed := ft.Observe(SensorTransient, 0)
	if !changed || status.State != FaultTransient {
		t.Fatalf("first transient must enter FaultTransient, got %+v changed=%v", status, changed)
	}
	// Transient again does not escalate.
	if _, changed := ft.Observe(SensorTransient, 1); changed {
		t.Fatalf("repeated transient must not change state")
	}
	// A single normal frame clears a transient observation.
	status, changed = ft.Observe(SensorOK, 2)
	if !changed || status.State != FaultOK {
		t.Fatalf("normal frame after transient must recover, got %+v", status)
	}
}

func TestFaultHardLatchesAfterThreshold(t *testing.T) {
	ft := NewFaultTracker(FaultTrackerConfig{PersistThreshold: 3, RecoverThreshold: 2})

	feed := func(code uint8, want FaultState, wantChange bool) FaultStatus {
		status, changed := ft.Observe(code, 0)
		if changed != wantChange {
			t.Fatalf("code %d: changed=%v want %v (state=%d)", code, changed, wantChange, status.State)
		}
		if status.State != want {
			t.Fatalf("code %d: state=%d want %d", code, status.State, want)
		}
		return status
	}

	feed(SensorHard, FaultTransient, true)  // 1st hard: transient pending
	feed(SensorHard, FaultTransient, false) // 2nd
	feed(SensorHard, FaultPersistent, true) // 3rd latches

	// Recovery requires a run of normal frames.
	feed(SensorOK, FaultPersistent, false) // 1st healthy: still latched
	feed(SensorOK, FaultOK, true)          // 2nd healthy: threshold reached
}

func TestFaultPersistLatchesImmediately(t *testing.T) {
	ft := NewFaultTracker(FaultTrackerConfig{PersistThreshold: 3, RecoverThreshold: 2})
	status, changed := ft.Observe(SensorPersist, 0)
	if !changed || status.State != FaultPersistent {
		t.Fatalf("persistent indication must latch immediately, got %+v", status)
	}
}

func TestFaultTransientInterruptedHardRun(t *testing.T) {
	ft := NewFaultTracker(FaultTrackerConfig{PersistThreshold: 3, RecoverThreshold: 2})
	ft.Observe(SensorHard, 0)
	ft.Observe(SensorHard, 0)
	// A transient frame interrupts the hard-error run, so persistence must
	// not be reached on the next hard frame.
	ft.Observe(SensorTransient, 0)
	status, _ := ft.Observe(SensorHard, 0)
	if status.State == FaultPersistent {
		t.Fatalf("interrupted hard run must not latch persistent")
	}
}
