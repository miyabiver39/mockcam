package camera

import (
	"bytes"
	"image/jpeg"
	"testing"
)

func TestSnapshotSize(t *testing.T) {
	cases := []struct{ w, h, wantW, wantH int }{
		{0, 0, 640, 360},
		{1920, 1080, 1280, 720},
		{1280, 720, 1280, 720},
		{640, 480, 640, 480},
	}
	for _, c := range cases {
		if w, h := snapshotSize(c.w, c.h); w != c.wantW || h != c.wantH {
			t.Errorf("snapshotSize(%d,%d) = %dx%d", c.w, c.h, w, h)
		}
	}
}

func TestRenderPreviewAndEncode(t *testing.T) {
	img := RenderPreview(200, 100, 1, 1, 1)
	if img.Bounds().Dx() != 200 || img.Bounds().Dy() != 100 {
		t.Fatal("preview size")
	}
	// Crosshair is drawn at the shifted position (pan=1 → x=150, tilt=1 → y=25).
	if r, g, b, _ := img.At(150, 25).RGBA(); r != 0 || g>>8 != 255 || b>>8 != 180 {
		t.Fatalf("crosshair colour at target = %v %v %v", r>>8, g>>8, b>>8)
	}

	data := EncodePreviewJPEG(320, 180, 0, 0, 0)
	decoded, err := jpeg.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	if decoded.Bounds().Dx() != 320 {
		t.Fatalf("encoded width = %d", decoded.Bounds().Dx())
	}
}
