package sent

// crc4 computes the 4-bit CRC of nibbles with the given polynomial and init
// value (MSB-first, one nibble per step).
func crc4(nibbles []uint8, poly, init uint8) uint8 {
	crc := init & 0x0F
	for _, n := range nibbles {
		crc ^= n & 0x0F
		for i := 0; i < 4; i++ {
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

// frameCRC returns the expected CRC nibble for the given variant.
func frameCRC(v Variant, status uint8, data [6]uint8) uint8 {
	switch v {
	case VariantRecommended:
		in := make([]uint8, 0, 7)
		in = append(in, status&0x0F)
		in = append(in, data[:]...)
		return crc4(in, 0x13, 5)
	case VariantLegacy:
		return crc4(data[:], 0x1D, 5)
	case VariantExtended:
		in := make([]uint8, 0, 7)
		in = append(in, status&0x0F)
		in = append(in, data[:]...)
		return crc4(in, 0x07, 0)
	default:
		return 0
	}
}

// slowCRC returns the 4-bit CRC over assembled slow-channel bits.
func slowCRC(v Variant, bits []byte) uint8 {
	spec := slowLayout(v)
	nibbles := make([]uint8, 0, len(bits)/4)
	for i := 0; i+4 <= len(bits); i += 4 {
		var n uint8
		for j := 0; j < 4; j++ {
			n = (n << 1) | (bits[i+j] & 1)
		}
		nibbles = append(nibbles, n)
	}
	return crc4(nibbles, spec.poly, spec.init)
}

// slowSpec describes the slow-channel message layout of a variant.
type slowSpec struct {
	tag      byte
	bits     int
	dataBits int
	frames   int
	poly     uint8
	init     uint8
}

// slowLayout returns the slow-channel specification for a variant.
func slowLayout(v Variant) slowSpec {
	switch v {
	case VariantRecommended:
		// 2 tag + 2 id + 8 data + 4 crc = 16 bits over 8 frames.
		return slowSpec{tag: 0b00, bits: 16, dataBits: 8, frames: 8, poly: 0x13, init: 5}
	case VariantLegacy:
		// 2 tag + 2 id + 4 data + 4 crc = 12 bits over 6 frames.
		return slowSpec{tag: 0b01, bits: 12, dataBits: 4, frames: 6, poly: 0x1D, init: 5}
	case VariantExtended:
		// 2 tag + 2 id + 16 data + 4 crc = 24 bits over 12 frames.
		return slowSpec{tag: 0b10, bits: 24, dataBits: 16, frames: 12, poly: 0x07, init: 0}
	default:
		return slowSpec{}
	}
}
