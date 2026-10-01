package sent

import "testing"

func TestCRC4Variant(t *testing.T) {
	nibbles := []uint8{0x1, 0x9, 0xA, 0xB, 0xC, 0xD, 0xE}

	a := CRC4(VariantA, nibbles)
	b := CRC4(VariantB, nibbles)
	if a == b {
		t.Fatalf("polynomials must produce different CRC, got %x", a)
	}

	if got := CRC4(VariantA, append(append([]uint8{}, nibbles...), a)); got != 0 {
		t.Fatalf("appending CRC must reduce to 0, got %x", got)
	}
	if !verifyCRC(VariantA, append(append([]uint8{}, nibbles...), a)) {
		t.Fatalf("verifyCRC rejected valid VariantA frame")
	}
	if verifyCRC(VariantB, append(append([]uint8{}, nibbles...), a)) {
		t.Fatalf("VariantA CRC must not validate under VariantB")
	}
}

func TestCRC4Determinism(t *testing.T) {
	nibbles := []uint8{0x3, 0x0, 0x1, 0x2, 0x4, 0x8, 0xF}
	first := CRC4(VariantA, nibbles)
	for i := 0; i < 10; i++ {
		if CRC4(VariantA, nibbles) != first {
			t.Fatal("CRC4 is not deterministic")
		}
	}
}
