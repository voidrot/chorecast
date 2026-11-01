package media

import (
	"bytes"
	"encoding/binary"
)

// jpegOrientation reads the EXIF orientation tag (0x0112) from a JPEG's APP1 segment. It returns
// 1 (upright) when the file has no EXIF, no orientation tag or a malformed one. Every offset is
// bounds-checked, since the bytes come from an upload.
func jpegOrientation(data []byte) int {
	i := 2 // after SOI
	for i+4 <= len(data) {
		if data[i] != 0xFF {
			return 1
		}
		marker := data[i+1]
		if marker == 0xFF { // fill byte
			i++
			continue
		}
		if marker == 0xDA || marker == 0xD9 { // start of scan or end of image: no more headers
			return 1
		}
		if marker == 0x01 || (marker >= 0xD0 && marker <= 0xD7) { // markers without a length
			i += 2
			continue
		}
		n := int(binary.BigEndian.Uint16(data[i+2 : i+4]))
		if n < 2 || i+2+n > len(data) {
			return 1
		}
		seg := data[i+4 : i+2+n]
		if marker == 0xE1 && bytes.HasPrefix(seg, []byte("Exif\x00\x00")) {
			return tiffOrientation(seg[6:])
		}
		i += 2 + n
	}
	return 1
}

// tiffOrientation finds the orientation tag in IFD0 of a TIFF (EXIF) block.
func tiffOrientation(t []byte) int {
	if len(t) < 8 {
		return 1
	}
	var bo binary.ByteOrder
	switch string(t[0:2]) {
	case "II":
		bo = binary.LittleEndian
	case "MM":
		bo = binary.BigEndian
	default:
		return 1
	}
	if bo.Uint16(t[2:4]) != 42 {
		return 1
	}
	off := int(bo.Uint32(t[4:8]))
	if off < 8 || off+2 > len(t) {
		return 1
	}
	count := int(bo.Uint16(t[off : off+2]))
	for e := range count {
		p := off + 2 + 12*e
		if p+12 > len(t) {
			return 1
		}
		if bo.Uint16(t[p:p+2]) != 0x0112 {
			continue
		}
		// Type SHORT (3), one value, stored in the first two bytes of the value field.
		if bo.Uint16(t[p+2:p+4]) != 3 || bo.Uint32(t[p+4:p+8]) != 1 {
			return 1
		}
		if o := int(bo.Uint16(t[p+8 : p+10])); o >= 1 && o <= 8 {
			return o
		}
		return 1
	}
	return 1
}
