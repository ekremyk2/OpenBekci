package files

import "encoding/binary"

// exifOrientation returns the EXIF Orientation (1..8) of a JPEG, or 1 when it
// has none. Re-encoding drops all EXIF, so the rotation it describes is
// applied to the pixels first or the photo would turn sideways.
func exifOrientation(b []byte) int {
	if len(b) < 4 || b[0] != 0xFF || b[1] != 0xD8 {
		return 1
	}
	i := 2
	for i+4 <= len(b) {
		if b[i] != 0xFF {
			return 1
		}
		marker := b[i+1]
		if marker == 0xFF { // fill byte
			i++
			continue
		}
		if marker == 0xDA || marker == 0xD9 { // start of scan, end of image: no EXIF follows
			return 1
		}
		if marker == 0x01 || (marker >= 0xD0 && marker <= 0xD7) { // standalone markers
			i += 2
			continue
		}
		segLen := int(binary.BigEndian.Uint16(b[i+2:]))
		if segLen < 2 || i+2+segLen > len(b) {
			return 1
		}
		if marker == 0xE1 {
			if o := tiffOrientation(b[i+4 : i+2+segLen]); o != 0 {
				return o
			}
		}
		i += 2 + segLen
	}
	return 1
}

// tiffOrientation reads the Orientation tag from an APP1 payload that starts
// with "Exif\0\0"; 0 means this segment has none.
func tiffOrientation(p []byte) int {
	if len(p) < 14 || string(p[:6]) != "Exif\x00\x00" {
		return 0
	}
	t := p[6:]
	var bo binary.ByteOrder
	switch string(t[:2]) {
	case "II":
		bo = binary.LittleEndian
	case "MM":
		bo = binary.BigEndian
	default:
		return 0
	}
	if bo.Uint16(t[2:]) != 42 {
		return 0
	}
	off := int(bo.Uint32(t[4:]))
	if off < 8 || off+2 > len(t) {
		return 0
	}
	n := int(bo.Uint16(t[off:]))
	for e := 0; e < n; e++ {
		at := off + 2 + e*12
		if at+12 > len(t) {
			return 0
		}
		if bo.Uint16(t[at:]) == 0x0112 { // Orientation, type SHORT
			if v := int(bo.Uint16(t[at+8:])); v >= 1 && v <= 8 {
				return v
			}
			return 0
		}
	}
	return 0
}
