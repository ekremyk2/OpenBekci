package files

import (
	"bufio"
	"errors"
	"fmt"
	"image"
	"image/draw"
	"image/jpeg"
	"image/png"
	"io"
	"os"

	// decoders for image.Decode
	_ "golang.org/x/image/webp"

	xdraw "golang.org/x/image/draw"
	"golang.org/x/image/math/f64"
)

// Image variant boxes: the longer side of each variant is at most this many pixels.
const (
	thumbBox  = 256
	mediumBox = 1600
)

var (
	errBadImage    = errors.New("files: not a valid image")
	errImageTooBig = errors.New("files: image has too many pixels")
	errImageNoSize = errors.New("files: image has no size")
	jpegOriginalQ  = 88
	jpegMediumQ    = 85
	jpegThumbnailQ = 80
	pngCompression = png.DefaultCompression
)

// DefaultMaxImagePixels is the largest image (width times height) that is
// decoded: 25 megapixels. A decoded image costs 4 bytes per pixel, so the cap
// bounds the memory one upload can take (about 100 MB for the decoded pixels).
const DefaultMaxImagePixels = 25_000_000

// encoded is one stored rendition of an image.
type encoded struct {
	v   Variant
	sha string // hex SHA-256 of the encoded bytes
}

// encodeSink stores one rendition. enc writes the encoded bytes to the writer
// it is given; the sink returns the size and digest of what was written.
type encodeSink func(name, mime string, w, h int, enc func(io.Writer) error) (encoded, error)

// renderImage decodes the image in f and hands the re-encoded original and
// its variants to put, one at a time (so only one scaled copy is alive at once).
// Re-encoding drops every piece of metadata (EXIF with its GPS location, XMP,
// ICC profile, comments, embedded thumbnails): the encoders write pixels only.
// The EXIF orientation (JPEG, PNG and WebP carry one) is applied to the pixels
// first. The original is downscaled to fit maxDim, and thumb and medium
// variants are made. maxPixels guards against decompression bombs: only the
// header is read to enforce it.
func renderImage(f *os.File, size int64, maxPixels, maxDim int, put encodeSink) (encoded, map[string]encoded, error) {
	if maxPixels <= 0 {
		maxPixels = DefaultMaxImagePixels
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return encoded{}, nil, err
	}
	cfg, format, err := image.DecodeConfig(bufio.NewReader(f))
	if err != nil {
		return encoded{}, nil, fmt.Errorf("%w: %w", errBadImage, err)
	}
	if cfg.Width <= 0 || cfg.Height <= 0 {
		return encoded{}, nil, errImageNoSize
	}
	if cfg.Width > maxPixels/cfg.Height { // avoids overflow of Width*Height
		return encoded{}, nil, errImageTooBig
	}
	orientation := imageOrientation(f, size, format)
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return encoded{}, nil, err
	}
	src, _, err := image.Decode(bufio.NewReader(f))
	if err != nil {
		return encoded{}, nil, fmt.Errorf("%w: %w", errBadImage, err)
	}
	opaque := isOpaque(src)
	img := orient(src, orientation) // a copy only when it has to turn the pixels
	src = nil                       //nolint:ineffassign,wastedassign // let the decoder's buffer go once oriented

	// JPEG stays JPEG, PNG stays PNG; WebP (which Go cannot encode) becomes
	// JPEG, or PNG when it has transparency.
	asJPEG := opaque && format != "png"
	mime := "image/png"
	if asJPEG {
		mime = "image/jpeg"
	}
	one := func(name string, im image.Image, q int) (encoded, error) {
		b := im.Bounds()
		return put(name, mime, b.Dx(), b.Dy(), func(w io.Writer) error { return encode(w, im, asJPEG, q) })
	}

	// original -> medium -> thumb, each scaled from the previous one
	cur := scaleTo(img, maxDim)
	img = nil //nolint:ineffassign,wastedassign // cur holds the pixels from here on
	orig, err := one("", cur, jpegOriginalQ)
	if err != nil {
		return encoded{}, nil, err
	}
	variants := map[string]encoded{}
	for _, s := range []struct {
		name  string
		limit int
		q     int
	}{{VariantMedium, mediumBox, jpegMediumQ}, {VariantThumb, thumbBox, jpegThumbnailQ}} {
		cur = scaleTo(cur, s.limit)
		if variants[s.name], err = one(s.name, cur, s.q); err != nil {
			return encoded{}, nil, err
		}
	}
	return orig, variants, nil
}

// scaleTo returns img scaled down to fit a limit x limit box, or img itself
// when it already fits (or limit is not positive).
//
// x/image's Kernel.Scale keeps a float64 intermediate of the destination width
// times the source height (32 bytes per pixel: 650 MB for 5000x5000 down to
// 4096), so it is not used on large images. Instead the image is halved with a
// 2x2 box filter while it is at least twice the target, then resampled once
// with Kernel.Transform, which works per output pixel and allocates only the
// result; the remaining ratio is under 2, so it does not alias.
func scaleTo(img image.Image, limit int) image.Image {
	for {
		b := img.Bounds()
		nw, nh := fit(b.Dx(), b.Dy(), limit)
		if nw == b.Dx() && nh == b.Dy() {
			return img
		}
		if limit > 0 && (b.Dx() >= 2*limit || b.Dy() >= 2*limit) && b.Dx() >= 2 && b.Dy() >= 2 {
			img = halve(img)
			continue
		}
		dst := image.NewRGBA(image.Rect(0, 0, nw, nh))
		sx, sy := float64(nw)/float64(b.Dx()), float64(nh)/float64(b.Dy())
		m := f64.Aff3{sx, 0, -sx * float64(b.Min.X), 0, sy, -sy * float64(b.Min.Y)}
		xdraw.CatmullRom.Transform(dst, m, img, b, xdraw.Src, nil)
		return dst
	}
}

