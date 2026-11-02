package media

import (
	"encoding/binary"
	"errors"
	"hash/crc32"
	"testing"
)

func pngWith(w, h uint32, depth, colorType byte, trns bool) []byte {
	chunk := func(id string, data []byte) []byte {
		out := binary.BigEndian.AppendUint32(nil, uint32(len(data))) //nolint:gosec // test data
		out = append(out, id...)
		out = append(out, data...)
		return binary.BigEndian.AppendUint32(out, crc32.ChecksumIEEE(append([]byte(id), data...)))
	}
	ihdr := binary.BigEndian.AppendUint32(nil, w)
	ihdr = binary.BigEndian.AppendUint32(ihdr, h)
	ihdr = append(ihdr, depth, colorType, 0, 0, 0)
	out := append([]byte("\x89PNG\r\n\x1a\n"), chunk("IHDR", ihdr)...)
	if trns {
		out = append(out, chunk("tRNS", []byte{0, 0})...)
	}
	return append(out, chunk("IEND", nil)...)
}

type comp struct{ id, hv byte }

// jpegWith is a JPEG header: optional JFIF and Adobe (transform) markers, then a start of frame
// sof with the given components, then a start of scan.
func jpegWith(sof byte, w, h uint16, jfif bool, adobe int, comps ...comp) []byte {
	out := []byte{0xFF, 0xD8}
	if jfif {
		out = append(out, 0xFF, 0xE0, 0, 16, 'J', 'F', 'I', 'F', 0, 1, 1, 0, 0, 1, 0, 1, 0, 0)
	}
	if adobe >= 0 {
		out = append(out, 0xFF, 0xEE, 0, 14, 'A', 'd', 'o', 'b', 'e', 0, 100, 0, 0, 0, 0, byte(adobe)) //nolint:gosec // test data
	}
	seg := []byte{8, byte(h >> 8), byte(h), byte(w >> 8), byte(w), byte(len(comps))} //nolint:gosec // big-endian bytes, truncation intended
	for _, c := range comps {
		seg = append(seg, c.id, c.hv, 0)
	}
	out = append(out, 0xFF, sof, 0, byte(len(seg)+2)) //nolint:gosec // short test segment
	out = append(out, seg...)
	return append(out, 0xFF, 0xDA, 0, 2)
}

// webpWith is an extended WebP header claiming a w×h canvas, with an ALPH chunk when alpha.
func webpWith(w, h int, alpha bool) []byte {
	chunk := func(id string, payload []byte) []byte {
		out := binary.LittleEndian.AppendUint32([]byte(id), uint32(len(payload))) //nolint:gosec // test data
		return append(out, payload...)
	}
	vp8x := make([]byte, 10)
	if alpha {
		vp8x[0] = 0x10
	}
	vp8x[4], vp8x[5], vp8x[6] = byte(w-1), byte((w-1)>>8), byte((w-1)>>16) //nolint:gosec // 24-bit fields
	vp8x[7], vp8x[8], vp8x[9] = byte(h-1), byte((h-1)>>8), byte((h-1)>>16) //nolint:gosec // 24-bit fields
	body := append([]byte("WEBP"), chunk("VP8X", vp8x)...)
	if alpha {
		body = append(body, chunk("ALPH", []byte{0, 0})...)
	}
	body = append(body, chunk("VP8 ", make([]byte, 10))...)
	return append(binary.LittleEndian.AppendUint32([]byte("RIFF"), uint32(len(body))), body...) //nolint:gosec // test data
}

// The decode budget counts what the stdlib and vpx decoders promote images to: gray with tRNS
// to NRGBA, RGB JPEGs to RGBA, non-standard JPEG sampling to full planes, WebP alpha.
func TestDecodeCostCountsPromotions(t *testing.T) {
	y, cb, cr := comp{1, 0x11}, comp{2, 0x11}, comp{3, 0x11}
	for name, tc := range map[string]struct {
		data     []byte
		complex_ bool
	}{
		"gray16 png":                 {pngWith(6000, 4000, 16, 0, false), false},
		"gray16 png with tRNS":       {pngWith(6000, 4000, 16, 0, true), true},
		"YCbCr jpeg (JFIF)":          {jpegWith(0xC0, 4000, 4000, true, -1, y, cb, cr), false},
		"RGB jpeg (Adobe transform)": {jpegWith(0xC0, 4000, 4000, false, 0, y, cb, cr), true},
		"RGB jpeg (R, G, B ids)":     {jpegWith(0xC0, 4000, 4000, false, -1, comp{'R', 0x11}, comp{'G', 0x11}, comp{'B', 0x11}), true},
		"progressive 4:2:0 jpeg":     {jpegWith(0xC2, 4000, 3000, true, -1, comp{1, 0x22}, cb, cr), false},
		"progressive flex jpeg":      {jpegWith(0xC2, 4000, 3000, true, -1, y, cb, comp{3, 0x22}), true},
		"lossy webp":                 {webpWith(4000, 4000, false), false},
		"lossy webp with alpha":      {webpWith(4000, 4000, true), true},
	} {
		f, err := Sniff(tc.data)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		_, err = checkHeader(f, tc.data)
		if got := errors.Is(err, ErrTooComplex); got != tc.complex_ || (!got && err != nil) {
			t.Errorf("%s: err = %v, want too complex = %v", name, err, tc.complex_)
		}
	}
}
