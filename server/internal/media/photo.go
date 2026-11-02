package media

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"hash"
	"image"
	stddraw "image/draw"
	"image/jpeg"
	"image/png"

	"github.com/gen2brain/vpx/webp"
	"golang.org/x/image/draw"
)

const (
	// MaxLongEdge is the long edge, in pixels, a stored photo is scaled down to.
	MaxLongEdge = 1600
	// ThumbLongEdge is the long edge of a photo's thumbnail.
	ThumbLongEdge = 320
	// MaxSide caps each side of an upload, and MaxPixels its area. Both are read from the file
	// header before decoding, so a small file that claims a huge or extremely thin image (a
	// decompression bomb) is refused without allocating it. Phone cameras stay below both, and
	// the app scales photos down before uploading.
	MaxSide   = 12_000
	MaxPixels = 24_000_000
	// MaxDecodeBytes caps what decoding may allocate, estimated from the header per format
	// (decodeCost): a progressive or CMYK JPEG, a 16-bit PNG or a lossless WebP needs several
	// times the memory of a baseline JPEG of the same size. Scaling, thumbnail and encoding add
	// about 90 MB on top (they work on images of at most 2×MaxLongEdge). Measured peak RSS for
	// one upload is about 85 MB for a 24 MP baseline JPEG and at most about 220 MB for the worst
	// accepted inputs (progressive 4:4:4 or CMYK JPEG, 16-bit PNG, lossless WebP at the limit).
	MaxDecodeBytes = 128 << 20
	// webpQuality is the lossy WebP quality for photos and thumbnails.
	webpQuality = 80
)

// Errors from DecodePhoto and Encode. Callers map them to API problems.
var (
	ErrUnsupportedFormat = errors.New("media: not a JPEG, PNG or WebP image")
	ErrHEIC              = errors.New("media: HEIC and AVIF images are not supported")
	ErrAnimated          = errors.New("media: animated images are not supported")
	ErrInvalidImage      = errors.New("media: the image could not be decoded")
	ErrTooManyPixels     = errors.New("media: the image is too large")
	ErrTooComplex        = errors.New("media: the image needs too much memory to decode")
)

// Format is an accepted upload format, recognized by its magic bytes.
type Format string

const (
	FormatJPEG Format = "jpeg"
	FormatPNG  Format = "png"
	FormatWebP Format = "webp"
)

// Decoded is an uploaded photo, decoded, scaled and oriented but not yet encoded, so a caller
// can reject it by its hash before paying for the encode.
type Decoded struct {
	Format Format // what was uploaded
	// SHA256 is the hash of the decoded pixels as uploaded, before orientation and scaling:
	// width and height as big-endian uint32, then each row as 8-bit RGBA (alpha-premultiplied).
	// Re-saving the same pixels with other metadata gives the same hash.
	SHA256 [32]byte
	// DHash is a 64-bit difference hash of the oriented photo, for near-duplicate detection.
	DHash uint64

	full, thumb *image.RGBA
}

// Photo is an uploaded photo after processing: re-encoded without any metadata.
type Photo struct {
	Format        Format
	WebP          []byte // the photo, at most MaxLongEdge on its long edge
	Width, Height int
	Thumb         []byte // the thumbnail, at most ThumbLongEdge on its long edge
	ThumbWidth    int
	ThumbHeight   int
	SHA256        [32]byte // see Decoded.SHA256
	DHash         uint64
}

// Sniff identifies the format from the file's magic bytes. The declared Content-Type is not
// trusted.
func Sniff(data []byte) (Format, error) {
	switch {
	case len(data) >= 3 && data[0] == 0xFF && data[1] == 0xD8 && data[2] == 0xFF:
		return FormatJPEG, nil
	case bytes.HasPrefix(data, []byte("\x89PNG\r\n\x1a\n")):
		return FormatPNG, nil
	case len(data) >= 12 && string(data[0:4]) == "RIFF" && string(data[8:12]) == "WEBP":
		return FormatWebP, nil
	case len(data) >= 12 && string(data[4:8]) == "ftyp":
		switch string(data[8:12]) {
		case "heic", "heix", "hevc", "hevx", "heim", "heis", "mif1", "msf1", "avif", "avis":
			return "", ErrHEIC
		}
	}
	return "", ErrUnsupportedFormat
}

