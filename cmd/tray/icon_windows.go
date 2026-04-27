package main

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/png"
)

// encodeIcon wraps a PNG image in an ICO container for Windows systray.
func encodeIcon(img *image.RGBA) []byte {
	var pngBuf bytes.Buffer
	png.Encode(&pngBuf, img)
	pngData := pngBuf.Bytes()

	size := img.Bounds().Dx()
	w := byte(size)
	if size >= 256 {
		w = 0 // 0 means 256 in ICO format
	}
	h := byte(size)
	if size >= 256 {
		h = 0
	}

	var buf bytes.Buffer

	// ICONDIR header (6 bytes)
	binary.Write(&buf, binary.LittleEndian, uint16(0)) // Reserved
	binary.Write(&buf, binary.LittleEndian, uint16(1)) // Type: 1 = icon
	binary.Write(&buf, binary.LittleEndian, uint16(1)) // Count: 1 image

	// ICONDIRENTRY (16 bytes)
	buf.WriteByte(w)                                              // Width
	buf.WriteByte(h)                                              // Height
	buf.WriteByte(0)                                              // Color count (0 for >256 colors)
	buf.WriteByte(0)                                              // Reserved
	binary.Write(&buf, binary.LittleEndian, uint16(1))            // Color planes
	binary.Write(&buf, binary.LittleEndian, uint16(32))           // Bits per pixel
	binary.Write(&buf, binary.LittleEndian, uint32(len(pngData))) // Size of image data
	binary.Write(&buf, binary.LittleEndian, uint32(6+16))         // Offset to image data

	// PNG image data
	buf.Write(pngData)

	return buf.Bytes()
}
