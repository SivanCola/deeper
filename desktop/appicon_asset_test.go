package main

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"testing"
)

func TestAppIconPNGUsesCeramicPlateAndOrangeWhiteRibbon(t *testing.T) {
	f, err := os.Open("build/appicon.png")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	img, err := png.Decode(f)
	if err != nil {
		t.Fatal(err)
	}

	assertRibbonIcon(t, img, 1024)
}

func TestWindowsICOUsesCeramicPlateAndOrangeWhiteRibbon(t *testing.T) {
	for _, size := range []int{16, 24, 32, 48, 64, 256} {
		t.Run(fmt.Sprintf("%dx%d", size, size), func(t *testing.T) {
			img := decodeICOImage(t, "build/windows/icon.ico", size)
			assertRibbonIcon(t, img, size)
		})
	}
}

func TestDarwinICNSUsesMacOSIconSafeArea(t *testing.T) {
	img := decodeICNSImage(t, "build/darwin/icon.icns", "ic10")
	if got, want := img.Bounds(), image.Rect(0, 0, 1024, 1024); got != want {
		t.Fatalf("macOS ic10 frame bounds = %v, want %v", got, want)
	}
	assertRibbonIcon(t, img, 1024)
	// The explicit ceramic plate is fitted once into the 824px macOS grid;
	// the folded logo must not acquire another layer of transparent padding.
	visible := alphaBounds(img)
	if !visible.In(image.Rect(96, 96, 928, 928)) || visible.Dx() < 816 || visible.Dy() < 816 {
		t.Fatalf("macOS ribbon visible bounds = %v, want artwork fitted in the 824px grid", visible)
	}
	_, _, _, plateAlpha := img.At(512, 120).RGBA()
	if plateAlpha < 0xff00 {
		t.Fatal("macOS artwork must contain its own opaque plate inside the safe area")
	}
}

func TestDarwinICNSUsesMacOSCompatibleCorners(t *testing.T) {
	// Excess rounding makes macOS 26+ add a gray compatibility tile.
	// Check normal and Retina silhouettes; center and bounds also pass for a circle.
	for _, iconType := range []string{"ic07", "ic08", "ic09", "ic10", "ic13", "ic14"} {
		t.Run(iconType, func(t *testing.T) {
			img := decodeICNSImage(t, "build/darwin/icon.icns", iconType)
			size := img.Bounds().Dx()
			for _, point := range []struct{ x, y int }{{160, 160}, {863, 160}, {160, 863}, {863, 863}} {
				c := img.At(point.x*size/1024, point.y*size/1024)
				_, _, _, alpha := c.RGBA()
				if alpha < 0xff00 {
					t.Fatalf("macOS rounded-square corner (%d, %d) must be opaque; alpha=%d", point.x, point.y, alpha)
				}
				pixel := color.NRGBAModel.Convert(c).(color.NRGBA)
				if pixel.R < 170 || pixel.G < 170 || pixel.B < 150 {
					t.Fatalf("macOS corner must contain the warm ceramic plate, got %v", pixel)
				}
			}
		})
	}
}

func TestDarwinDevelopmentIconMatchesBundleArtwork(t *testing.T) {
	f, err := os.Open("build/darwin/appicon.png")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	dev, err := png.Decode(f)
	if err != nil {
		t.Fatal(err)
	}
	bundle := decodeICNSImage(t, "build/darwin/icon.icns", "ic10")
	if dev.Bounds() != bundle.Bounds() {
		t.Fatalf("development icon bounds = %v, want %v", dev.Bounds(), bundle.Bounds())
	}
	for y := dev.Bounds().Min.Y; y < dev.Bounds().Max.Y; y++ {
		for x := dev.Bounds().Min.X; x < dev.Bounds().Max.X; x++ {
			if color.NRGBAModel.Convert(dev.At(x, y)) != color.NRGBAModel.Convert(bundle.At(x, y)) {
				t.Fatalf("development icon differs from bundle artwork at (%d, %d)", x, y)
			}
		}
	}
}

func assertRibbonIcon(t *testing.T, img image.Image, size int) {
	t.Helper()

	bounds := img.Bounds()
	if bounds.Dx() != size || bounds.Dy() != size {
		t.Fatalf("app icon must be square, got %dx%d", bounds.Dx(), bounds.Dy())
	}

	corners := []struct {
		name string
		x    int
		y    int
	}{
		{"top-left", bounds.Min.X, bounds.Min.Y},
		{"top-right", bounds.Max.X - 1, bounds.Min.Y},
		{"bottom-left", bounds.Min.X, bounds.Max.Y - 1},
		{"bottom-right", bounds.Max.X - 1, bounds.Max.Y - 1},
	}
	for _, corner := range corners {
		_, _, _, a := img.At(corner.x, corner.y).RGBA()
		if a > 0xff {
			t.Fatalf("%s corner must be transparent, alpha=%d", corner.name, a)
		}
	}

	_, _, _, centerAlpha := img.At(bounds.Min.X+bounds.Dx()/2, bounds.Min.Y+bounds.Dy()/2).RGBA()
	if centerAlpha == 0 {
		t.Fatal("app icon center must contain visible artwork")
	}

	// Check both surfaces, including at 16px: a solid orange tile, blank PNG,
	// monochrome silhouette, or an accidentally transparent white fold fails.
	orange, ivory, transparent := 0, 0, 0
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			pixel := color.NRGBAModel.Convert(img.At(x, y)).(color.NRGBA)
			if pixel.A < 16 {
				transparent++
			}
			if pixel.A < 128 {
				continue
			}
			if pixel.R > 180 && pixel.G > 35 && pixel.G < 200 && pixel.B < 150 {
				orange++
			}
			if pixel.R > 190 && pixel.G > 190 && pixel.B > 170 {
				ivory++
			}
		}
	}
	area := size * size
	// A nearly full-canvas rounded plate has much less transparent area than
	// a floating symbol, especially after antialiasing at 16–32px.
	if orange < area/8 || ivory < area/30 || transparent < area/32 {
		t.Fatalf("ribbon surfaces missing at %dpx: orange=%d ivory=%d transparent=%d", size, orange, ivory, transparent)
	}
}

