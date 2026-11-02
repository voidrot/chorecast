package media

import (
	"encoding/binary"
	"testing"
)

// A TIFF block whose IFD0 offset is huge must not panic. On 32-bit platforms the old check
// (off+2 > len) overflowed for offsets near 2³¹ and let a slice past the end through.
func TestTiffOrientationHugeOffsets(t *testing.T) {
	for _, off := range []uint32{0x7FFFFFFE, 0x7FFFFFFF, 0x80000000, 0xFFFFFFFE, 0xFFFFFFFF, 22, 23} {
		tiff := []byte("II*\x00\x00\x00\x00\x00" + "\x01\x00\x12\x01\x03\x00\x01\x00\x00\x00\x06\x00\x00\x00")
		binary.LittleEndian.PutUint32(tiff[4:8], off)
		if o := tiffOrientation(tiff); o != 1 {
			t.Errorf("offset %#x: orientation %d, want 1", off, o)
		}
	}
	// The same block with a valid offset is read.
	tiff := []byte("II*\x00\x08\x00\x00\x00" + "\x01\x00\x12\x01\x03\x00\x01\x00\x00\x00\x06\x00\x00\x00")
	if o := tiffOrientation(tiff); o != 6 {
		t.Errorf("valid block: orientation %d, want 6", o)
	}
}

func TestJPEGOrientationTruncatedSegments(t *testing.T) {
	for _, b := range [][]byte{
		{0xFF, 0xD8, 0xFF, 0xE1, 0xFF, 0xFF},           // length past the end
		{0xFF, 0xD8, 0xFF, 0xE1, 0x00, 0x01},           // length below 2
		{0xFF, 0xD8, 0xFF, 0xE1, 0x00, 0x08, 'E', 'x'}, // truncated
	} {
		if o := jpegOrientation(b); o != 1 {
			t.Errorf("% x: orientation %d", b, o)
		}
	}
}
