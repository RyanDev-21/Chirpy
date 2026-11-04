package media

import (
	"bytes"
	"errors"
	"image"
	"image/jpeg"
	_ "image/png" // decoders for thumbnails sent by phones
	"slices"

	"golang.org/x/image/draw"
	_ "golang.org/x/image/webp"
)

const (
	thumbMaxSide = 480
	thumbQuality = 80
	// dimensions are checked before decoding, so a tiny file claiming huge
	// dimensions (a decompression bomb) is refused without allocating
	thumbMaxPixels = 40_000_000
)

var ErrBadThumbnail = errors.New("thumbnail must be a JPEG, PNG or WebP image (at most 2 MB)")

// ProcessThumbnail turns a phone-made video still into a JPEG of at most
// 480 px on its longer side, keeping the shape. Re-encoding keeps only the
// pixels (no EXIF or other metadata, nothing that isn't an image).
func ProcessThumbnail(raw []byte) ([]byte, error) {
	cfg, format, err := image.DecodeConfig(bytes.NewReader(raw))
	if err != nil || !slices.Contains([]string{"jpeg", "png", "webp"}, format) {
		return nil, ErrBadThumbnail
	}
	if cfg.Width <= 0 || cfg.Height <= 0 || cfg.Width*cfg.Height > thumbMaxPixels {
		return nil, ErrBadThumbnail
	}
	src, _, err := image.Decode(bytes.NewReader(raw))
	if err != nil {
		return nil, ErrBadThumbnail
	}
	w, h := cfg.Width, cfg.Height
	if w > thumbMaxSide || h > thumbMaxSide {
		if w >= h {
			w, h = thumbMaxSide, max(1, h*thumbMaxSide/cfg.Width)
		} else {
			w, h = max(1, w*thumbMaxSide/cfg.Height), thumbMaxSide
		}
	}
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.Draw(dst, dst.Bounds(), image.White, image.Point{}, draw.Src)
	draw.CatmullRom.Scale(dst, dst.Bounds(), src, src.Bounds(), draw.Over, nil)
	var out bytes.Buffer
	if err := jpeg.Encode(&out, dst, &jpeg.Options{Quality: thumbQuality}); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}
