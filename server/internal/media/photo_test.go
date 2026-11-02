package media_test

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"image/png"
	"os"
	"runtime"
	"testing"
	"time"

	"github.com/gen2brain/vpx/webp"

	"github.com/voidrot/chorecast/server/internal/media"
	"github.com/voidrot/chorecast/server/internal/media/mediatest"
)

// riffChunks lists the chunk ids of a WebP file (and those inside nothing else: WebP has a flat
// chunk list after the RIFF header).
func riffChunks(t *testing.T, b []byte) []string {
	t.Helper()
	if len(b) < 12 || string(b[0:4]) != "RIFF" || string(b[8:12]) != "WEBP" {
		t.Fatalf("not a WebP file: % x", b[:min(16, len(b))])
	}
	var ids []string
	for i := 12; i+8 <= len(b); {
		ids = append(ids, string(b[i:i+4]))
		n := int(binary.LittleEndian.Uint32(b[i+4 : i+8]))
		i += 8 + n + n%2
	}
	return ids
}

func TestProcessPhotoStripsMetadataAndOrients(t *testing.T) {
	src := mediatest.WithExif(mediatest.JPEG(t, mediatest.Halves(40, 20)), 6)
	if !bytes.Contains(src, []byte(mediatest.GPSMarker)) {
		t.Fatal("fixture should carry GPS data")
	}
	p, err := media.ProcessPhoto(t.Context(), src)
	if err != nil {
		t.Fatal(err)
	}
	for _, out := range [][]byte{p.WebP, p.Thumb} {
		for _, id := range riffChunks(t, out) {
			if id != "VP8 " && id != "VP8L" && id != "VP8X" && id != "ALPH" {
				t.Errorf("output has a %q chunk; only image data is allowed", id)
			}
		}
		if _, err := webp.RawExif(bytes.NewReader(out)); !errors.Is(err, webp.ErrNoExif) {
			t.Errorf("output carries EXIF: %v", err)
		}
		if bytes.Contains(out, []byte(mediatest.GPSMarker)) || bytes.Contains(out, []byte("Exif")) {
			t.Error("output contains the EXIF/GPS bytes")
		}
	}
	// Orientation 6 means "turn 90° clockwise": the 40×20 image becomes 20×40, left half on top.
	if p.Width != 20 || p.Height != 40 {
		t.Fatalf("size = %dx%d, want 20x40", p.Width, p.Height)
	}
	img, err := webp.Decode(bytes.NewReader(p.WebP))
	if err != nil {
		t.Fatal(err)
	}
	if r, _, b, _ := img.At(10, 5).RGBA(); r <= b {
		t.Errorf("top should be red (the original left half), got r=%d b=%d", r>>8, b>>8)
	}
	if r, _, b, _ := img.At(10, 35).RGBA(); b <= r {
		t.Errorf("bottom should be blue (the original right half), got r=%d b=%d", r>>8, b>>8)
	}
}

func TestProcessPhotoScalesDown(t *testing.T) {
	for _, tc := range []struct {
		w, h, wantW, wantH, thumbW, thumbH int
	}{
		{3200, 2400, 1600, 1200, 320, 240},
		{1000, 2500, 640, 1600, 128, 320},
		{800, 600, 800, 600, 320, 240}, // never scaled up
	} {
		p, err := media.ProcessPhoto(t.Context(), mediatest.JPEG(t, mediatest.Halves(tc.w, tc.h)))
		if err != nil {
			t.Fatal(err)
		}
		if p.Width != tc.wantW || p.Height != tc.wantH || p.ThumbWidth != tc.thumbW || p.ThumbHeight != tc.thumbH {
			t.Errorf("%dx%d: got %dx%d, thumb %dx%d", tc.w, tc.h, p.Width, p.Height, p.ThumbWidth, p.ThumbHeight)
		}
		for name, b := range map[string][]byte{"photo": p.WebP, "thumb": p.Thumb} {
			cfg, err := webp.DecodeConfig(bytes.NewReader(b))
			if err != nil {
				t.Fatal(err)
			}
			wantW, wantH := p.Width, p.Height
			if name == "thumb" {
				wantW, wantH = p.ThumbWidth, p.ThumbHeight
			}
			if cfg.Width != wantW || cfg.Height != wantH {
				t.Errorf("%s encoded as %dx%d, want %dx%d", name, cfg.Width, cfg.Height, wantW, wantH)
			}
		}
	}
}

