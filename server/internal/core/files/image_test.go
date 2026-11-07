package files

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"testing"
)

// gpsMarker is planted in the EXIF GPS block and in a JPEG comment; it must
// not survive processing.
const gpsMarker = "SECRET-GPS-41N-29E"

// buildExif makes an APP1 EXIF payload with an Orientation tag and a GPS IFD.
func buildExif(orientation uint16) []byte {
	le := binary.LittleEndian
	var b bytes.Buffer
	b.WriteString("Exif\x00\x00")
	b.WriteString("II")
	_ = binary.Write(&b, le, uint16(42))
	_ = binary.Write(&b, le, uint32(8))
	// IFD0 at 8: two entries (30 bytes), GPS IFD at 38
	_ = binary.Write(&b, le, uint16(2))
	entry := func(tag, typ uint16, count, value uint32) {
		_ = binary.Write(&b, le, tag)
		_ = binary.Write(&b, le, typ)
		_ = binary.Write(&b, le, count)
		_ = binary.Write(&b, le, value)
	}
	entry(0x0112, 3, 1, uint32(orientation))
	entry(0x8825, 4, 1, 38)
	_ = binary.Write(&b, le, uint32(0))
	// GPS IFD at 38: two entries (30 bytes), rationals at 68
	_ = binary.Write(&b, le, uint16(2))
	entry(0x0001, 2, 2, uint32('N'))
	entry(0x0002, 5, 3, 68)
	_ = binary.Write(&b, le, uint32(0))
	for _, v := range []uint32{41, 1, 0, 1, 0, 1} {
		_ = binary.Write(&b, le, v)
	}
	b.WriteString(gpsMarker)
	return b.Bytes()
}

// jpegWithGPS is a w x h JPEG (red left half, blue right half) carrying EXIF
// with the given orientation, a GPS location and a comment.
func jpegWithGPS(t testing.TB, w, h int, orientation uint16) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			c := color.RGBA{R: 220, A: 255}
			if x >= w/2 {
				c = color.RGBA{B: 220, A: 255}
			}
			img.Set(x, y, c)
		}
	}
	var enc bytes.Buffer
	if err := jpeg.Encode(&enc, img, &jpeg.Options{Quality: 90}); err != nil {
		t.Fatal(err)
	}
	raw := enc.Bytes()
	segment := func(marker byte, payload []byte) []byte {
		out := []byte{0xFF, marker, 0, 0}
		binary.BigEndian.PutUint16(out[2:], uint16(len(payload)+2))
		return append(out, payload...)
	}
	var out bytes.Buffer
	out.Write(raw[:2]) // SOI
	out.Write(segment(0xE1, buildExif(orientation)))
	out.Write(segment(0xFE, []byte("comment "+gpsMarker)))
	out.Write(raw[2:])
	return out.Bytes()
}

func TestExifOrientationParsing(t *testing.T) {
	for o := uint16(1); o <= 8; o++ {
		if got := exifOrientation(jpegWithGPS(t, 8, 8, o)); got != int(o) {
			t.Errorf("orientation %d parsed as %d", o, got)
		}
	}
	if exifOrientation([]byte("not a jpeg")) != 1 || exifOrientation(nil) != 1 {
		t.Error("garbage must read as orientation 1")
	}
	// truncated EXIF must not panic
	full := jpegWithGPS(t, 8, 8, 6)
	for n := 0; n < 80 && n < len(full); n++ {
		_ = exifOrientation(full[:n])
	}
}

func TestProcessImageStripsMetadataAndMakesVariants(t *testing.T) {
	in := jpegWithGPS(t, 3000, 1500, 1)
	if !bytes.Contains(in, []byte(gpsMarker)) || !bytes.Contains(in, []byte("Exif")) {
		t.Fatal("the fixture must carry EXIF and the marker")
	}
	r, err := processImage(in, 0, DefaultMaxImageDimension)
	if err != nil {
		t.Fatal(err)
	}
	for name, data := range map[string][]byte{"original": r.original.data, "thumb": r.variants[VariantThumb].data, "medium": r.variants[VariantMedium].data} {
		if bytes.Contains(data, []byte(gpsMarker)) || bytes.Contains(data, []byte("Exif")) || bytes.Contains(data, []byte("comment")) {
			t.Errorf("%s still carries metadata", name)
		}
	}
	dims := func(data []byte) (int, int) {
		c, f, err := image.DecodeConfig(bytes.NewReader(data))
		if err != nil || f != "jpeg" {
			t.Fatalf("decode: %v %s", err, f)
		}
		return c.Width, c.Height
	}
	if w, h := dims(r.original.data); w != 3000 || h != 1500 {
		t.Errorf("original is %dx%d, want 3000x1500 (within the cap)", w, h)
	}
	if w, h := dims(r.variants[VariantMedium].data); w != 1600 || h != 800 {
		t.Errorf("medium is %dx%d, want 1600x800", w, h)
	}
	if w, h := dims(r.variants[VariantThumb].data); w != 256 || h != 128 {
		t.Errorf("thumb is %dx%d, want 256x128", w, h)
	}
	if v := r.variants[VariantThumb].v; v.Width != 256 || v.Height != 128 || v.Size != int64(len(r.variants[VariantThumb].data)) || v.Mime != "image/jpeg" {
		t.Errorf("thumb metadata = %+v", v)
	}
	// the stored original is capped
	big := jpegWithGPS(t, 5000, 100, 1)
	r, err = processImage(big, 0, 4096)
	if err != nil {
		t.Fatal(err)
	}
	if w, h := dims(r.original.data); w != 4096 || h != 81 {
		t.Errorf("capped original is %dx%d, want 4096x81", w, h)
	}
}