func alphaBounds(img image.Image) image.Rectangle {
	bounds := img.Bounds()
	visible := image.Rectangle{}
	found := false
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			_, _, _, a := img.At(x, y).RGBA()
			if a == 0 {
				continue
			}
			if !found {
				visible = image.Rect(x, y, x+1, y+1)
				found = true
				continue
			}
			if x < visible.Min.X {
				visible.Min.X = x
			}
			if y < visible.Min.Y {
				visible.Min.Y = y
			}
			if x >= visible.Max.X {
				visible.Max.X = x + 1
			}
			if y >= visible.Max.Y {
				visible.Max.Y = y + 1
			}
		}
	}
	return visible
}

func decodeICNSImage(t *testing.T, path, iconType string) image.Image {
	t.Helper()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) < 8 || string(data[:4]) != "icns" {
		t.Fatal("invalid ICNS header")
	}
	if declaredSize := int(binary.BigEndian.Uint32(data[4:8])); declaredSize != len(data) {
		t.Fatalf("ICNS size = %d, want %d", declaredSize, len(data))
	}

	for offset := 8; offset < len(data); {
		if offset+8 > len(data) {
			t.Fatalf("truncated ICNS entry header at offset %d", offset)
		}
		entryType := string(data[offset : offset+4])
		entrySize := int(binary.BigEndian.Uint32(data[offset+4 : offset+8]))
		if entrySize < 8 || offset+entrySize > len(data) {
			t.Fatalf("invalid ICNS entry %q size %d at offset %d", entryType, entrySize, offset)
		}
		if entryType == iconType {
			img, err := png.Decode(bytes.NewReader(data[offset+8 : offset+entrySize]))
			if err != nil {
				t.Fatalf("decode ICNS entry %q: %v", iconType, err)
			}
			return img
		}
		offset += entrySize
	}

	t.Fatalf("ICNS is missing %q image", iconType)
	return nil
}

func decodeICOImage(t *testing.T, path string, size int) image.Image {
	t.Helper()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	r := bytes.NewReader(data)

	var header struct {
		Reserved uint16
		Type     uint16
		Count    uint16
	}
	if err := binary.Read(r, binary.LittleEndian, &header); err != nil {
		t.Fatal(err)
	}
	if header.Reserved != 0 || header.Type != 1 {
		t.Fatalf("invalid ICO header: reserved=%d type=%d", header.Reserved, header.Type)
	}

	type iconEntry struct {
		Width       uint8
		Height      uint8
		ColorCount  uint8
		Reserved    uint8
		Planes      uint16
		BitCount    uint16
		BytesInRes  uint32
		ImageOffset uint32
	}

	entries := make([]iconEntry, header.Count)
	for i := range entries {
		if err := binary.Read(r, binary.LittleEndian, &entries[i]); err != nil {
			t.Fatal(err)
		}
	}

	expectedSizes := map[int]bool{16: false, 24: false, 32: false, 48: false, 64: false, 256: false}
	targetIndex := -1
	for i, entry := range entries {
		width := int(entry.Width)
		height := int(entry.Height)
		if width == 0 {
			width = 256
		}
		if height == 0 {
			height = 256
		}
		if width != height {
			t.Fatalf("ICO image must be square, got %dx%d", width, height)
		}
		if _, ok := expectedSizes[width]; ok {
			expectedSizes[width] = true
		}
		if width == size {
			targetIndex = i
		}
	}
	for expectedSize, found := range expectedSizes {
		if !found {
			t.Fatalf("ICO is missing %dx%d image", expectedSize, expectedSize)
		}
	}
	if targetIndex < 0 {
		t.Fatalf("ICO is missing %dx%d image", size, size)
	}

	entry := entries[targetIndex]
	end := int(entry.ImageOffset + entry.BytesInRes)
	if end > len(data) {
		t.Fatalf("ICO image offset exceeds file size: offset=%d size=%d file=%d", entry.ImageOffset, entry.BytesInRes, len(data))
	}
	img, err := png.Decode(bytes.NewReader(data[entry.ImageOffset:end]))
	if err != nil {
		t.Fatal(err)
	}
	return img
}
