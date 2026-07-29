// Package gui provides the MyVPN desktop application interface using Fyne v2.
package gui

import (
	"bytes"
	"image"
	"image/color"
	"image/png"

	"fyne.io/fyne/v2"
)

// generateIcon creates a simple purple shield icon as a Fyne resource.
func generateIcon() fyne.Resource {
	// Create a 64x64 RGBA image
	img := image.NewRGBA(image.Rect(0, 0, 64, 64))

	// Colors
	purple := color.RGBA{R: 0xA8, G: 0x55, B: 0xF7, A: 0xFF}      // #A855F7 accent
	darkPurple := color.RGBA{R: 0x7C, G: 0x3A, B: 0xED, A: 0xFF}  // #7C3AED darker
	transparent := color.RGBA{R: 0, G: 0, B: 0, A: 0}

	// Draw a simple shield shape
	for y := 0; y < 64; y++ {
		for x := 0; x < 64; x++ {
			// Shield shape using basic geometry
			cx, cy := float64(x-32), float64(y-32)
			// Shield: wider at top, pointed at bottom
			topHalf := y < 32
			width := 28.0
			if topHalf {
				width = 22.0 + float64(y)*0.1875 // taper from 22 to 28
			} else {
				width = 28.0 - float64(y-32)*0.4375 // taper from 28 to 14
			}

			if cx < -width || cx > width {
				img.Set(x, y, transparent)
				continue
			}

			// Bottom point of shield
			if !topHalf && float64(y-32) > 24.0 {
				pointWidth := 14.0 * (1.0 - float64(y-56)/8.0)
				if cx < -pointWidth || cx > pointWidth {
					img.Set(x, y, transparent)
					continue
				}
			}

			// Fill with gradient
			if y < 32 {
				img.Set(x, y, purple)
			} else {
				t := float64(y-32) / 32.0
				r := lerp(uint8(purple.R), uint8(darkPurple.R), t)
				g := lerp(uint8(purple.G), uint8(darkPurple.G), t)
				b := lerp(uint8(purple.B), uint8(darkPurple.B), t)
				img.Set(x, y, color.RGBA{R: r, G: g, B: b, A: 0xFF})
			}
		}
	}

	// Draw a stylized "V" in the center
	vColor := color.RGBA{R: 0xFF, G: 0xFF, B: 0xFF, A: 0xFF}
	for y := 18; y < 44; y++ {
		t := float64(y-18) / 26.0
		left := int(14.0 - t*8.0)
		right := int(50.0 + t*8.0)
		mid := 32
		// Left arm of V
		img.Set(mid-left, y, vColor)
		img.Set(mid-left-1, y, vColor)
		img.Set(mid-left+1, y, vColor)
		// Right arm of V
		img.Set(mid+right-32, y, vColor)
		img.Set(mid+right-33, y, vColor)
		img.Set(mid+right-31, y, vColor)
	}

	// Encode as PNG
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		// Fallback to a minimal 1x1 pixel PNG if encoding fails
		fallback := image.NewRGBA(image.Rect(0, 0, 1, 1))
		fallback.Set(0, 0, purple)
		var fbBuf bytes.Buffer
		png.Encode(&fbBuf, fallback)
		return fyne.NewStaticResource("icon.png", fbBuf.Bytes())
	}

	return fyne.NewStaticResource("icon.png", buf.Bytes())
}

func lerp(a, b uint8, t float64) uint8 {
	return uint8(float64(a) + (float64(b)-float64(a))*t)
}