func TestPhotoHashesArePixelBased(t *testing.T) {
	plain := mediatest.JPEG(t, mediatest.Halves(64, 48))
	a, err := media.ProcessPhoto(t.Context(), plain)
	if err != nil {
		t.Fatal(err)
	}
	b, err := media.ProcessPhoto(t.Context(), mediatest.WithExif(plain, 1)) // same pixels, other metadata
	if err != nil {
		t.Fatal(err)
	}
	if a.SHA256 != b.SHA256 || a.DHash != b.DHash {
		t.Error("the same pixels with different metadata should hash the same")
	}
	c, err := media.ProcessPhoto(t.Context(), mediatest.JPEG(t, mediatest.Halves(64, 49)))
	if err != nil {
		t.Fatal(err)
	}
	if a.SHA256 == c.SHA256 {
		t.Error("different images should hash differently")
	}
	if a.DHash == 0 {
		t.Error("a red/blue image should set dHash bits")
	}
}

func TestProcessPhotoAcceptsPNGAndWebP(t *testing.T) {
	var p bytes.Buffer
	if err := png.Encode(&p, mediatest.Halves(30, 20)); err != nil {
		t.Fatal(err)
	}
	var w bytes.Buffer
	if err := webp.Encode(&w, mediatest.Halves(30, 20)); err != nil {
		t.Fatal(err)
	}
	for name, b := range map[string][]byte{"png": p.Bytes(), "webp": w.Bytes()} {
		got, err := media.ProcessPhoto(t.Context(), b)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if got.Width != 30 || got.Height != 20 || string(got.Format) != name {
			t.Errorf("%s: got %s %dx%d", name, got.Format, got.Width, got.Height)
		}
	}
}

func TestProcessPhotoRejects(t *testing.T) {
	jpg := mediatest.JPEG(t, mediatest.Halves(8, 8))
	heic := append([]byte{0, 0, 0, 0x18}, []byte("ftypheic\x00\x00\x00\x00mif1heic")...)
	for name, tc := range map[string]struct {
		data []byte
		want error
	}{
		"empty":           {nil, media.ErrUnsupportedFormat},
		"text":            {[]byte("<svg xmlns='http://www.w3.org/2000/svg'/>"), media.ErrUnsupportedFormat},
		"gif":             {[]byte("GIF89a\x01\x00\x01\x00"), media.ErrUnsupportedFormat},
		"riff not webp":   {[]byte("RIFF\x10\x00\x00\x00WAVEfmt "), media.ErrUnsupportedFormat},
		"heic":            {heic, media.ErrHEIC},
		"truncated jpeg":  {jpg[:len(jpg)/2], media.ErrInvalidImage},
		"jpeg magic only": {[]byte{0xFF, 0xD8, 0xFF, 0xE0, 0, 0}, media.ErrInvalidImage},
		// A tiny PNG claiming 40,000,000×1: small area, but kernel scalers allocate per column.
		"thin bomb":         {mediatest.PNGHeader(40_000_000, 1, 8, 0, 0), media.ErrTooManyPixels},
		"one side too long": {mediatest.PNGHeader(media.MaxSide+1, 10, 8, 0, 0), media.ErrTooManyPixels},
		"over the area cap": {mediatest.PNGHeader(6000, 4001, 8, 0, 0), media.ErrTooManyPixels},
		// Within the size caps, but decoding would need more than MaxDecodeBytes.
		"16-bit RGBA png":        {mediatest.PNGHeader(4800, 4800, 16, 6, 0), media.ErrTooComplex},
		"interlaced RGBA png":    {mediatest.PNGHeader(4800, 4800, 8, 6, 1), media.ErrTooComplex},
		"progressive CMYK jpeg":  {jpegHeader(0xC2, 4000, 3000, 4), media.ErrTooComplex},
		"progressive 4:4:4 jpeg": {jpegHeader(0xC2, 4000, 4000, 3), media.ErrTooComplex},
		"animated webp":          {mediatest.AnimatedWebP(t), media.ErrAnimated},
	} {
		if _, err := media.ProcessPhoto(t.Context(), tc.data); !errors.Is(err, tc.want) {
			t.Errorf("%s: err = %v, want %v", name, err, tc.want)
		}
	}
}

