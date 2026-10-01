package sent

// CRC polynomials as the low 4 coefficients (x^4 is implicit).
const (
	crcPoly3 = 0x3 // x^4 + x   + 1
	crcPolyD = 0xD // x^4 + x^3 + x^2 + 1
)

// CRC4 computes the nibble-wise 4-bit checksum for the given variant.
//
// Each input nibble is XORed into the running 4-bit remainder and then
// advanced by four bit steps. The remainder starts at 0. Appending the
// returned nibble to the inputs and recomputing yields 0, so a decoder may
// either compare the returned value with the received CRC nibble or verify
// that the full sequence reduces to 0.
func CRC4(variant Variant, nibbles []uint8) uint8 {
	poly := uint8(crcPoly3)
	if variant == VariantB {
		poly = crcPolyD
	}

	crc := uint8(0)
	for _, nibble := range nibbles {
		crc ^= nibble & 0x0F
		for bit := 0; bit < 4; bit++ {
			if crc&0x08 != 0 {
				crc = (crc << 1) ^ poly
			} else {
				crc <<= 1
			}
			crc &= 0x0F
		}
	}
	return crc
}

// verifyCRC reports whether the trailing nibble is a valid checksum over the
// preceding nibbles under the given variant.
func verifyCRC(variant Variant, nibbles []uint8) bool {
	if len(nibbles) < 1 {
		return false
	}
	return CRC4(variant, nibbles[:len(nibbles)-1]) == nibbles[len(nibbles)-1]
}
