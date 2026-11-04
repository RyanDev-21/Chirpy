package users

import (
	"bytes"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"testing"
)

func encodePNG(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for x := range w {
		img.Set(x, h/2, color.RGBA{R: 255, A: 255})
	}
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func TestProcessAvatar(t *testing.T) {
	out, err := ProcessAvatar(encodePNG(t, 800, 400))
	if err != nil {
		t.Fatal(err)
	}
	img, err := jpeg.Decode(bytes.NewReader(out))
	if err != nil {
		t.Fatal(err)
	}
	if b := img.Bounds(); b.Dx() != avatarSize || b.Dy() != avatarSize {
		t.Fatalf("size %v", b)
	}
	if _, err := ProcessAvatar([]byte("not an image")); !errors.Is(err, ErrBadImage) {
		t.Fatalf("text: %v", err)
	}
	gif := []byte("GIF89a\x01\x00\x01\x00\x00\x00\x00;")
	if _, err := ProcessAvatar(gif); !errors.Is(err, ErrBadImage) {
		t.Fatalf("gif: %v", err)
	}
}

// A tiny PNG header claiming 50000x50000 pixels must be refused without
// decoding (which would allocate ~10 GB).
func TestProcessAvatarRefusesDecompressionBombs(t *testing.T) {
	var b bytes.Buffer
	b.WriteString("\x89PNG\r\n\x1a\n")
	ihdr := make([]byte, 13)
	binary.BigEndian.PutUint32(ihdr[0:], 50000)
	binary.BigEndian.PutUint32(ihdr[4:], 50000)
	ihdr[8], ihdr[9] = 8, 6 // 8-bit RGBA
	chunk := append([]byte("IHDR"), ihdr...)
	_ = binary.Write(&b, binary.BigEndian, uint32(len(ihdr)))
	b.Write(chunk)
	_ = binary.Write(&b, binary.BigEndian, crc32.ChecksumIEEE(chunk))
	if _, err := ProcessAvatar(b.Bytes()); !errors.Is(err, ErrImageTooLarge) {
		t.Fatalf("bomb: %v", err)
	}
}