// ProcessPhoto is DecodePhoto followed by Encode.
func ProcessPhoto(ctx context.Context, data []byte) (*Photo, error) {
	d, err := DecodePhoto(ctx, data)
	if err != nil {
		return nil, err
	}
	return d.Encode(ctx)
}

// DecodePhoto validates and decodes an uploaded image, hashes it, scales it down to
// MaxLongEdge, applies its EXIF orientation and makes the thumbnail. Everything runs on the
// calling goroutine; a decoder panic becomes ErrInvalidImage. It stops between stages when ctx
// is done.
func DecodePhoto(ctx context.Context, data []byte) (d *Decoded, err error) {
	defer recoverInto(&err)
	format, err := Sniff(data)
	if err != nil {
		return nil, err
	}
	cfg, err := checkHeader(format, data)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	src, err := decode(format, data)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalidImage, err)
	}
	b := src.Bounds()
	if b.Dx() != cfg.Width || b.Dy() != cfg.Height {
		return nil, ErrInvalidImage // the header lied about the size
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	d = &Decoded{Format: format}
	h := sha256.New()
	var dims [8]byte
	binary.BigEndian.PutUint32(dims[0:4], uint32(b.Dx())) //nolint:gosec // bounded by MaxSide
	binary.BigEndian.PutUint32(dims[4:8], uint32(b.Dy())) //nolint:gosec // bounded by MaxSide
	h.Write(dims[:])
	scaled := scaleDown(src, MaxLongEdge, h) // also feeds every source row to h
	h.Sum(d.SHA256[:0])
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	d.full = orient(scaled, orientation(format, data))
	d.thumb = scaleDown(d.full, ThumbLongEdge, nil)
	d.DHash = dHash(d.thumb)
	return d, nil
}

// Encode encodes the photo and its thumbnail as lossy WebP. Only pixels are encoded, so the
// output carries no EXIF, GPS, XMP or ICC data.
func (d *Decoded) Encode(ctx context.Context) (p *Photo, err error) {
	defer recoverInto(&err)
	p = &Photo{Format: d.Format, SHA256: d.SHA256, DHash: d.DHash,
		Width: d.full.Rect.Dx(), Height: d.full.Rect.Dy(), ThumbWidth: d.thumb.Rect.Dx(), ThumbHeight: d.thumb.Rect.Dy()}
	if p.WebP, err = encode(d.full); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if p.Thumb, err = encode(d.thumb); err != nil {
		return nil, err
	}
	return p, nil
}

// recoverInto turns a panic in a decoder or encoder into ErrInvalidImage.
func recoverInto(err *error) {
	if r := recover(); r != nil {
		*err = fmt.Errorf("%w: decoder panic: %v", ErrInvalidImage, r)
	}
}

// checkHeader reads the image's dimensions from its header and refuses images that are too
// large or would need too much memory to decode, and animations.
func checkHeader(f Format, data []byte) (image.Config, error) {
	if f == FormatWebP && webpAnimated(data) {
		return image.Config{}, ErrAnimated
	}
	r := bytes.NewReader(data)
	var cfg image.Config
	var err error
	switch f {
	case FormatJPEG:
		cfg, err = jpeg.DecodeConfig(r)
	case FormatPNG:
		cfg, err = png.DecodeConfig(r)
	default:
		cfg, err = webp.DecodeConfig(r)
	}
	if err != nil {
		return cfg, fmt.Errorf("%w: %w", ErrInvalidImage, err)
	}
	if cfg.Width <= 0 || cfg.Height <= 0 {
		return cfg, ErrInvalidImage
	}
	if cfg.Width > MaxSide || cfg.Height > MaxSide || int64(cfg.Width)*int64(cfg.Height) > MaxPixels {
		return cfg, ErrTooManyPixels
	}
	if decodeCost(f, data, cfg) > MaxDecodeBytes {
		return cfg, ErrTooComplex
	}
	return cfg, nil
}

