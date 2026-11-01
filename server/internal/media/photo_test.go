package media_test

import (
	"bytes"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"image"
	"image/png"
	"testing"

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
	p, err := media.ProcessPhoto(src)
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
		p, err := media.ProcessPhoto(mediatest.JPEG(t, mediatest.Halves(tc.w, tc.h)))
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
	a, err := media.ProcessPhoto(plain)
	if err != nil {
		t.Fatal(err)
	}
	b, err := media.ProcessPhoto(mediatest.WithExif(plain, 1)) // same pixels, other metadata
	if err != nil {
		t.Fatal(err)
	}
	if a.SHA256 != b.SHA256 || a.DHash != b.DHash {
		t.Error("the same pixels with different metadata should hash the same")
	}
	c, err := media.ProcessPhoto(mediatest.JPEG(t, mediatest.Halves(64, 49)))
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
		got, err := media.ProcessPhoto(b)
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
		"pixel bomb":      {pngClaiming(t, 20000, 20000), media.ErrTooManyPixels},
	} {
		if _, err := media.ProcessPhoto(tc.data); !errors.Is(err, tc.want) {
			t.Errorf("%s: err = %v, want %v", name, err, tc.want)
		}
	}
}

// pngClaiming is a tiny PNG whose header claims w×h pixels.
func pngClaiming(t *testing.T, w, h uint32) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewGray(image.Rect(0, 0, 1, 1))); err != nil {
		t.Fatal(err)
	}
	b := buf.Bytes()
	// Signature (8), IHDR length (4), "IHDR" (4), then width and height; CRC after 13 data bytes.
	binary.BigEndian.PutUint32(b[16:20], w)
	binary.BigEndian.PutUint32(b[20:24], h)
	binary.BigEndian.PutUint32(b[29:33], crc32.ChecksumIEEE(b[12:29]))
	return b
}
