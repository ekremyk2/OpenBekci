package files

import (
	"bytes"
	"io"
	"os"
)

// rendition is one encoded image kept in memory by the test sink.
type rendition struct {
	data []byte
	v    Variant
}

type renditions struct {
	original rendition
	variants map[string]rendition
}

// processImage runs renderImage on in-memory data with an in-memory sink.
func processImage(data []byte, maxPixels, maxDim int) (renditions, error) {
	f, err := os.CreateTemp("", "image-test-*")
	if err != nil {
		return renditions{}, err
	}
	defer func() { _ = f.Close(); _ = os.Remove(f.Name()) }()
	if _, err := f.Write(data); err != nil {
		return renditions{}, err
	}
	out := renditions{variants: map[string]rendition{}}
	sink := func(name, mime string, w, h int, enc func(io.Writer) error) (encoded, error) {
		var buf bytes.Buffer
		if err := enc(&buf); err != nil {
			return encoded{}, err
		}
		r := rendition{data: buf.Bytes(), v: Variant{Width: w, Height: h, Size: int64(buf.Len()), Mime: mime}}
		if name == "" {
			out.original = r
		} else {
			out.variants[name] = r
		}
		return encoded{v: r.v}, nil
	}
	if _, _, err := renderImage(f, int64(len(data)), maxPixels, maxDim, sink); err != nil {
		return renditions{}, err
	}
	return out, nil
}
