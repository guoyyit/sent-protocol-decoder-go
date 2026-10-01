package sent

// SignalDef describes one physical signal packed into the 24-bit fast value.
type SignalDef struct {
	// Name is the output map key.
	Name string
	// BitOffset is the offset of the least-significant signal bit in the
	// 24-bit fast word (0 = bit 0 / LSB).
	BitOffset uint
	// BitLength is the number of bits used by the signal.
	BitLength uint
	// Signed selects two's-complement interpretation.
	Signed bool
	// Scale and Offset convert raw bits: physical = raw*Scale + Offset.
	Scale  float64
	Offset float64
}

// PhysicalConfig maps raw fast-channel bits to named physical signals.
type PhysicalConfig struct {
	Signals []SignalDef
}

// FastValue packs six nibbles into one 24-bit word, preserving frame order:
// data[0] is the most significant nibble.
func FastValue(raw [6]uint8) uint32 {
	return uint32(raw[0])<<20 |
		uint32(raw[1])<<16 |
		uint32(raw[2])<<12 |
		uint32(raw[3])<<8 |
		uint32(raw[4])<<4 |
		uint32(raw[5])
}

// Decode extracts physical signal values from six raw nibbles.
func (c *PhysicalConfig) Decode(raw [6]uint8) map[string]float64 {
	if len(c.Signals) == 0 {
		return nil
	}
	word := FastValue(raw)
	out := make(map[string]float64, len(c.Signals))
	for _, sig := range c.Signals {
		if sig.BitLength == 0 || sig.BitLength > 24 {
			continue
		}
		mask := uint32(1)<<sig.BitLength - 1
		bits := (word >> sig.BitOffset) & mask
		value := float64(bits)
		if sig.Signed {
			if bits&(uint32(1)<<(sig.BitLength-1)) != 0 {
				value -= float64(uint32(1) << sig.BitLength)
			}
		}
		out[sig.Name] = value*sig.Scale + sig.Offset
	}
	return out
}
