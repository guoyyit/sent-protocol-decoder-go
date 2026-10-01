package sent

import "testing"

func TestFaultTransientPersistentRecovery(t *testing.T) {
	tr := NewFaultTracker(3)

	// One erroring frame is only transient.
	r := tr.observe(1, true, false)
	if r == nil || r.Level != FaultTransient {
		t.Fatalf("want transient, got %+v", r)
	}
	if r2 := tr.observe(2, true, false); r2 != nil {
		t.Fatalf("second erroring frame must stay quiet, got %+v", r2)
	}
	// Third consecutive error promotes to persistent.
	r3 := tr.observe(3, true, false)
	if r3 == nil || r3.Level != FaultPersistent || r3.Consecutive != 3 {
		t.Fatalf("want persistent at 3, got %+v", r3)
	}
	if r4 := tr.observe(4, true, false); r4 != nil {
		t.Fatalf("ongoing persistent fault must stay quiet, got %+v", r4)
	}
	// First healthy frame reports recovery.
	r5 := tr.observe(5, false, false)
	if r5 == nil || r5.Level != FaultRecovered {
		t.Fatalf("want recovery, got %+v", r5)
	}
	if r6 := tr.observe(6, false, false); r6 != nil {
		t.Fatalf("stable healthy state must stay quiet, got %+v", r6)
	}
}

func TestFaultThresholdOne(t *testing.T) {
	tr := NewFaultTracker(1)
	r := tr.observe(0, false, true)
	if r == nil || r.Level != FaultPersistent {
		t.Fatalf("threshold 1 should promote immediately, got %+v", r)
	}
}

func TestFaultThroughDecoder(t *testing.T) {
	cfg := testConfig()
	cfg.PersistThreshold = 3
	specs := cleanSpecs(8)
	for i := 2; i < 5; i++ {
		specs[i].ErrB = true
	}
	edges := NewEncoder(cfg, 11).Build(specs, nil)
	events := Decode(cfg, edges)

	var levels []FaultLevel
	var recovered *FaultReport
	for _, ev := range events {
		if ev.Kind == EventSensorFault {
			levels = append(levels, ev.Fault.Level)
			if ev.Fault.Level == FaultRecovered {
				recovered = ev.Fault
			}
		}
	}
	want := []FaultLevel{FaultTransient, FaultPersistent, FaultRecovered}
	if len(levels) != 3 || levels[0] != want[0] || levels[1] != want[1] || levels[2] != want[2] {
		t.Fatalf("fault transitions = %v, want %v", levels, want)
	}
	if recovered == nil {
		t.Fatal("missing recovery report")
	}
}

func TestVariantPolynomialsDiffer(t *testing.T) {
	var n [6]uint8
	for i := range n {
		n[i] = uint8(i + 1)
	}
	c1 := frameCRC(VariantRecommended, 0xA, n)
	c2 := frameCRC(VariantLegacy, 0xA, n)
	c3 := frameCRC(VariantExtended, 0xA, n)
	if c1 == c2 && c2 == c3 {
		t.Fatal("variants must not share identical CRCs for this input")
	}

	// Each variant must decode frames its own encoder produced.
	for _, v := range []Variant{VariantRecommended, VariantLegacy, VariantExtended} {
		cfg := DefaultConfig("s", v)
		specs := cleanSpecs(4)
		edges := NewEncoder(cfg, 2).Build(specs, nil)
		if countReadings(Decode(cfg, edges)) != 4 {
			t.Fatalf("%s: expected 4 readings", v)
		}
	}
}

func TestLegacyCRCExcludesStatus(t *testing.T) {
	// Legacy CRC is computed over the six data nibbles only; changing the
	// status nibble must not change the CRC nibble.
	var n [6]uint8
	for i := range n {
		n[i] = uint8(i*2 + 1)
	}
	c0 := frameCRC(VariantLegacy, 0x0, n)
	c1 := frameCRC(VariantLegacy, 0xF, n)
	if c0 != c1 {
		t.Fatalf("legacy CRC must ignore status: %d vs %d", c0, c1)
	}
	// Recommended CRC includes status, so it must differ.
	r0 := frameCRC(VariantRecommended, 0x0, n)
	r1 := frameCRC(VariantRecommended, 0xF, n)
	if r0 == r1 {
		t.Fatal("recommended CRC must cover the status nibble")
	}
}

func TestVariantValidity(t *testing.T) {
	if !VariantRecommended.valid() || !VariantLegacy.valid() || !VariantExtended.valid() {
		t.Fatal("known variants must be valid")
	}
	if Variant("bogus").valid() {
		t.Fatal("unknown variant must be invalid")
	}
}
