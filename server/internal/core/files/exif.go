package files

import (
	"bytes"
	"encoding/binary"
	"io"
)

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

// maxExifChunk bounds the metadata chunk read from a PNG or WebP.
const maxExifChunk = 1 << 20

// imageOrientation returns the EXIF orientation (1..8) of the image in r, for
// the formats that carry one: JPEG (APP1), PNG (eXIf chunk) and WebP (EXIF
// chunk). It reads only chunk headers and the metadata chunk, never the pixels.
func imageOrientation(r io.ReaderAt, size int64, format string) int {
	switch format {
	case "jpeg":
		head := make([]byte, min(size, maxExifChunk))
		n, _ := r.ReadAt(head, 0)
		return exifOrientation(head[:n])
	case "png":
		return chunkOrientation(r, size, 8, false)
	case "webp":
		return chunkOrientation(r, size, 12, true)
	}
	return 1
}

// chunkOrientation walks the chunks of a PNG (big-endian lengths, 4-byte CRC
// after each chunk) or a RIFF/WebP file (little-endian sizes, even padding)
// from offset and returns the orientation of the first EXIF chunk.
func chunkOrientation(r io.ReaderAt, size, off int64, riff bool) int {
	for range 10_000 { // a bound on pathological files
		var h [8]byte
		if off+8 > size {
			return 1
		}
		if _, err := r.ReadAt(h[:], off); err != nil {
			return 1
		}
		var n int64
		if riff {
			n = int64(binary.LittleEndian.Uint32(h[4:]))
		} else {
			n = int64(binary.BigEndian.Uint32(h[:4]))
		}
		kind := string(h[4:8])
		if riff {
			kind = string(h[:4])
		}
		if (!riff && kind == "eXIf") || (riff && kind == "EXIF") {
			if n > maxExifChunk || off+8+n > size {
				return 1
			}
			buf := make([]byte, n)
			if _, err := r.ReadAt(buf, off+8); err != nil {
				return 1
			}
			buf = bytes.TrimPrefix(buf, []byte("Exif\x00\x00")) // some writers keep the JPEG prefix
			if o := tiffHeaderOrientation(buf); o != 0 {
				return o
			}
			return 1
		}
		if !riff && kind == "IEND" {
			return 1
		}
		if riff {
			off += 8 + n + n&1
		} else {
			off += 12 + n
		}
	}
	return 1
}

// tiffOrientation reads the Orientation tag from an APP1 payload that starts
// with "Exif\0\0"; 0 means this segment has none.
func tiffOrientation(p []byte) int {
	if len(p) < 14 || string(p[:6]) != "Exif\x00\x00" {
		return 0
	}
	return tiffHeaderOrientation(p[6:])
}

// tiffHeaderOrientation reads the Orientation tag from TIFF data (the byte
// order mark, magic 42 and the first directory); 0 means there is none.
func tiffHeaderOrientation(t []byte) int {
	if len(t) < 8 {
		return 0
	}
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