// decodeCost estimates, in bytes, what decoding the image allocates, from its header.
func decodeCost(f Format, data []byte, cfg image.Config) int64 {
	px := int64(cfg.Width) * int64(cfg.Height)
	switch f {
	case FormatJPEG:
		return jpegDecodeCost(data, cfg)
	case FormatPNG:
		return pngDecodeCost(data, px)
	default:
		// Measured: a lossy WebP decodes to 4:2:0 YUV plus alpha and working buffers (about
		// 4 B/px); a lossless one to NRGBA plus the decoder's 32-bit ARGB buffers (about 10 B/px).
		if webpHasChunk(data, "VP8L") {
			return 10 * px
		}
		return 4 * px
	}
}

// jpegDecodeCost sums the component planes the stdlib decoder allocates (at their subsampled
// size), the CMYK image it converts four-component files into, and for progressive files the
// 32-bit coefficient blocks it keeps for every component.
func jpegDecodeCost(data []byte, cfg image.Config) int64 {
	progressive, comps := jpegFrame(data)
	if len(comps) == 0 {
		return int64(cfg.Width) * int64(cfg.Height) * 24 // unknown layout: assume the worst
	}
	hmax, vmax := 1, 1
	for _, c := range comps {
		hmax, vmax = max(hmax, c.h), max(vmax, c.v)
	}
	var planes int64
	for _, c := range comps {
		// Rounded up to whole 8×8 blocks, as the decoder allocates them.
		w := (int64(cfg.Width)*int64(c.h)/int64(hmax) + 7) &^ 7
		h := (int64(cfg.Height)*int64(c.v)/int64(vmax) + 7) &^ 7
		planes += w * h
	}
	cost := planes
	if len(comps) == 4 {
		cost += 4 * int64(cfg.Width) * int64(cfg.Height) // the converted *image.CMYK
	}
	if progressive {
		cost += 4 * planes // a [64]int32 block per 8×8 block per component
	}
	return cost
}

type jpegComponent struct{ h, v int }

// jpegFrame reads the start-of-frame segment: whether the file is progressive and each
// component's sampling factors. It returns no components if there is no valid SOF.
func jpegFrame(data []byte) (progressive bool, comps []jpegComponent) {
	i := 2
	for i+4 <= len(data) {
		if data[i] != 0xFF {
			return false, nil
		}
		m := data[i+1]
		if m == 0xFF {
			i++
			continue
		}
		if m == 0xD9 || m == 0xDA {
			return false, nil
		}
		if m == 0x01 || (m >= 0xD0 && m <= 0xD7) {
			i += 2
			continue
		}
		n := int(binary.BigEndian.Uint16(data[i+2 : i+4]))
		if n < 2 || n > len(data)-i-2 {
			return false, nil
		}
		if m >= 0xC0 && m <= 0xCF && m != 0xC4 && m != 0xC8 && m != 0xCC {
			seg := data[i+4 : i+2+n]
			if len(seg) < 6 {
				return false, nil
			}
			nc := int(seg[5])
			if nc < 1 || nc > 4 || len(seg) < 6+3*nc {
				return false, nil
			}
			for k := range nc {
				s := seg[6+3*k+1]
				h, v := int(s>>4), int(s&0x0F)
				if h < 1 || v < 1 {
					return false, nil
				}
				comps = append(comps, jpegComponent{h, v})
			}
			return m == 0xC2 || m == 0xC6 || m == 0xCA || m == 0xCE, comps
		}
		i += 2 + n
	}
	return false, nil
}

// pngDecodeCost is the decoded image's size from the IHDR bit depth and color type, doubled
// for interlaced files (the decoder fills each pass's image before merging).
func pngDecodeCost(data []byte, px int64) int64 {
	if len(data) < 29 {
		return px * 8
	}
	depth, colorType, interlace := data[24], data[25], data[28]
	var bpp int64
	switch colorType {
	case 0: // gray
		bpp = 1
	case 3: // paletted
		bpp = 1
	default: // RGB, gray + alpha, RGBA: decoded to 4 channels
		bpp = 4
	}
	if depth == 16 {
		bpp *= 2
	}
	cost := bpp * px
	if interlace != 0 {
		cost *= 2
	}
	return cost
}

