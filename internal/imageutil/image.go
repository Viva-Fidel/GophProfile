// Package imageutil определяет формат изображений и создаёт миниатюры.
package imageutil

import (
	"bytes"
	"fmt"
	"image"
	_ "image/gif"
	"image/jpeg"
	"image/png"
	"strings"

	"github.com/disintegration/imaging"
	"gophprofile/internal/domain"

	_ "golang.org/x/image/webp"
)

var supportedMIMEs = map[string]string{
	"image/jpeg": "image/jpeg",
	"image/jpg":  "image/jpeg",
	"image/png":  "image/png",
	"image/webp": "image/webp",
}

// Detect определяет MIME-тип и размеры изображения.
func Detect(data []byte) (mimeType string, width, height int, err error) {
	cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return "", 0, 0, domain.ErrInvalidFormat
	}
	mime, ok := formatToMIME(format)
	if !ok {
		return "", 0, 0, domain.ErrInvalidFormat
	}
	return mime, cfg.Width, cfg.Height, nil
}

// formatToMIME сопоставляет формат image.Decode с MIME-типом.
func formatToMIME(format string) (string, bool) {
	switch strings.ToLower(format) {
	case "jpeg", "jpg":
		return "image/jpeg", true
	case "png":
		return "image/png", true
	case "webp":
		return "image/webp", true
	default:
		return "", false
	}
}

// IsSupportedMIME проверяет, поддерживается ли MIME-тип загрузки.
func IsSupportedMIME(mime string) bool {
	_, ok := supportedMIMEs[strings.ToLower(mime)]
	return ok
}

// Resize создаёт JPEG-миниатюру заданного размера.
func Resize(data []byte, width, height int) ([]byte, error) {
	img, err := decode(data)
	if err != nil {
		return nil, err
	}
	thumb := imaging.Fill(img, width, height, imaging.Center, imaging.Lanczos)
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, thumb, &jpeg.Options{Quality: 85}); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// Convert перекодирует изображение в jpeg или png.
func Convert(data []byte, format string) ([]byte, string, error) {
	format = strings.ToLower(strings.TrimSpace(format))
	if format == "" {
		return nil, "", domain.ErrInvalidFormatParam
	}
	img, err := decode(data)
	if err != nil {
		return nil, "", err
	}
	var buf bytes.Buffer
	switch format {
	case "jpeg", "jpg":
		if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 85}); err != nil {
			return nil, "", err
		}
		return buf.Bytes(), "image/jpeg", nil
	case "png":
		if err := png.Encode(&buf, img); err != nil {
			return nil, "", err
		}
		return buf.Bytes(), "image/png", nil
	case "webp":
		if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 85}); err != nil {
			return nil, "", err
		}
		return buf.Bytes(), "image/jpeg", nil
	default:
		return nil, "", domain.ErrInvalidFormatParam
	}
}

// decode декодирует изображение из байтов.
func decode(data []byte) (image.Image, error) {
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("%w: %v", domain.ErrInvalidFormat, err)
	}
	return img, nil
}

// ParseSize разбирает query-параметр size.
func ParseSize(size string) (w, h int, original bool, err error) {
	switch size {
	case "", domain.SizeOriginal:
		return 0, 0, true, nil
	case domain.Size100:
		return 100, 100, false, nil
	case domain.Size300:
		return 300, 300, false, nil
	default:
		return 0, 0, false, domain.ErrInvalidSize
	}
}
