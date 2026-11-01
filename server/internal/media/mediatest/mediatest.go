// Package mediatest builds test images: JPEGs with EXIF orientation and GPS data.
package mediatest

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"image/jpeg"
	"testing"
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
