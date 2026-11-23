package artifact

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var (
	pngMagic  = []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR")
	jpegMagic = []byte("\xff\xd8\xff\xe0\x00\x10JFIF\x00")
	gifMagic  = []byte("GIF89a\x01\x00\x01\x00")
	webpMagic = []byte("RIFF\x00\x00\x00\x00WEBPVP8 ")
	svgPlain  = []byte(`<svg xmlns="http://www.w3.org/2000/svg"/>`)
)

func TestDetect(t *testing.T) {
	t.Run("recognises each image by its magic bytes", func(t *testing.T) {
		assertDetected(t, "a.bin", pngMagic, KindImage, "image/png")
		assertDetected(t, "a.bin", jpegMagic, KindImage, "image/jpeg")
		assertDetected(t, "a.bin", gifMagic, KindImage, "image/gif")
		assertDetected(t, "a.bin", webpMagic, KindImage, "image/webp")
	})
	t.Run("accepts an svg with a prolog, comment and doctype", func(t *testing.T) {
		data := []byte("<?xml version=\"1.0\"?>\n<!-- made in a tool -->\n<!DOCTYPE svg>\n<svg width=\"1\"/>")
		assertDetected(t, "diagram.svg", data, KindImage, "image/svg+xml")
	})
	t.Run("accepts an svg root after whitespace and an uppercase svg extension", func(t *testing.T) {
		assertDetected(t, "DIAGRAM.SVG", append([]byte(" \n\t"), svgPlain...), KindImage, "image/svg+xml")
	})
	t.Run("refuses a .svg file that is not svg", func(t *testing.T) {
		assertRefused(t, "diagram.svg", []byte("<html><body/></html>"))
	})
	t.Run("refuses an svg with a root that only starts with svg", func(t *testing.T) {
		assertRefused(t, "diagram.svg", []byte("<svgx/>"))
		assertRefused(t, "diagram.svg", []byte("<svg"))
	})
	t.Run("refuses an unterminated prolog", func(t *testing.T) {
		assertRefused(t, "diagram.svg", []byte("<?xml version=\"1.0\""))
		assertRefused(t, "diagram.svg", []byte("<!-- never closed"))
		assertRefused(t, "diagram.svg", []byte("<!DOCTYPE svg"))
	})
	t.Run("detects markdown by extension", func(t *testing.T) {
		assertDetected(t, "pitch.md", []byte("# Pitch\n"), KindMarkdown, "text/markdown; charset=utf-8")
		assertDetected(t, "pitch.MARKDOWN", []byte("# Pitch\n"), KindMarkdown, "text/markdown; charset=utf-8")
	})
	t.Run("treats other UTF-8 text as text", func(t *testing.T) {
		assertDetected(t, "notes.txt", []byte("hello\n"), KindText, "text/plain; charset=utf-8")
		assertDetected(t, "page.html", []byte("<html></html>"), KindText, "text/plain; charset=utf-8")
	})
	t.Run("refuses empty data", func(t *testing.T) {
		assertRefused(t, "notes.txt", nil)
	})
	t.Run("refuses data with a NUL byte", func(t *testing.T) {
		assertRefused(t, "notes.txt", []byte("a\x00b"))
	})
	t.Run("refuses invalid UTF-8", func(t *testing.T) {
		assertRefused(t, "notes.txt", []byte("\xff\xfe\xfd"))
	})
	t.Run("refuses a PNG over 10 MiB", func(t *testing.T) {
		data := append(append([]byte{}, pngMagic...), make([]byte, MaxImageSize-len(pngMagic)+1)...)
		assertRefused(t, "big.png", data)
	})
	t.Run("accepts a PNG of exactly 10 MiB", func(t *testing.T) {
		data := append(append([]byte{}, pngMagic...), make([]byte, MaxImageSize-len(pngMagic))...)
		assertDetected(t, "big.png", data, KindImage, "image/png")
	})
	t.Run("refuses text over 1 MiB", func(t *testing.T) {
		assertRefused(t, "big.txt", bytes.Repeat([]byte("a"), MaxTextSize+1))
	})
}

func assertDetected(t *testing.T, name string, data []byte, kind Kind, ct string) {
	t.Helper()
	gotKind, gotCT, err := Detect(name, data)
	require.NoError(t, err)
	assert.Equal(t, kind, gotKind)
	assert.Equal(t, ct, gotCT)
}

func assertRefused(t *testing.T, name string, data []byte) {
	t.Helper()
	_, _, err := Detect(name, data)
	assert.ErrorIs(t, err, ErrInvalid)
}

func TestCheckName(t *testing.T) {
	t.Run("accepts a plain name", func(t *testing.T) {
		assert.NoError(t, CheckName("pitch.md"))
	})
	t.Run("accepts a name of 128 runes", func(t *testing.T) {
		assert.NoError(t, CheckName(string(bytes.Repeat([]byte("é"), MaxNameRunes))))
	})
	t.Run("refuses names that are empty, too long, or not one path element", func(t *testing.T) {
		assert.ErrorIs(t, CheckName(""), ErrInvalid)
		assert.ErrorIs(t, CheckName(string(bytes.Repeat([]byte("a"), MaxNameRunes+1))), ErrInvalid)
		assert.ErrorIs(t, CheckName("a/b.md"), ErrInvalid)
		assert.ErrorIs(t, CheckName(`a\b.md`), ErrInvalid)
		assert.ErrorIs(t, CheckName(".hidden"), ErrInvalid)
		assert.ErrorIs(t, CheckName("a\nb"), ErrInvalid)
	})
}

func TestCheckLine(t *testing.T) {
	t.Run("accepts empty and one-line text", func(t *testing.T) {
		assert.NoError(t, CheckLine("title", ""))
		assert.NoError(t, CheckLine("title", "Pitch: cache sharing"))
	})
	t.Run("refuses text over the limit or with a control character", func(t *testing.T) {
		assert.ErrorIs(t, CheckLine("note", string(bytes.Repeat([]byte("a"), MaxLineRunes+1))), ErrInvalid)
		assert.ErrorIs(t, CheckLine("note", "two\nlines"), ErrInvalid)
		assert.ErrorIs(t, CheckLine("note", "tab\there"), ErrInvalid)
	})
}
