package mods

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"io"
	"path/filepath"
	"strings"

	"github.com/diamondburned/gotkit/app/prefs"
)

var enableRandomFilenames = prefs.NewBool(false, prefs.PropMeta{
	Name:        "Randomize Upload Filenames",
	Section:     "Mods",
	Description: "Replace original filenames with random strings before uploading. May violate Discord's Terms of Service.",
})

var enableStripMetadata = prefs.NewBool(false, prefs.PropMeta{
	Name:        "Strip Image Metadata",
	Section:     "Mods",
	Description: "Remove EXIF, GPS, and camera info from JPEG/PNG images before uploading. May violate Discord's Terms of Service.",
})

// RandomizeFilename replaces the filename with a random hex string,
// preserving the original extension and any SPOILER_ prefix.
func RandomizeFilename(name string) string {
	if !enableRandomFilenames.Value() {
		return name
	}

	var prefix string
	if strings.HasPrefix(name, "SPOILER_") {
		prefix = "SPOILER_"
		name = strings.TrimPrefix(name, "SPOILER_")
	}

	ext := filepath.Ext(name)

	b := make([]byte, 8)
	rand.Read(b)
	return prefix + hex.EncodeToString(b) + ext
}

// StripImageMetadata wraps a file's Open function to strip EXIF and other
// metadata from JPEG and PNG images. Non-image files pass through unchanged.
func StripImageMetadata(open func() (io.ReadCloser, error), mimeType string) func() (io.ReadCloser, error) {
	if !enableStripMetadata.Value() {
		return open
	}

	switch {
	case strings.HasPrefix(mimeType, "image/jpeg"), mimeType == "image/jpg":
		return func() (io.ReadCloser, error) {
			rc, err := open()
			if err != nil {
				return nil, err
			}
			data, err := io.ReadAll(rc)
			rc.Close()
			if err != nil {
				return nil, err
			}
			stripped := stripJPEGMetadata(data)
			return io.NopCloser(bytes.NewReader(stripped)), nil
		}
	case mimeType == "image/png":
		return func() (io.ReadCloser, error) {
			rc, err := open()
			if err != nil {
				return nil, err
			}
			data, err := io.ReadAll(rc)
			rc.Close()
			if err != nil {
				return nil, err
			}
			stripped := stripPNGMetadata(data)
			return io.NopCloser(bytes.NewReader(stripped)), nil
		}
	default:
		return open
	}
}

// stripJPEGMetadata removes EXIF (APP1) and other APP markers from JPEG data
// without re-encoding. The actual image data is untouched — no quality loss.
func stripJPEGMetadata(data []byte) []byte {
	if len(data) < 2 || data[0] != 0xFF || data[1] != 0xD8 {
		return data // not a JPEG
	}

	var out []byte
	out = append(out, 0xFF, 0xD8) // SOI marker

	i := 2
	for i < len(data)-1 {
		if data[i] != 0xFF {
			out = append(out, data[i:]...)
			break
		}

		marker := data[i+1]

		// SOS (Start of Scan) — rest is compressed image data, copy all
		if marker == 0xDA {
			out = append(out, data[i:]...)
			break
		}

		// Skip APP1-APP15 markers (EXIF, XMP, ICC profiles with PII, etc.)
		// Keep APP0 (JFIF) since it's required for valid JPEG.
		if marker >= 0xE1 && marker <= 0xEF {
			if i+3 >= len(data) {
				break
			}
			length := int(data[i+2])<<8 | int(data[i+3])
			i += 2 + length
			continue
		}

		// Keep all other markers (DQT, DHT, SOF, APP0, etc.)
		if i+3 >= len(data) {
			out = append(out, data[i:]...)
			break
		}
		length := int(data[i+2])<<8 | int(data[i+3])
		out = append(out, data[i:i+2+length]...)
		i += 2 + length
	}

	return out
}

// stripPNGMetadata removes text and EXIF chunks from PNG data without
// re-encoding. Preserves IHDR, PLTE, IDAT, IEND and other critical chunks.
func stripPNGMetadata(data []byte) []byte {
	// PNG signature: 89 50 4E 47 0D 0A 1A 0A
	if len(data) < 8 || string(data[:4]) != "\x89PNG" {
		return data // not a PNG
	}

	var out []byte
	out = append(out, data[:8]...) // PNG signature

	i := 8
	for i+8 <= len(data) {
		// Each chunk: 4 bytes length + 4 bytes type + data + 4 bytes CRC
		chunkLen := int(data[i])<<24 | int(data[i+1])<<16 | int(data[i+2])<<8 | int(data[i+3])
		chunkType := string(data[i+4 : i+8])
		totalLen := 12 + chunkLen // length field + type + data + CRC

		if i+totalLen > len(data) {
			out = append(out, data[i:]...)
			break
		}

		// Strip metadata chunks, keep everything else
		switch chunkType {
		case "tEXt", "iTXt", "zTXt", "eXIf":
			// Skip these metadata chunks
		default:
			out = append(out, data[i:i+totalLen]...)
		}

		i += totalLen
	}

	return out
}
