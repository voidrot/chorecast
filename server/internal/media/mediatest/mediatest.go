// Package mediatest builds test images: JPEGs with EXIF orientation and GPS data.
package mediatest

import (
	"bytes"
	"encoding/binary"
	"hash/crc32"
	"image"
	"image/color"
	"image/jpeg"
	"testing"

	"github.com/gen2brain/vpx/webp"
)

// Halves is a w×h image whose left half is red and right half blue.
func Halves(w, h int) *image.RGBA {
	m := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			c := color.RGBA{R: 220, B: 20, A: 255}
			if x >= w/2 {
				c = color.RGBA{R: 20, B: 220, A: 255}
			}
			m.SetRGBA(x, y, c)
		}
	}
	return m
}

// JPEG encodes m as a JPEG.
func JPEG(t testing.TB, m image.Image) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, m, &jpeg.Options{Quality: 90}); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// GPSMarker is the GPS latitude ref written into test EXIF, so tests can look for it.
const GPSMarker = "N-CHORECAST-GPS"

// exifTIFF is a big-endian TIFF block with IFD0 holding the orientation and a GPS IFD pointer,
// and a GPS IFD with a latitude ref.
func exifTIFF(orientation uint16) []byte {
	var b bytes.Buffer
	w := func(v any) { _ = binary.Write(&b, binary.BigEndian, v) }
	b.WriteString("MM")
	w(uint16(42))
	w(uint32(8)) // IFD0
	w(uint16(2))
	w([]uint16{0x0112, 3}) // orientation, SHORT
	w(uint32(1))
	w([]uint16{orientation, 0})
	const gpsIFD = 8 + 2 + 2*12 + 4
	w([]uint16{0x8825, 4}) // GPS IFD pointer, LONG
	w(uint32(1))
	w(uint32(gpsIFD))
	w(uint32(0)) // no next IFD
	// GPS IFD: GPSLatitudeRef (ASCII), its value stored after the IFD.
	valueOff := gpsIFD + 2 + 12 + 4
	w(uint16(1))
	w([]uint16{0x0001, 2})
	w(uint32(len(GPSMarker) + 1))
	w(uint32(valueOff))
	w(uint32(0))
	b.WriteString(GPSMarker + "\x00")
	return b.Bytes()
}

// WithExif splices an APP1 EXIF segment carrying orientation and GPS data after the SOI.
func WithExif(jpg []byte, orientation uint16) []byte {
	payload := append([]byte("Exif\x00\x00"), exifTIFF(orientation)...)
	seg := []byte{0xFF, 0xE1, 0, 0}
	binary.BigEndian.PutUint16(seg[2:], uint16(len(payload)+2)) //nolint:gosec // small test data
	seg = append(seg, payload...)
	out := append([]byte{}, jpg[:2]...)
	out = append(out, seg...)
	return append(out, jpg[2:]...)
}

// ExifWithFiller is a little-endian TIFF block whose IFD0 has n Make entries, each pointing at
// the same strLen-byte string, before the orientation entry. A parser that reads every string
// does n×strLen work.
func ExifWithFiller(orientation uint16, n, strLen int) []byte {
	var b bytes.Buffer
	w := func(v any) { _ = binary.Write(&b, binary.LittleEndian, v) }
	b.WriteString("II")
	w(uint16(42))
	w(uint32(8))
	w(uint16(n + 1)) //nolint:gosec // test data, n < 65535
	strOff := 8 + 2 + 12*(n+1) + 4
	for range n {
		w([]uint16{0x010F, 2}) // Make, ASCII
		w(uint32(strLen))      //nolint:gosec // test data
		w(uint32(strOff))      //nolint:gosec // test data
	}
	w([]uint16{0x0112, 3})
	w(uint32(1))
	w([]uint16{orientation, 0})
	w(uint32(0))
	b.Write(bytes.Repeat([]byte("A"), strLen-1))
	b.WriteByte(0)
	return b.Bytes()
}

// WebP encodes m as a simple lossy WebP.
func WebP(t testing.TB, m image.Image) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := webp.Encode(&buf, m); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// WebPWithExif rewrites a simple lossy WebP (RIFF, "VP8 " chunk) as an extended one carrying
// tiff in an EXIF chunk.
func WebPWithExif(t testing.TB, simple []byte, w, h int, tiff []byte) []byte {
	t.Helper()
	if len(simple) < 20 || string(simple[12:16]) != "VP8 " {
		t.Fatalf("not a simple lossy WebP")
	}
	chunk := func(id string, payload []byte) []byte {
		out := append([]byte(id), 0, 0, 0, 0)
		binary.LittleEndian.PutUint32(out[4:], uint32(len(payload))) //nolint:gosec // test data
		out = append(out, payload...)
		if len(payload)%2 == 1 {
			out = append(out, 0)
		}
		return out
	}
	vp8x := make([]byte, 10)
	vp8x[0] = 0x08                                                                         // EXIF present
	put24 := func(b []byte, v int) { b[0], b[1], b[2] = byte(v), byte(v>>8), byte(v>>16) } //nolint:gosec // 24-bit fields, truncation intended
	put24(vp8x[4:], w-1)
	put24(vp8x[7:], h-1)
	body := append([]byte("WEBP"), chunk("VP8X", vp8x)...)
	body = append(body, simple[12:]...) // the VP8 chunk as is
	body = append(body, chunk("EXIF", tiff)...)
	out := append([]byte("RIFF"), 0, 0, 0, 0)
	binary.LittleEndian.PutUint32(out[4:], uint32(len(body))) //nolint:gosec // test data
	return append(out, body...)
}

// PNGHeader is a PNG with only an IHDR (and IEND): enough for its header to be checked.
func PNGHeader(w, h uint32, depth, colorType, interlace byte) []byte {
	ihdr := make([]byte, 13)
	binary.BigEndian.PutUint32(ihdr[0:4], w)
	binary.BigEndian.PutUint32(ihdr[4:8], h)
	ihdr[8], ihdr[9], ihdr[12] = depth, colorType, interlace
	chunk := func(id string, data []byte) []byte {
		out := binary.BigEndian.AppendUint32(nil, uint32(len(data))) //nolint:gosec // test data
		out = append(out, id...)
		out = append(out, data...)
		return binary.BigEndian.AppendUint32(out, crc32.ChecksumIEEE(append([]byte(id), data...)))
	}
	out := []byte("\x89PNG\r\n\x1a\n")
	out = append(out, chunk("IHDR", ihdr)...)
	return append(out, chunk("IEND", nil)...)
}

// AnimatedWebP is a two-frame animated WebP.
func AnimatedWebP(t testing.TB) []byte {
	t.Helper()
	var buf bytes.Buffer
	anim := &webp.WEBP{Image: []image.Image{Halves(16, 16), Halves(16, 16)}, Delay: []int{100, 100}}
	if err := webp.EncodeAll(&buf, anim); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}
