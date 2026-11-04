package users

import (
	"bytes"
	"errors"
	"image"
	"image/jpeg"
	_ "image/png" // decoders for uploads
	"slices"

	"golang.org/x/image/draw"
	_ "golang.org/x/image/webp"
)

const (
	// MaxAvatarUpload is the largest accepted upload, in bytes.
	MaxAvatarUpload = 5 << 20
	avatarSize      = 256
	// decoding allocates width*height*4 bytes, so huge dimensions in a small
	// file (a "decompression bomb") are refused before decoding
	maxAvatarPixels = 40_000_000
	avatarQuality   = 85
)

var (
	ErrBadImage      = errors.New("upload a JPEG, PNG or WebP image")
	ErrImageTooLarge = errors.New("image is too large (at most 5 MB and 40 megapixels)")
)

// ProcessAvatar turns an upload into a 256x256 JPEG: the centre square,
// scaled down. Re-encoding drops everything but pixels, including EXIF
// metadata such as the GPS position phones embed in photos.
func ProcessAvatar(raw []byte) ([]byte, error) {
	cfg, format, err := image.DecodeConfig(bytes.NewReader(raw))
	if err != nil || !slices.Contains([]string{"jpeg", "png", "webp"}, format) {
		return nil, ErrBadImage
	}
	if cfg.Width <= 0 || cfg.Height <= 0 || cfg.Width*cfg.Height > maxAvatarPixels {
		return nil, ErrImageTooLarge
	}
	src, _, err := image.Decode(bytes.NewReader(raw))
	if err != nil {
		return nil, ErrBadImage
	}
	b := src.Bounds()
	side := min(b.Dx(), b.Dy())
	crop := image.Rect(0, 0, side, side).Add(image.Pt(b.Min.X+(b.Dx()-side)/2, b.Min.Y+(b.Dy()-side)/2))
	dst := image.NewRGBA(image.Rect(0, 0, avatarSize, avatarSize))
	// transparent PNG areas become white rather than black in the JPEG
	draw.Draw(dst, dst.Bounds(), image.White, image.Point{}, draw.Src)
	draw.CatmullRom.Scale(dst, dst.Bounds(), src, crop, draw.Over, nil)
	var out bytes.Buffer
	if err := jpeg.Encode(&out, dst, &jpeg.Options{Quality: avatarQuality}); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}