// jpegHeader is a JPEG with only a start-of-frame (marker sof) for nc 1×1-sampled components,
// followed by a start of scan: enough for its header to be checked.
func jpegHeader(sof byte, w, h uint16, nc byte) []byte {
	seg := []byte{8, byte(h >> 8), byte(h), byte(w >> 8), byte(w), nc} //nolint:gosec // big-endian bytes, truncation intended
	for i := range nc {
		seg = append(seg, i+1, 0x11, 0)
	}
	out := []byte{0xFF, 0xD8, 0xFF, sof, 0, byte(len(seg) + 2)} //nolint:gosec // short test segment
	out = append(out, seg...)
	return append(out, 0xFF, 0xDA, 0, 2)
}

// The orientation of a WebP comes from its raw EXIF chunk, read with the bounded parser: EXIF
// built so that a parser reading every string does gigabytes of work is handled at once.
func TestWebPExifOrientation(t *testing.T) {
	tiff := mediatest.ExifWithFiller(6, 16000, 1<<20) // ~16 GB of string reads for a naive parser
	src := mediatest.WebPWithExif(t, mediatest.WebP(t, mediatest.Halves(40, 20)), 40, 20, tiff)
	start := time.Now() //nolint:forbidigo // measuring this test's own run time
	p, err := media.ProcessPhoto(t.Context(), src)
	if err != nil {
		t.Fatal(err)
	}
	if d := time.Since(start); d > 2*time.Second { //nolint:forbidigo // as above
		t.Errorf("processing took %v", d)
	}
	if p.Width != 20 || p.Height != 40 {
		t.Errorf("size = %dx%d, want 20x40 (turned by EXIF orientation 6)", p.Width, p.Height)
	}
	if bytes.Contains(p.WebP, []byte("AAAA")) {
		t.Error("the EXIF strings were copied into the output")
	}
}

// One upload's allocations stay within the documented budget for a large ordinary photo. The
// total allocated is an upper bound on the peak.
func TestProcessPhotoMemory(t *testing.T) {
	src := mediatest.JPEG(t, mediatest.Halves(6000, 4000)) // 24 MP, the area cap
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	p, err := media.ProcessPhoto(t.Context(), src)
	runtime.ReadMemStats(&after)
	if err != nil {
		t.Fatal(err)
	}
	if p.Width != 1600 || p.Height != 1067 {
		t.Errorf("size = %dx%d", p.Width, p.Height)
	}
	t.Logf("allocated %d MB", (after.TotalAlloc-before.TotalAlloc)>>20)
	if mb := (after.TotalAlloc - before.TotalAlloc) >> 20; mb > 120 {
		t.Errorf("processing a 24 MP JPEG allocated %d MB, want at most 120", mb)
	}
}

func TestProcessPhotoStopsWhenCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := media.ProcessPhoto(ctx, mediatest.JPEG(t, mediatest.Halves(64, 48))); !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want context.Canceled", err)
	}
}

// An extended WebP's canvas is what the budget is computed from, so a bitstream frame larger
// than the canvas is refused before it is allocated. testdata holds the auditor's repro: a
// 698-byte lossless 16384×16384 frame in a 1×1 canvas (about 1 GB decoded).
func TestWebPFrameLargerThanCanvas(t *testing.T) {
	repro, err := os.ReadFile("testdata/vp8l-16384px-in-1px-canvas.webp")
	if err != nil {
		t.Fatal(err)
	}
	for name, data := range map[string][]byte{
		"lossless 16384² in 1×1": repro,
		"lossy 3000² in 1×1":     mediatest.WebPInCanvas(t, mediatest.WebP(t, mediatest.Halves(3000, 3000)), 1, 1),
		"lossy 3000² in 100×100": mediatest.WebPInCanvas(t, mediatest.WebP(t, mediatest.Halves(3000, 3000)), 100, 100),
	} {
		var before, after runtime.MemStats
		runtime.GC()
		runtime.ReadMemStats(&before)
		_, err := media.ProcessPhoto(t.Context(), data)
		runtime.ReadMemStats(&after)
		if !errors.Is(err, media.ErrInvalidImage) {
			t.Errorf("%s: err = %v, want ErrInvalidImage", name, err)
		}
		if mb := (after.TotalAlloc - before.TotalAlloc) >> 20; mb > 8 {
			t.Errorf("%s: allocated %d MB before refusing it", name, mb)
		}
	}
}