// webpAnimated reports whether a WebP file is an animation: an extended (VP8X) header with the
// animation flag, or any ANIM or ANMF chunk.
func webpAnimated(data []byte) bool {
	if len(data) >= 21 && string(data[12:16]) == "VP8X" && data[20]&0x02 != 0 {
		return true
	}
	return webpHasChunk(data, "ANIM") || webpHasChunk(data, "ANMF")
}

// webpHasChunk reports whether the WebP file's top-level chunk list has a chunk with id.
func webpHasChunk(data []byte, id string) bool {
	for i := 12; i+8 <= len(data); {
		if string(data[i:i+4]) == id {
			return true
		}
		n := int(binary.LittleEndian.Uint32(data[i+4 : i+8]))
		if n < 0 || n > len(data)-i-8 {
			return false
		}
		i += 8 + n + n%2
	}
	return false
}

func decode(f Format, data []byte) (image.Image, error) {
	r := bytes.NewReader(data)
	switch f {
	case FormatJPEG:
		return jpeg.Decode(r)
	case FormatPNG:
		return png.Decode(r)
	default:
		// One thread, so a decoder panic happens on this goroutine and is recovered. Orientation
		// is applied by orient, as for JPEG, so the hash is of the pixels as stored.
		return webp.Decode(r, webp.Options{Threads: 1})
	}
}

func encode(m *image.RGBA) ([]byte, error) {
	var buf bytes.Buffer
	if err := webp.Encode(&buf, m, webp.EncodeOptions{Quality: webpQuality, Method: webp.DefaultMethod, Threads: 1}); err != nil {
		return nil, fmt.Errorf("encode webp: %w", err)
	}
	return buf.Bytes(), nil
}

// fit scales w×h down so its long edge is at most edge, keeping the aspect ratio. It never
// scales up.
func fit(w, h, edge int) (int, int) {
	if w <= edge && h <= edge {
		return w, h
	}
	if w >= h {
		return edge, max(1, (h*edge+w/2)/w)
	}
	return max(1, (w*edge+h/2)/h), edge
}

// scaleDown returns src scaled so its long edge is at most edge (never up), as RGBA. It reads
// src a row at a time, converting each to RGBA and writing it to rows when that isn't nil, so
// the source is never copied whole.
//
// When src is at least twice the target in both directions it averages boxes of source pixels
// down to twice the target size, then 2×2 blocks down to the target. That needs no buffer
// larger than the 2× image, unlike x/image's kernel scalers, whose working buffer is
// 32 bytes × target width × source height. Smaller reductions use bilinear sampling, which
// doesn't alias below a factor of 2.
func scaleDown(src image.Image, edge int, rows hash.Hash) *image.RGBA {
	b := src.Bounds()
	sw, sh := b.Dx(), b.Dy()
	dw, dh := fit(sw, sh, edge)
	row := image.NewRGBA(image.Rect(0, 0, sw, 1))
	readRow := func(y int) []uint8 {
		stddraw.Draw(row, row.Rect, src, image.Pt(b.Min.X, b.Min.Y+y), stddraw.Src)
		if rows != nil {
			rows.Write(row.Pix)
		}
		return row.Pix
	}

	if sw < 2*dw || sh < 2*dh {
		dst := image.NewRGBA(image.Rect(0, 0, dw, dh))
		if dw == sw && dh == sh {
			for y := range sh {
				copy(dst.Pix[y*dst.Stride:], readRow(y))
			}
			return dst
		}
		if rows != nil {
			for y := range sh {
				readRow(y)
			}
		}
		draw.ApproxBiLinear.Scale(dst, dst.Rect, src, b, draw.Src, nil)
		return dst
	}

	// Box average to mw×mh = twice the target. Source column x falls in box column x*mw/sw.
	mw, mh := 2*dw, 2*dh
	mid := image.NewRGBA(image.Rect(0, 0, mw, mh))
	col := make([]int, sw)
	colN := make([]uint64, mw)
	for x := range sw {
		col[x] = x * mw / sw
		colN[col[x]]++
	}
	acc := make([]uint64, 4*mw)
	flush := func(my int, rowsIn uint64) {
		out := mid.Pix[my*mid.Stride:]
		for mx := range mw {
			n := colN[mx] * rowsIn
			for c := range 4 {
				out[4*mx+c] = uint8((acc[4*mx+c] + n/2) / n) //nolint:gosec // an average of bytes
				acc[4*mx+c] = 0
			}
		}
	}
	cur, rowsIn := 0, uint64(0)
	for y := range sh {
		my := y * mh / sh
		if my != cur {
			flush(cur, rowsIn)
			cur, rowsIn = my, 0
		}
		px := readRow(y)
		for x := range sw {
			i, j := 4*col[x], 4*x
			acc[i] += uint64(px[j])
			acc[i+1] += uint64(px[j+1])
			acc[i+2] += uint64(px[j+2])
			acc[i+3] += uint64(px[j+3])
		}
		rowsIn++
	}
	flush(cur, rowsIn)

	dst := image.NewRGBA(image.Rect(0, 0, dw, dh))
	for y := range dh {
		a, c := mid.Pix[2*y*mid.Stride:], mid.Pix[(2*y+1)*mid.Stride:]
		out := dst.Pix[y*dst.Stride:]
		for x := range dw {
			for k := range 4 {
				i := 8*x + k
				s := uint16(a[i]) + uint16(a[i+4]) + uint16(c[i]) + uint16(c[i+4])
				out[4*x+k] = uint8((s + 2) / 4) //nolint:gosec // an average of bytes
			}
		}
	}
	return dst
}

