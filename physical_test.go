package sent

import "testing"

func TestFastValueAndPhysical(t *testing.T) {
	raw := [6]uint8{0x1, 0x2, 0x3, 0x4, 0x5, 0x6}
	if got, want := FastValue(raw), uint32(0x123456); got != want {
		t.Fatalf("FastValue = %06x, want %06x", got, want)
	}

	cfg := PhysicalConfig{Signals: []SignalDef{
		{Name: "pressure", BitOffset: 0, BitLength: 8, Scale: 0.5, Offset: 10},
		{Name: "temp", BitOffset: 8, BitLength: 8, Signed: true, Scale: 1},
		{Name: "high", BitOffset: 16, BitLength: 8, Scale: 1},
	}}

	values := cfg.Decode(raw)
	// bits 0..7 = 0x56 = 86 -> 86*0.5 + 10 = 53
	if values["pressure"] != 53 {
		t.Fatalf("pressure = %v, want 53", values["pressure"])
	}
	// bits 8..15 = byte 0x34 = 52, positive signed
	if values["temp"] != 52 {
		t.Fatalf("temp = %v, want 52", values["temp"])
	}
	// bits 16..23 = byte 0x12 = 18
	if values["high"] != 18 {
		t.Fatalf("high = %v, want 18", values["high"])
	}
}

func TestPhysicalSignedNegative(t *testing.T) {
	raw := [6]uint8{0, 0, 0, 0xF, 0xF, 0x0}
	cfg := PhysicalConfig{Signals: []SignalDef{
		{Name: "s4", BitOffset: 4, BitLength: 4, Signed: true, Scale: 1},
	}}
	values := cfg.Decode(raw)
	// nibble F at offset 4 -> 4-bit signed -1
	if values["s4"] != -1 {
		t.Fatalf("s4 = %v, want -1", values["s4"])
	}
}
