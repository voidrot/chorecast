package media

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
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
	// MaxPixels caps the decoded image area (40 MP, about 60 MB as decoded JPEG planes). It is
	// checked against the file header before decoding, so a small file that claims a huge image
	// (a decompression bomb) is refused without allocating it. Phone cameras stay below it, and
	// the app scales photos down before uploading.
	MaxPixels = 40_000_000
	// webpQuality is the lossy WebP quality for photos and thumbnails.
	webpQuality = 80
)

// Errors from ProcessPhoto. Callers map them to API problems.
var (
	ErrUnsupportedFormat = errors.New("media: not a JPEG, PNG or WebP image")
	ErrHEIC              = errors.New("media: HEIC and AVIF images are not supported")
	ErrInvalidImage      = errors.New("media: the image could not be decoded")
	ErrTooManyPixels     = errors.New("media: the image has too many pixels")
)

// Format is an accepted upload format, recognized by its magic bytes.
type Format string

const (
	FormatJPEG Format = "jpeg"
	FormatPNG  Format = "png"
	FormatWebP Format = "webp"
)

// Photo is an uploaded photo after processing: re-encoded without any metadata.
type Photo struct {
	Format        Format // what was uploaded
	WebP          []byte // the photo, at most MaxLongEdge on its long edge
	Width, Height int
	Thumb         []byte // the thumbnail, at most ThumbLongEdge on its long edge
	ThumbWidth    int
	ThumbHeight   int
	// SHA256 is the hash of the decoded pixels as uploaded, before orientation and scaling:
	// width and height as big-endian uint32, then each row as 8-bit RGBA (alpha-premultiplied).
	// Re-saving the same pixels with other metadata gives the same hash.
	SHA256 [32]byte
	// DHash is a 64-bit difference hash of the oriented photo, for near-duplicate detection.
	DHash uint64
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

// ProcessPhoto validates and decodes an uploaded image, applies its EXIF orientation, scales it
// down to MaxLongEdge and re-encodes it, and its thumbnail, as WebP. The output carries no EXIF,
// GPS, XMP or ICC data: only pixels are encoded.
func ProcessPhoto(data []byte) (*Photo, error) {
	format, err := Sniff(data)
	if err != nil {
		return nil, err
	}
	cfg, err := decodeConfig(format, data)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalidImage, err)
	}
	if cfg.Width <= 0 || cfg.Height <= 0 {
		return nil, ErrInvalidImage
	}
	if int64(cfg.Width)*int64(cfg.Height) > MaxPixels {
		return nil, ErrTooManyPixels
	}
	src, err := decode(format, data)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalidImage, err)
	}
	b := src.Bounds()
	if b.Dx() != cfg.Width || b.Dy() != cfg.Height {
		return nil, ErrInvalidImage // the header lied about the size
	}

	p := &Photo{Format: format, SHA256: pixelHash(src)}
	o := orientation(format, data)

	// Scale first (in the stored orientation), then orient the smaller image.
	w, h := fit(b.Dx(), b.Dy(), MaxLongEdge)
	scaled := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.CatmullRom.Scale(scaled, scaled.Bounds(), src, b, draw.Src, nil)
	full := orient(scaled, o)

	tw, th := fit(full.Rect.Dx(), full.Rect.Dy(), ThumbLongEdge)
	thumb := image.NewRGBA(image.Rect(0, 0, tw, th))
	draw.CatmullRom.Scale(thumb, thumb.Bounds(), full, full.Rect, draw.Src, nil)
	p.DHash = dHash(thumb)

	if p.WebP, err = encode(full); err != nil {
		return nil, err
	}
	if p.Thumb, err = encode(thumb); err != nil {
		return nil, err
	}
	p.Width, p.Height = full.Rect.Dx(), full.Rect.Dy()
	p.ThumbWidth, p.ThumbHeight = tw, th
	return p, nil
}

func decodeConfig(f Format, data []byte) (image.Config, error) {
	r := bytes.NewReader(data)
	switch f {
	case FormatJPEG:
		return jpeg.DecodeConfig(r)
	case FormatPNG:
		return png.DecodeConfig(r)
	default:
		return webp.DecodeConfig(r)
	}
}

func decode(f Format, data []byte) (image.Image, error) {
	r := bytes.NewReader(data)
	switch f {
	case FormatJPEG:
		return jpeg.Decode(r)
	case FormatPNG:
		return png.Decode(r)
	default:
		// Orientation is applied by orient, as for JPEG, so the hash is of the pixels as stored.
		// An animated WebP decodes to its first frame.
		return webp.Decode(r, webp.Options{FrameSizeLimit: MaxPixels})
	}
}

func encode(m *image.RGBA) ([]byte, error) {
	var buf bytes.Buffer
	if err := webp.Encode(&buf, m, webp.EncodeOptions{Quality: webpQuality, Method: webp.DefaultMethod}); err != nil {
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

// pixelHash hashes the image's dimensions and its rows converted to 8-bit RGBA, a row at a time
// so a large photo isn't copied whole.
func pixelHash(m image.Image) [32]byte {
	b := m.Bounds()
	h := sha256.New()
	var dims [8]byte
	binary.BigEndian.PutUint32(dims[0:4], uint32(b.Dx())) //nolint:gosec // bounded by MaxPixels
	binary.BigEndian.PutUint32(dims[4:8], uint32(b.Dy())) //nolint:gosec // bounded by MaxPixels
	h.Write(dims[:])
	row := image.NewRGBA(image.Rect(0, 0, b.Dx(), 1))
	for y := b.Min.Y; y < b.Max.Y; y++ {
		stddraw.Draw(row, row.Rect, m, image.Pt(b.Min.X, y), stddraw.Src)
		h.Write(row.Pix)
	}
	var sum [32]byte
	h.Sum(sum[:0])
	return sum
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
func orientation(f Format, data []byte) int {
	switch f {
	case FormatJPEG:
		return jpegOrientation(data)
	case FormatWebP:
		if x, err := webp.DecodeExif(bytes.NewReader(data)); err == nil && x.Orientation >= 1 && x.Orientation <= 8 {
			return x.Orientation
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