// dHash is the 64-bit difference hash: the image scaled to 9×8 in grayscale, one bit per
// horizontally adjacent pair, set when the left pixel is brighter.
func dHash(m *image.RGBA) uint64 {
	small := image.NewRGBA(image.Rect(0, 0, 9, 8))
	draw.BiLinear.Scale(small, small.Rect, m, m.Rect, draw.Src, nil)
	var gray [8][9]int
	for y := range 8 {
		for x := range 9 {
			i := small.PixOffset(x, y)
			r, g, b := int(small.Pix[i]), int(small.Pix[i+1]), int(small.Pix[i+2])
			gray[y][x] = 299*r + 587*g + 114*b
		}
	}
	var hash uint64
	for y := range 8 {
		for x := range 8 {
			hash <<= 1
			if gray[y][x] > gray[y][x+1] {
				hash |= 1
			}
		}
	}
	return hash
}

// orientation is the EXIF orientation (1–8) the image asks to be displayed with; 1 when absent.
// For WebP only the raw EXIF chunk is taken from the decoder; it's parsed by the bounded
// tiffOrientation, not the decoder's full EXIF parser.
func orientation(f Format, data []byte) int {
	switch f {
	case FormatJPEG:
		return jpegOrientation(data)
	case FormatWebP:
		if raw, err := webp.RawExif(bytes.NewReader(data)); err == nil {
			return tiffOrientation(raw)
		}
	}
	return 1
}

// orient returns m transformed for EXIF orientation o, so it displays upright.
func orient(m *image.RGBA, o int) *image.RGBA {
	if o < 2 || o > 8 {
		return m
	}
	w, h := m.Rect.Dx(), m.Rect.Dy()
	ow, oh := w, h
	if o >= 5 {
		ow, oh = h, w
	}
	out := image.NewRGBA(image.Rect(0, 0, ow, oh))
	for y := range oh {
		for x := range ow {
			var sx, sy int
			switch o {
			case 2: // mirrored horizontally
				sx, sy = w-1-x, y
			case 3: // rotated 180°
				sx, sy = w-1-x, h-1-y
			case 4: // mirrored vertically
				sx, sy = x, h-1-y
			case 5: // transposed
				sx, sy = y, x
			case 6: // needs a 90° clockwise turn
				sx, sy = y, h-1-x
			case 7: // transversed
				sx, sy = w-1-y, h-1-x
			case 8: // needs a 90° counter-clockwise turn
				sx, sy = w-1-y, x
			}
			si, di := m.PixOffset(m.Rect.Min.X+sx, m.Rect.Min.Y+sy), out.PixOffset(x, y)
			copy(out.Pix[di:di+4], m.Pix[si:si+4])
		}
	}
	return out
}
