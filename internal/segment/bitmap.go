package segment

// Segment membership lives in Redis as a plain bitmap: bit N set = contact N is a member.
// These helpers build and compare bitmaps in Redis bit order (offset 0 is the most significant
// bit of byte 0), so a bitmap built here can be written with SET and read with GETBIT.

// Build returns the bitmap for ids. An empty set is nil.
func Build(ids []uint32) []byte {
	if len(ids) == 0 {
		return nil
	}
	var max uint32
	for _, id := range ids {
		if id > max {
			max = id
		}
	}
	b := make([]byte, max/8+1)
	for _, id := range ids {
		b[id/8] |= 0x80 >> (id % 8)
	}
	return b
}

// Has reports whether bit id is set.
func Has(b []byte, id uint32) bool {
	i := int(id / 8)
	return i < len(b) && b[i]&(0x80>>(id%8)) != 0
}

// Members lists the set bits in ascending order.
func Members(b []byte) []uint32 {
	var out []uint32
	for i, x := range b {
		for bit := uint32(0); x != 0 && bit < 8; bit++ {
			if x&(0x80>>bit) != 0 {
				out = append(out, uint32(i)*8+bit)
			}
		}
	}
	return out
}

// Diff returns the ids set in cur but not old (entered) and set in old but not cur (exited).
func Diff(old, cur []byte) (entered, exited []uint32) {
	n := max(len(old), len(cur))
	for i := 0; i < n; i++ {
		var o, c byte
		if i < len(old) {
			o = old[i]
		}
		if i < len(cur) {
			c = cur[i]
		}
		changed := o ^ c
		for bit := uint32(0); changed != 0 && bit < 8; bit++ {
			mask := byte(0x80 >> bit)
			if changed&mask == 0 {
				continue
			}
			id := uint32(i)*8 + bit
			if c&mask != 0 {
				entered = append(entered, id)
			} else {
				exited = append(exited, id)
			}
		}
	}
	return entered, exited
}
