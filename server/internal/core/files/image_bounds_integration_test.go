package files_test

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/png"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/openbekci/openbekci/server/internal/core/files"
)

// solidImage is a w x h opaque image of one colour that takes no memory of
// its own, so the test does not pay for the pixels it wants the server to hold.
type solidImage struct{ w, h int }

func (s solidImage) ColorModel() color.Model { return color.RGBAModel }
func (s solidImage) Bounds() image.Rectangle { return image.Rect(0, 0, s.w, s.h) }
func (solidImage) At(int, int) color.Color   { return color.RGBA{R: 30, G: 120, B: 200, A: 255} }
func (solidImage) Opaque() bool              { return true }

// solidPNG is a w x h PNG: a few hundred KB for 25 megapixels, a decompression bomb in miniature.
func solidPNG(t testing.TB, w, h int) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, solidImage{w, h}); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// peakHeap runs fn while sampling the heap and returns the highest HeapInuse
// seen above what was in use before.
func peakHeap(fn func()) uint64 {
	runtime.GC()
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	base := ms.HeapInuse
	var peak atomic.Uint64
	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			var m runtime.MemStats
			runtime.ReadMemStats(&m)
			if m.HeapInuse > peak.Load() {
				peak.Store(m.HeapInuse)
			}
			select {
			case <-stop:
				return
			case <-time.After(5 * time.Millisecond):
			}
		}
	}()
	fn()
	close(stop)
	wg.Wait()
	if p := peak.Load(); p > base {
		return p - base
	}
	return 0
}

func TestIntegrationLargeImageStaysWithinMemoryBound(t *testing.T) {
	e := newEnv(t, nil)
	admin := e.world.Actor("org_admin")
	ctx := e.ctx(admin, e.world.OrgA)

	atCap := solidPNG(t, 5000, 5000) // 25 megapixels exactly
	var f files.File
	var err error
	delta := peakHeap(func() {
		f, err = e.upload(ctx, e.world.OrgA, "bomb.png", atCap, nil)
	})
	if err != nil {
		t.Fatalf("a 25 MP image must be accepted: %v", err)
	}
	const bound = 300 << 20
	t.Logf("25 MP PNG (%d bytes): heap grew by %d MiB", len(atCap), delta>>20)
	if delta > bound {
		t.Errorf("processing a 25 MP image grew the heap by %d MiB, want under %d MiB", delta>>20, bound>>20)
	}
	if f.Variants[files.VariantThumb].Width != 256 || f.Variants[files.VariantMedium].Width != 1600 {
		t.Errorf("variants = %+v", f.Variants)
	}

	over := solidPNG(t, 5200, 5000) // 26 megapixels
	delta = peakHeap(func() {
		_, err = e.upload(ctx, e.world.OrgA, "toobig.png", over, nil)
	})
	wantStatus(t, err, 422, "26 MP image")
	if delta > 50<<20 {
		t.Errorf("rejecting a 26 MP image (header only) grew the heap by %d MiB", delta>>20)
	}
	if e.fileRows() != 1 {
		t.Errorf("the rejected image left a row behind: %d rows", e.fileRows())
	}
}

func TestIntegrationUploadRateLimitPerUser(t *testing.T) {
	e := newEnv(t, func(c *files.Config) { c.UploadsPerMinute = 2 })
	admin, member := e.world.Actor("org_admin"), e.world.Actor("staff")
	for i := 0; i < 2; i++ {
		e.mustUpload(e.ctx(admin, e.world.OrgA), e.world.OrgA, "a.txt", []byte("hello"), nil)
	}
	_, err := e.upload(e.ctx(admin, e.world.OrgA), e.world.OrgA, "a.txt", []byte("hello"), nil)
	wantStatus(t, err, 429, "third upload within the minute")
	// another user has a bucket of their own
	e.mustUpload(e.ctx(member, e.world.OrgA), e.world.OrgA, "a.txt", []byte("hello"), nil)
}

func TestIntegrationImageWorkersSerializeProcessing(t *testing.T) {
	e := newEnv(t, func(c *files.Config) { c.ImageWorkers = 1 })
	admin := e.world.Actor("org_admin")
	ctx := e.ctx(admin, e.world.OrgA)
	img := solidPNG(t, 2000, 2000)

	var wg sync.WaitGroup
	errs := make(chan error, 4)
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := e.upload(context.WithoutCancel(ctx), e.world.OrgA, "p.png", img, nil)
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Errorf("concurrent upload: %v", err)
		}
	}
}