// halve returns img at half size (rounded up), each pixel the average of a 2x2
// block (alpha-weighted for non-premultiplied images).
func halve(img image.Image) image.Image {
	switch s := img.(type) {
	case *image.RGBA:
		b := s.Bounds()
		w, h := b.Dx(), b.Dy()
		d := image.NewRGBA(image.Rect(0, 0, (w+1)/2, (h+1)/2))
		for y := 0; y < d.Rect.Dy(); y++ {
			y0, y1 := 2*y, min(2*y+1, h-1)
			for x := 0; x < d.Rect.Dx(); x++ {
				x0, x1 := 2*x, min(2*x+1, w-1)
				p := [4]int{s.PixOffset(b.Min.X+x0, b.Min.Y+y0), s.PixOffset(b.Min.X+x1, b.Min.Y+y0), s.PixOffset(b.Min.X+x0, b.Min.Y+y1), s.PixOffset(b.Min.X+x1, b.Min.Y+y1)}
				o := d.PixOffset(x, y)
				for c := 0; c < 4; c++ {
					d.Pix[o+c] = uint8((int(s.Pix[p[0]+c]) + int(s.Pix[p[1]+c]) + int(s.Pix[p[2]+c]) + int(s.Pix[p[3]+c]) + 2) / 4) //nolint:gosec // an average of four bytes fits a byte
				}
			}
		}
		return d
	case *image.NRGBA:
		b := s.Bounds()
		w, h := b.Dx(), b.Dy()
		d := image.NewNRGBA(image.Rect(0, 0, (w+1)/2, (h+1)/2))
		for y := 0; y < d.Rect.Dy(); y++ {
			y0, y1 := 2*y, min(2*y+1, h-1)
			for x := 0; x < d.Rect.Dx(); x++ {
				x0, x1 := 2*x, min(2*x+1, w-1)
				p := [4]int{s.PixOffset(b.Min.X+x0, b.Min.Y+y0), s.PixOffset(b.Min.X+x1, b.Min.Y+y0), s.PixOffset(b.Min.X+x0, b.Min.Y+y1), s.PixOffset(b.Min.X+x1, b.Min.Y+y1)}
				var sum [3]int
				asum := 0
				for _, o := range p {
					a := int(s.Pix[o+3])
					asum += a
					for c := 0; c < 3; c++ {
						sum[c] += int(s.Pix[o+c]) * a
					}
				}
				o := d.PixOffset(x, y)
				if asum > 0 {
					for c := 0; c < 3; c++ {
						d.Pix[o+c] = uint8((sum[c] + asum/2) / asum) //nolint:gosec // an alpha-weighted average of bytes fits a byte
					}
				}
				d.Pix[o+3] = uint8((asum + 2) / 4)
			}
		}
		return d
	}
	return halve(toRGBA(img))
}

func encode(w io.Writer, img image.Image, asJPEG bool, quality int) error {
	if asJPEG {
		if err := jpeg.Encode(w, img, &jpeg.Options{Quality: quality}); err != nil {
			return fmt.Errorf("encode jpeg: %w", err)
		}
		return nil
	}
	enc := png.Encoder{CompressionLevel: pngCompression}
	if err := enc.Encode(w, img); err != nil {
		return fmt.Errorf("encode png: %w", err)
	}
	return nil
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

// orient applies an EXIF orientation (1..8) to the pixels. Orientation 1 (or
// none) returns src untouched, with no copy.
func orient(src image.Image, o int) image.Image {
	if o <= 1 || o > 8 {
		return src
	}
	// RGBA and NRGBA are turned in their own layout (one new image); anything
	// else (YCbCr, paletted) is converted to RGBA first.
	sb := src.Bounds()
	sw, sh := sb.Dx(), sb.Dy()
	dw, dh := sw, sh
	if o >= 5 {
		dw, dh = sh, sw
	}
	drect := image.Rect(0, 0, dw, dh)
	var (
		spix, dpix []byte
		soff, doff func(x, y int) int
		out        image.Image
	)
	switch s := src.(type) {
	case *image.NRGBA:
		d := image.NewNRGBA(drect)
		spix, dpix, soff, doff, out = s.Pix, d.Pix, s.PixOffset, d.PixOffset, d
	case *image.RGBA:
		d := image.NewRGBA(drect)
		spix, dpix, soff, doff, out = s.Pix, d.Pix, s.PixOffset, d.PixOffset, d
	default:
		return orient(toRGBA(src), o)
	}
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
			si := soff(sb.Min.X+sx, sb.Min.Y+sy)
			di := doff(dx, dy)
			copy(dpix[di:di+4], spix[si:si+4])
		}
	}
	return out
}
