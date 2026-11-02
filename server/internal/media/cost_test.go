package media

import (
	"bytes"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"image"
	"image/jpeg"
	"slices"
	"testing"

	"github.com/voidrot/chorecast/server/internal/media/mediatest"
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

// Segments for splicing into JPEGs: an Adobe APP14 with the given color transform (0 makes the
// stdlib decode three components as RGB), a JFIF APP0 and an APP0 that is not JFIF.
var (
	jpegJFIF  = []byte{0xFF, 0xE0, 0, 16, 'J', 'F', 'I', 'F', 0, 1, 1, 0, 0, 1, 0, 1, 0, 0}
	jpegAPP0  = []byte{0xFF, 0xE0, 0, 7, 'X', 'X', 'X', 'X', 0}
	jpegEOI   = []byte{0xFF, 0xD9}
	jpegJunk  = []byte{0x12, 0x34, 0x56}
	jpegStuff = []byte{0xFF, 0x00} // a stuffed 0xFF, which the decoder skips between segments
	jpegFill  = []byte{0xFF, 0xFF, 0xFF}
)

func jpegAdobe(transform byte) []byte {
	return []byte{0xFF, 0xEE, 0, 14, 'A', 'd', 'o', 'b', 'e', 0, 100, 0, 0, 0, 0, transform}
}

// splice returns data with parts inserted at offset at.
func splice(data []byte, at int, parts ...[]byte) []byte {
	return slices.Concat(data[:at:at], slices.Concat(parts...), data[at:])
}

// The stdlib decoder skips stray bytes, "\xff\x00" and fill bytes between segments, and reads
// segments after the scan up to the end of image, so an Adobe marker anywhere it reads still
// makes the image RGB and counts towards the budget.
func TestDecodeCostFindsMarkersTheDecoderReads(t *testing.T) {
	y, cb, cr := comp{1, 0x11}, comp{2, 0x11}, comp{3, 0x11}
	ycbcr := jpegWith(0xC0, 4000, 4000, false, -1, y, cb, cr) // 48 MB as YCbCr, 112 MB as RGB
	jfif := jpegWith(0xC0, 4000, 4000, true, -1, y, cb, cr)
	sos := len(ycbcr) - 4                                    // jpegWith ends with a 4-byte start of scan
	scan := []byte{0x55, 0xFF, 0x00, 0xAA, 0xFF, 0xD0, 0x01} // entropy data with a stuffed byte and a restart
	for name, tc := range map[string]struct {
		data     []byte
		complex_ bool
	}{
		"Adobe RGB after stray bytes":      {splice(ycbcr, sos, jpegJunk, jpegAdobe(0)), true},
		"Adobe RGB after a stuffed 0xFF":   {splice(ycbcr, sos, jpegStuff, jpegAdobe(0)), true},
		"Adobe RGB after fill bytes":       {splice(ycbcr, sos, jpegFill, jpegAdobe(0)), true},
		"Adobe RGB after the scan":         {slices.Concat(ycbcr, scan, jpegJunk, jpegAdobe(0), jpegEOI), true},
		"JFIF replaced by a later APP0":    {splice(jfif, len(jfif)-4, jpegAPP0, jpegAdobe(0)), true},
		"Adobe YCbCr after stray bytes":    {splice(ycbcr, sos, jpegJunk, jpegAdobe(1)), false},
		"JFIF after Adobe RGB":             {splice(ycbcr, sos, jpegJunk, jpegAdobe(0), jpegJFIF), false},
		"Adobe RGB after the end of image": {slices.Concat(ycbcr, scan, jpegEOI, jpegAdobe(0)), false},
		"Adobe RGB after fill bytes and a stuffed byte before a restart": {
			restartJPEG(4000, 4000, 60000, []byte{0xFF, 0xFF, 0x00}, jpegAdobe(0)), true},
	} {
		_, err := checkHeader(FormatJPEG, tc.data)
		if got := errors.Is(err, ErrTooComplex); got != tc.complex_ || (!got && err != nil) {
			t.Errorf("%s: err = %v, want too complex = %v", name, err, tc.complex_)
		}
	}
}

// jpegHeader agrees with the stdlib decoder on whether real JPEGs, with markers spliced in where
// the decoder skips or still reads them, decode as RGB.
func TestJPEGHeaderMatchesDecoder(t *testing.T) {
	base := mediatest.JPEG(t, mediatest.Halves(16, 16)) // SOI, DQT, SOF0, DHT, SOS, data, EOI; no APP0
	afterSOF := segmentEnd(t, base, 0xC0)
	eoi := len(base) - 2
	for name, tc := range map[string]struct {
		data []byte
		rgb  bool
	}{
		"unchanged":                        {base, false},
		"Adobe RGB after stray bytes":      {splice(base, afterSOF, jpegJunk, jpegAdobe(0)), true},
		"Adobe RGB after a stuffed 0xFF":   {splice(base, afterSOF, jpegStuff, jpegAdobe(0)), true},
		"Adobe RGB after fill bytes":       {splice(base, afterSOF, jpegFill, jpegAdobe(0)), true},
		"Adobe RGB after the scan":         {splice(base, eoi, jpegJunk, jpegAdobe(0)), true},
		"Adobe RGB after a restart":        {splice(base, eoi, []byte{0xFF, 0xD3}, jpegAdobe(0)), true},
		"JFIF replaced by a later APP0":    {splice(base, afterSOF, jpegJFIF, jpegAPP0, jpegAdobe(0)), true},
		"Adobe YCbCr after stray bytes":    {splice(base, afterSOF, jpegJunk, jpegAdobe(1)), false},
		"JFIF after Adobe RGB":             {splice(base, afterSOF, jpegAdobe(0), jpegJunk, jpegJFIF), false},
		"Adobe RGB after the end of image": {slices.Concat(base, jpegAdobe(0)), false},
		// With a restart interval the decoder resyncs to each restart marker, skipping
		// "\xff\xff…\x00" there, which its marker loop elsewhere refuses.
		"restarts":                       {restartJPEG(64, 64, 8, nil, nil), false},
		"restarts, Adobe RGB after them": {restartJPEG(64, 64, 8, nil, jpegAdobe(0)), true},
		"restarts, Adobe RGB after FF FF 00": {
			restartJPEG(64, 64, 8, []byte{0xFF, 0xFF, 0x00}, jpegAdobe(0)), true},
		"restarts, Adobe RGB after FF FF FF 00": {
			restartJPEG(64, 64, 8, []byte{0xFF, 0xFF, 0xFF, 0x00}, jpegAdobe(0)), true},
		"restarts, JFIF, Adobe RGB after FF FF 00": {
			splice(restartJPEG(64, 64, 8, []byte{0xFF, 0xFF, 0x00}, jpegAdobe(0)), 2, jpegJFIF), false},
		"restarts, JFIF replaced by an APP0 after FF FF 00": {
			splice(restartJPEG(64, 64, 8, []byte{0xFF, 0xFF, 0x00}, slices.Concat(jpegAPP0, jpegAdobe(0))), 2, jpegJFIF), true},
	} {
		img, err := jpeg.Decode(bytes.NewReader(tc.data))
		if err != nil {
			t.Fatalf("%s: decode: %v", name, err)
		}
		if _, rgb := img.(*image.RGBA); rgb != tc.rgb {
			t.Fatalf("%s: the decoder returned %T, want RGB = %v", name, img, tc.rgb)
		}
		if got := jpegHeader(tc.data).rgb(); got != tc.rgb {
			t.Errorf("%s: jpegHeader RGB = %v, want %v", name, got, tc.rgb)
		}
	}
}

// segmentEnd is the offset just past the first marker segment of a well-formed JPEG.
func segmentEnd(t *testing.T, data []byte, marker byte) int {
	t.Helper()
	for i := 2; i+4 <= len(data) && data[i] == 0xFF; {
		end := i + 2 + int(binary.BigEndian.Uint16(data[i+2:i+4]))
		if data[i+1] == marker {
			return end
		}
		i = end
	}
	t.Fatalf("no segment with marker %#x", marker)
	return 0
}

// restartJPEG is a baseline w×h JPEG with three 1×1-sampled components (ids 1, 2, 3, no JFIF)
// and a restart every ri MCUs. Every block is a zero DC difference and an end of block, each a
// 1-bit code in one-code Huffman tables. junk goes before the first restart marker, trailer
// after the scan.
func restartJPEG(w, h, ri int, junk, trailer []byte) []byte {
	be := func(v int) []byte { return binary.BigEndian.AppendUint16(nil, uint16(v)) } //nolint:gosec // test sizes
	dht := func(class byte) []byte {
		return slices.Concat([]byte{0xFF, 0xC4, 0, 20, class, 1}, make([]byte, 15), []byte{0})
	}
	out := slices.Concat(
		[]byte{0xFF, 0xD8},
		[]byte{0xFF, 0xDB, 0, 67, 0}, bytes.Repeat([]byte{1}, 64),
		[]byte{0xFF, 0xC0, 0, 17, 8}, be(h), be(w), []byte{3, 1, 0x11, 0, 2, 0x11, 0, 3, 0x11, 0},
		dht(0x00), dht(0x10),
		[]byte{0xFF, 0xDD, 0, 4}, be(ri),
		[]byte{0xFF, 0xDA, 0, 12, 3, 1, 0x00, 2, 0x00, 3, 0x00, 0, 63, 0},
	)
	total := ((w + 7) / 8) * ((h + 7) / 8)
	rst := byte(0xD0)
	for done := 0; done < total; {
		n := min(ri, total-done)
		bits := 6 * n // two 1-bit codes per block, three blocks per MCU
		seg := make([]byte, (bits+7)/8)
		if pad := len(seg)*8 - bits; pad > 0 {
			seg[len(seg)-1] = byte(1<<pad - 1) // pad with 1 bits
		}
		out = append(out, seg...)
		done += n
		if done == total {
			break
		}
		if rst == 0xD0 && done == n {
			out = append(out, junk...)
		}
		out = append(out, 0xFF, rst)
		if rst++; rst > 0xD7 {
			rst = 0xD0
		}
	}
	return slices.Concat(out, trailer, jpegEOI)
}
