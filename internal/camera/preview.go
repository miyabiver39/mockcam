package camera

import (
	"bytes"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
)

const (
	snapshotMaxWidth  = 1280
	snapshotMaxHeight = 720
)

// snapshotSize returns the preview size for a profile, capped for browser
// responsiveness and defaulting to 640x360 when the profile is unknown.
func snapshotSize(width, height int) (int, int) {
	if width <= 0 || height <= 0 {
		return 640, 360
	}
	if width > snapshotMaxWidth {
		return snapshotMaxWidth, snapshotMaxHeight
	}
	return width, height
}

// RenderPreview draws a synthetic preview frame: a dark background tinted
// by the PTZ state, a grid, and a crosshair offset by pan/tilt. It is used
// when the FFmpeg worker has not delivered a live frame (yet).
func RenderPreview(width, height int, pan, tilt, zoom float64) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, width, height))

	bgR := uint8(24 + int((pan+1.0)*15))
	bgG := uint8(28 + int((tilt+1.0)*15))
	bgB := uint8(40 + int(zoom*30))
	draw.Draw(img, img.Bounds(), &image.Uniform{color.RGBA{bgR, bgG, bgB, 255}}, image.Point{}, draw.Src)

	gridCol := color.RGBA{60, 70, 90, 255}
	for x := 0; x < width; x += 40 {
		for y := 0; y < height; y += 2 {
			img.Set(x, y, gridCol)
		}
	}
	for y := 0; y < height; y += 40 {
		for x := 0; x < width; x += 2 {
			img.Set(x, y, gridCol)
		}
	}

	centerX, centerY := width/2, height/2
	targetX := centerX + int(pan*float64(centerX/2))
	targetY := centerY - int(tilt*float64(centerY/2))
	crossCol := color.RGBA{0, 255, 180, 255}
	for x := targetX - 25; x <= targetX+25; x++ {
		if x >= 0 && x < width && targetY >= 0 && targetY < height {
			img.Set(x, targetY, crossCol)
		}
	}
	for y := targetY - 25; y <= targetY+25; y++ {
		if y >= 0 && y < height && targetX >= 0 && targetX < width {
			img.Set(targetX, y, crossCol)
		}
	}
	return img
}

// EncodePreviewJPEG renders and JPEG-encodes the synthetic preview.
func EncodePreviewJPEG(width, height int, pan, tilt, zoom float64) []byte {
	var buf bytes.Buffer
	_ = jpeg.Encode(&buf, RenderPreview(width, height, pan, tilt, zoom), &jpeg.Options{Quality: 80})
	return buf.Bytes()
}
