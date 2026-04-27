//go:build !windows

package main

import (
	"bytes"
	"image"
	"image/png"
)

// encodeIcon returns PNG bytes for macOS/Linux systray.
func encodeIcon(img *image.RGBA) []byte {
	var buf bytes.Buffer
	png.Encode(&buf, img)
	return buf.Bytes()
}
