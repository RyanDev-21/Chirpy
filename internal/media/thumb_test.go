package media

import (
	"bytes"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"image"
	"image/jpeg"
	"image/png"
	"testing"
)

func pngOf(t *testing.T, w, h int) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := png.Encode(&b, image.NewRGBA(image.Rect(0, 0, w, h))); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func TestThumbnailKeepsShape(t *testing.T) {
	for _, c := range []struct{ w, h, ww, wh int }{
		{1920, 1080, 480, 270}, // landscape video
		{1080, 1920, 270, 480}, // portrait
		{320, 240, 320, 240},   // already small: not enlarged
	} {
		out, err := ProcessThumbnail(pngOf(t, c.w, c.h))
		if err != nil {
			t.Fatal(err)
		}
		img, err := jpeg.Decode(bytes.NewReader(out))
		if err != nil {
			t.Fatal(err)
		}
		if b := img.Bounds(); b.Dx() != c.ww || b.Dy() != c.wh {
			t.Errorf("%dx%d -> %v, want %dx%d", c.w, c.h, b, c.ww, c.wh)
		}
	}
}

func TestThumbnailRefusesBadInput(t *testing.T) {
	if _, err := ProcessThumbnail([]byte("<html>not an image</html>")); !errors.Is(err, ErrBadThumbnail) {
		t.Fatalf("html: %v", err)
	}
	// a tiny PNG header claiming 60000x60000 must be refused before decoding
	var b bytes.Buffer
	b.WriteString("\x89PNG\r\n\x1a\n")
	ihdr := make([]byte, 13)
	binary.BigEndian.PutUint32(ihdr[0:], 60000)
	binary.BigEndian.PutUint32(ihdr[4:], 60000)
	ihdr[8], ihdr[9] = 8, 6
	chunk := append([]byte("IHDR"), ihdr...)
	_ = binary.Write(&b, binary.BigEndian, uint32(len(ihdr)))
	b.Write(chunk)
	_ = binary.Write(&b, binary.BigEndian, crc32.ChecksumIEEE(chunk))
	if _, err := ProcessThumbnail(b.Bytes()); !errors.Is(err, ErrBadThumbnail) {
		t.Fatalf("bomb: %v", err)
	}
}
