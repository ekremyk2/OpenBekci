package files

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/draw"
	"image/jpeg"
	"image/png"

	// decoders for image.Decode
	_ "golang.org/x/image/webp"

	xdraw "golang.org/x/image/draw"
)

// Image variant boxes: the longer side of each variant is at most this many pixels.
const (
	thumbBox  = 256
	mediumBox = 1600
)

var (
	errBadImage     = errors.New("files: not a valid image")
	errImageTooBig  = errors.New("files: image has too many pixels")
	errImageNoSize  = errors.New("files: image has no size")
	jpegOriginalQ   = 88
	jpegMediumQ     = 85
	jpegThumbnailQ  = 80
	pngCompression  = png.DefaultCompression
	defaultMaxPixel = 64_000_000
)

// rendition is one encoded image.
type rendition struct {
	data []byte
	v    Variant
}

// renditions is an uploaded image after processing: the cleaned original and
// its variants.
type renditions struct {
	original rendition
	variants map[string]rendition
}

// processImage decodes an uploaded image and returns it re-encoded, which
// drops every piece of metadata (EXIF with its GPS location, XMP, ICC
// profile, comments, embedded thumbnails): the encoders write pixels only.
// The EXIF orientation of a JPEG is applied to the pixels first. The original
// is downscaled to fit maxDim, and thumb and medium variants are made.
// maxPixels guards against decompression bombs.
func processImage(data []byte, maxPixels, maxDim int) (renditions, error) {
	if maxPixels <= 0 {
		maxPixels = defaultMaxPixel
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return renditions{}, fmt.Errorf("%w: %w", errBadImage, err)
	}
	if cfg.Width <= 0 || cfg.Height <= 0 {
		return renditions{}, errImageNoSize
	}
	if cfg.Width > maxPixels/cfg.Height { // avoids overflow of Width*Height
		return renditions{}, errImageTooBig
	}
	src, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return renditions{}, fmt.Errorf("%w: %w", errBadImage, err)
	}
	opaque := isOpaque(src)
	img := toRGBA(src)
	if format == "jpeg" {
		img = orient(img, exifOrientation(data))
	}
	// JPEG stays JPEG, PNG stays PNG; WebP (which Go cannot encode) becomes
	// JPEG, or PNG when it has transparency.
	asJPEG := opaque && format != "png"
	mime := "image/png"
	if asJPEG {
		mime = "image/jpeg"
	}

	out := renditions{variants: map[string]rendition{}}
	box := func(w, h, limit int) *image.RGBA {
		nw, nh := fit(w, h, limit)
		if nw == w && nh == h {
			return img
		}
		dst := image.NewRGBA(image.Rect(0, 0, nw, nh))
		xdraw.CatmullRom.Scale(dst, dst.Bounds(), img, img.Bounds(), xdraw.Src, nil)
		return dst
	}
	b := img.Bounds()
	for _, s := range []struct {
		name  string
		limit int
		q     int
	}{{"", maxDim, jpegOriginalQ}, {VariantMedium, mediumBox, jpegMediumQ}, {VariantThumb, thumbBox, jpegThumbnailQ}} {
		scaled := img
		if s.limit > 0 {
			scaled = box(b.Dx(), b.Dy(), s.limit)
		}
		enc, err := encode(scaled, asJPEG, s.q)
		if err != nil {
			return renditions{}, err
		}
		r := rendition{data: enc, v: Variant{Width: scaled.Bounds().Dx(), Height: scaled.Bounds().Dy(), Size: int64(len(enc)), Mime: mime}}
		if s.name == "" {
			out.original = r
		} else {
			out.variants[s.name] = r
		}
	}
	return out, nil
}

func encode(img *image.RGBA, asJPEG bool, quality int) ([]byte, error) {
	var buf bytes.Buffer
	if asJPEG {
		if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: quality}); err != nil {
			return nil, fmt.Errorf("encode jpeg: %w", err)
		}
		return buf.Bytes(), nil
	}
	enc := png.Encoder{CompressionLevel: pngCompression}
	if err := enc.Encode(&buf, img); err != nil {
		return nil, fmt.Errorf("encode png: %w", err)
	}
	return buf.Bytes(), nil
}

// fit returns the size of a w x h image scaled down to fit a limit x limit
// box (never up). A limit of zero or less means no limit.
func fit(w, h, limit int) (int, int) {
	if limit <= 0 || (w <= limit && h <= limit) {
		return w, h
	}
	if w >= h {
		return limit, max(1, h*limit/w)
	}
	return max(1, w*limit/h), limit
}

func isOpaque(img image.Image) bool {
	if o, ok := img.(interface{ Opaque() bool }); ok {
		return o.Opaque()
	}
	return false
}

// toRGBA copies img into a zero-based RGBA image.
func toRGBA(img image.Image) *image.RGBA {
	b := img.Bounds()
	dst := image.NewRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	draw.Draw(dst, dst.Bounds(), img, b.Min, draw.Src)
	return dst
}

// orient applies an EXIF orientation (1..8) to the pixels.
func orient(src *image.RGBA, o int) *image.RGBA {
	if o <= 1 || o > 8 {
		return src
	}
	sw, sh := src.Bounds().Dx(), src.Bounds().Dy()
	dw, dh := sw, sh
	if o >= 5 {
		dw, dh = sh, sw
	}
	dst := image.NewRGBA(image.Rect(0, 0, dw, dh))
	for dy := 0; dy < dh; dy++ {
		for dx := 0; dx < dw; dx++ {
			var sx, sy int
			switch o {
			case 2:
				sx, sy = sw-1-dx, dy
			case 3:
				sx, sy = sw-1-dx, sh-1-dy
			case 4:
				sx, sy = dx, sh-1-dy
			case 5:
				sx, sy = dy, dx
			case 6:
				sx, sy = dy, sh-1-dx
			case 7:
				sx, sy = sw-1-dy, sh-1-dx
			case 8:
				sx, sy = sw-1-dy, dx
			}
			si := src.PixOffset(sx, sy)
			di := dst.PixOffset(dx, dy)
			copy(dst.Pix[di:di+4], src.Pix[si:si+4])
		}
	}
	return dst
}