func TestProcessImageAppliesOrientation(t *testing.T) {
	// 400x200, red left half and blue right half, orientation 6 (rotate 90 clockwise):
	// upright it is 200x400 with red on top and blue at the bottom.
	r, err := processImage(jpegWithGPS(t, 400, 200, 6), 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	img, _, err := image.Decode(bytes.NewReader(r.original.data))
	if err != nil {
		t.Fatal(err)
	}
	if b := img.Bounds(); b.Dx() != 200 || b.Dy() != 400 {
		t.Fatalf("rotated image is %dx%d, want 200x400", b.Dx(), b.Dy())
	}
	top, _ := rgb(img.At(100, 50))
	_, bottom := rgb(img.At(100, 350))
	if top.R < 150 || bottom.B < 150 {
		t.Errorf("wrong orientation: top=%v bottom=%v", top, bottom)
	}
}

func rgb(c color.Color) (r, b color.RGBA) {
	cr, cg, cb, _ := c.RGBA()
	v := color.RGBA{R: uint8(cr >> 8), G: uint8(cg >> 8), B: uint8(cb >> 8), A: 255}
	return v, v
}

func TestOrientEveryCase(t *testing.T) {
	// a 3x2 image with distinct pixels: check where pixel (0,0) lands for each orientation
	src := image.NewRGBA(image.Rect(0, 0, 3, 2))
	for y := 0; y < 2; y++ {
		for x := 0; x < 3; x++ {
			src.SetRGBA(x, y, color.RGBA{R: uint8(x*10 + y), A: 255})
		}
	}
	corner := func(o int) (w, h int, x, y int) {
		dst := orient(src, o)
		w, h = dst.Bounds().Dx(), dst.Bounds().Dy()
		for yy := 0; yy < h; yy++ {
			for xx := 0; xx < w; xx++ {
				if dst.RGBAAt(xx, yy).R == 0 {
					return w, h, xx, yy
				}
			}
		}
		return
	}
	// original top-left pixel lands at: (x, y) in a w x h result
	want := map[int][4]int{
		1: {3, 2, 0, 0}, 2: {3, 2, 2, 0}, 3: {3, 2, 2, 1}, 4: {3, 2, 0, 1},
		5: {2, 3, 0, 0}, 6: {2, 3, 1, 0}, 7: {2, 3, 1, 2}, 8: {2, 3, 0, 2},
	}
	for o, exp := range want {
		w, h, x, y := corner(o)
		if got := [4]int{w, h, x, y}; got != exp {
			t.Errorf("orientation %d: got %v, want %v", o, got, exp)
		}
	}
}

func TestProcessImagePNGKeepsAlpha(t *testing.T) {
	img := image.NewNRGBA(image.Rect(0, 0, 40, 40))
	img.SetNRGBA(5, 5, color.NRGBA{R: 255, A: 128})
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	r, err := processImage(buf.Bytes(), 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if r.original.v.Mime != "image/png" {
		t.Errorf("mime = %s, want image/png", r.original.v.Mime)
	}
}

func TestProcessImageWebP(t *testing.T) {
	// a 1x1 lossless WebP
	webp, err := base64.StdEncoding.DecodeString("UklGRhoAAABXRUJQVlA4TA0AAAAvAAAAEAcQERGIiP4HAA==")
	if err != nil {
		t.Fatal(err)
	}
	r, err := processImage(webp, 0, 0)
	if err != nil {
		t.Fatalf("webp: %v", err)
	}
	if m := r.original.v.Mime; m != "image/jpeg" && m != "image/png" {
		t.Errorf("a re-encoded webp must be jpeg or png, got %s", m)
	}
	if got := renameForMIME("photo.webp", "image/jpeg"); got != "photo.jpg" {
		t.Errorf("renameForMIME = %q", got)
	}
	if got := renameForMIME("photo.JPEG", "image/jpeg"); got != "photo.JPEG" {
		t.Errorf("renameForMIME kept-extension = %q", got)
	}
}

func TestProcessImageRejects(t *testing.T) {
	if _, err := processImage([]byte("this is not an image at all"), 0, 0); err == nil {
		t.Error("garbage accepted")
	}
	// a valid image over the pixel budget is refused before it is decoded
	if _, err := processImage(jpegWithGPS(t, 300, 300, 1), 50_000, 0); err == nil {
		t.Error("an image over the pixel limit was accepted")
	}
	// a truncated JPEG
	j := jpegWithGPS(t, 100, 100, 1)
	if _, err := processImage(j[:len(j)/2], 0, 0); err == nil {
		t.Error("a truncated image was accepted")
	}
}

func TestCleanName(t *testing.T) {
	for in, want := range map[string]string{
		"photo.jpg":            "photo.jpg",
		"../../etc/passwd":     "passwd",
		`C:\Users\x\evil.exe`:  "evil.exe",
		"a\x00b\r\n.png":       "ab.png",
		`quote".pdf`:           "quote.pdf",
		".hidden":              "hidden",
		"   spaced name.txt  ": "spaced name.txt",
		"":                     "",
		"..":                   "",
	} {
		if got := cleanName(in); got != want {
			t.Errorf("cleanName(%q) = %q, want %q", in, got, want)
		}
	}
	long := cleanName(string(bytes.Repeat([]byte("a"), 400)) + ".pdf")
	if len([]rune(long)) != 200 || long[len(long)-4:] != ".pdf" {
		t.Errorf("long name not shortened with its extension: %d %q", len(long), long[len(long)-6:])
	}
}

func TestMatchSniffing(t *testing.T) {
	rules := DefaultTypes()
	exe := append([]byte("MZ\x90\x00"), make([]byte, 100)...)
	if _, allowed, ok := match(rules, "holiday.jpg", exe); !allowed || ok {
		t.Errorf("an exe named .jpg: allowed=%v ok=%v, want allowed extension but mismatching content", allowed, ok)
	}
	if _, allowed, _ := match(rules, "setup.exe", exe); allowed {
		t.Error(".exe is not an allowed extension")
	}
	pdf := []byte("%PDF-1.7\n%....")
	if r, _, ok := match(rules, "Report.PDF", pdf); !ok || r.MIME != "application/pdf" {
		t.Errorf("pdf: %+v %v", r, ok)
	}
	if _, _, ok := match(rules, "report.png", pdf); ok {
		t.Error("a pdf named .png was accepted")
	}
	zip := []byte("PK\x03\x04\x14\x00\x00\x00")
	if _, _, ok := match(rules, "minutes.docx", zip); !ok {
		t.Error("docx (zip) rejected")
	}
	if _, _, ok := match(rules, "minutes.pdf", zip); ok {
		t.Error("a zip named .pdf was accepted")
	}
	if _, _, ok := match(rules, "old.doc", append(append([]byte{}, oleMagic...), 0, 0)); !ok {
		t.Error("legacy .doc rejected")
	}
	if _, _, ok := match(rules, "notes.txt", []byte("plain text, nothing special\n")); !ok {
		t.Error("txt rejected")
	}
	if _, _, ok := match(rules, "notes.txt", []byte("\x00\x01\x02binary")); ok {
		t.Error("binary named .txt was accepted")
	}
	// an html page with a .jpg extension, and svg, are not images
	if _, _, ok := match(rules, "x.jpg", []byte("<html><script>alert(1)</script>")); ok {
		t.Error("html named .jpg accepted")
	}
	if _, allowed, _ := match(rules, "logo.svg", []byte("<svg xmlns=...>")); allowed {
		t.Error("svg must not be allowed")
	}
	// narrowing the allowlist by configuration
	narrow := FilterTypes(rules, []string{".JPG", "pdf"})
	if _, allowed, _ := match(narrow, "a.png", []byte("\x89PNG\r\n\x1a\n")); allowed {
		t.Error("png allowed after narrowing to jpg and pdf")
	}
	if _, _, ok := match(narrow, "a.jpg", jpegWithGPS(t, 8, 8, 1)[:600]); !ok {
		t.Error("jpg rejected after narrowing")
	}
}
