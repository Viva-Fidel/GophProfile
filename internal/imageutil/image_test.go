package imageutil

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"testing"

	"gophprofile/internal/domain"
)

func pngBytes(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{R: 10, G: 20, B: 30, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func jpegBytes(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 8, 8))
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 80}); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestDetectPNG(t *testing.T) {
	mime, w, h, err := Detect(pngBytes(t, 12, 18))
	if err != nil || mime != "image/png" || w != 12 || h != 18 {
		t.Fatal(err, mime, w, h)
	}
}

func TestDetectJPEG(t *testing.T) {
	mime, _, _, err := Detect(jpegBytes(t))
	if err != nil || mime != "image/jpeg" {
		t.Fatal(err, mime)
	}
}

func TestDetectInvalid(t *testing.T) {
	if _, _, _, err := Detect([]byte("not an image")); err != domain.ErrInvalidFormat {
		t.Fatal(err)
	}
}

func TestResize(t *testing.T) {
	data, err := Resize(pngBytes(t, 40, 20), 100, 100)
	if err != nil || len(data) == 0 {
		t.Fatal(err, len(data))
	}
	mime, w, h, err := Detect(data)
	if err != nil || mime != "image/jpeg" || w != 100 || h != 100 {
		t.Fatal(err, mime, w, h)
	}
}

func TestConvert(t *testing.T) {
	src := pngBytes(t, 8, 8)
	data, mime, err := Convert(src, "jpeg")
	if err != nil || mime != "image/jpeg" || len(data) == 0 {
		t.Fatal(err, mime)
	}
	data, mime, err = Convert(src, "png")
	if err != nil || mime != "image/png" {
		t.Fatal(err, mime)
	}
	data, mime, err = Convert(src, "webp")
	if err != nil || mime != "image/jpeg" {
		t.Fatal(err, mime)
	}
	if _, _, err := Convert(src, "gif"); err != domain.ErrInvalidFormatParam {
		t.Fatal(err)
	}
	if _, _, err := Convert(src, ""); err != domain.ErrInvalidFormatParam {
		t.Fatal(err)
	}
}

func TestParseSize(t *testing.T) {
	_, _, original, err := ParseSize("")
	if err != nil || !original {
		t.Fatal(err, original)
	}
	w, h, original, err := ParseSize(domain.Size100)
	if err != nil || original || w != 100 || h != 100 {
		t.Fatal(err, w, h, original)
	}
	if _, _, _, err := ParseSize("50x50"); err != domain.ErrInvalidSize {
		t.Fatal(err)
	}
}

func TestIsSupportedMIME(t *testing.T) {
	if !IsSupportedMIME("image/jpeg") || IsSupportedMIME("image/gif") {
		t.Fatal("mime checks")
	}
}

func TestFormatToMIME(t *testing.T) {
	if mime, ok := formatToMIME("jpg"); !ok || mime != "image/jpeg" {
		t.Fatal(mime, ok)
	}
	if _, ok := formatToMIME("gif"); ok {
		t.Fatal("gif should be unsupported")
	}
}
