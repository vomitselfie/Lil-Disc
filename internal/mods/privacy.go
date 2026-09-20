package mods

import (
	"bytes"
	"crypto/rand"
	"fmt"
	"io"
	"math/big"
	"path/filepath"
	"strings"
	"time"

	"github.com/diamondburned/gotkit/app/prefs"
)

var enableRandomFilenames = prefs.NewBool(false, prefs.PropMeta{
	Name:    "Randomize Upload Filenames",
	Section: "Mods",
	Description: "Replace original filenames before uploading with ones in the " +
		"style a camera or screenshot tool produces, such as IMG_4821.jpg. " +
		"Hides names that identify you without making uploads look automated. " +
		"May violate Discord's Terms of Service.",
})

var enableStripMetadata = prefs.NewBool(false, prefs.PropMeta{
	Name:        "Strip Image Metadata",
	Section:     "Mods",
	Description: "Remove EXIF, GPS, and camera info from JPEG/PNG images before uploading. May violate Discord's Terms of Service.",
})

// RandomizeFilename discards the original filename, which can carry a real
// name, a project, or a camera's serial, and replaces it with one of the
// shapes an ordinary device produces. The extension and any SPOILER_ prefix
// are preserved.
//
// The replacement used to be sixteen characters of random hex. That defeats
// the purpose it was added for: a filename with no human structure is what
// bulk spam and malware uploads look like, and Discord scores attachment
// filenames. Naming the file the way a phone or a screenshot tool would keeps
// the original name hidden without making every upload look automated.
//
// The names are plausible rather than truthful. The counter in IMG_ does not
// count, and the clock in a screenshot name is not when the shot was taken.
// Both only have to look like something a device wrote.
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

	return prefix + plausibleStem(ext) + ext
}

// plausibleStem builds a filename stem in the house style of whatever device
// usually produces that kind of file.
func plausibleStem(ext string) string {
	// Today's date, with a time of day chosen at random. A screenshot or
	// photo taken earlier today is the ordinary case, and a real timestamp
	// would leak when the file was actually sent.
	stamp := func(sep string) string {
		day := time.Now().Format("20060102")
		secs := randInt(24 * 60 * 60)
		return day + sep + fmt.Sprintf("%02d%02d%02d", secs/3600, (secs/60)%60, secs%60)
	}

	switch strings.ToLower(ext) {
	case ".png":
		// GNOME, KDE and Windows all name screenshots this way, and a PNG
		// from a desktop is usually a screenshot.
		return "Screenshot_" + stamp("_")
	case ".jpg", ".jpeg", ".heic":
		// Camera roll. Real cameras use a four-digit sequence counter.
		return fmt.Sprintf("IMG_%04d", randInt(10000))
	case ".mp4", ".mov", ".webm", ".mkv":
		return "VID_" + stamp("_")
	case ".gif", ".webp":
		// Saved from a browser, where a short lowercase stem is the norm.
		return fmt.Sprintf("image_%04d", randInt(10000))
	case ".mp3", ".ogg", ".opus", ".wav", ".flac", ".m4a":
		return fmt.Sprintf("audio_%04d", randInt(10000))
	default:
		return fmt.Sprintf("file_%04d", randInt(10000))
	}
}

// randInt returns a uniform value in [0, n) from the system CSPRNG, falling
// back to zero if it is unavailable. The value is cosmetic, so a predictable
// fallback is harmless; what matters is that the original name is gone.
func randInt(n int) int {
	v, err := rand.Int(rand.Reader, big.NewInt(int64(n)))
	if err != nil {
		return 0
	}
	return int(v.Int64())
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
