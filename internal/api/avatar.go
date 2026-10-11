package api

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"image"
	"image/color"
	_ "image/gif" // decoders for the identity service's pictures
	_ "image/jpeg"
	"image/png"
)

const avatarSize = 96

// resizeAvatar crops a picture to a centered square and scales it to
// 96x96 by averaging, returning PNG bytes and an ETag.
func resizeAvatar(raw []byte) ([]byte, string, string, error) {
	cfg, _, err := image.DecodeConfig(bytes.NewReader(raw))
	if err != nil {
		return nil, "", "", err
	}
	if cfg.Width*cfg.Height > 25_000_000 {
		return nil, "", "", errors.New("picture too large")
	}
	src, _, err := image.Decode(bytes.NewReader(raw))
	if err != nil {
		return nil, "", "", err
	}
	b := src.Bounds()
	side := min(b.Dx(), b.Dy())
	if side < 1 {
		return nil, "", "", errors.New("empty picture")
	}
	x0, y0 := b.Min.X+(b.Dx()-side)/2, b.Min.Y+(b.Dy()-side)/2
	dst := image.NewNRGBA(image.Rect(0, 0, avatarSize, avatarSize))
	for y := range avatarSize {
		sy0, sy1 := y0+y*side/avatarSize, y0+max((y+1)*side/avatarSize, y*side/avatarSize+1)
		for x := range avatarSize {
			sx0, sx1 := x0+x*side/avatarSize, x0+max((x+1)*side/avatarSize, x*side/avatarSize+1)
			var r, g, bl, a, n uint64
			for sy := sy0; sy < sy1; sy++ {
				for sx := sx0; sx < sx1; sx++ {
					c := color.NRGBAModel.Convert(src.At(sx, sy)).(color.NRGBA)
					r, g, bl, a = r+uint64(c.R), g+uint64(c.G), bl+uint64(c.B), a+uint64(c.A)
					n++
				}
			}
			dst.SetNRGBA(x, y, color.NRGBA{R: uint8(r / n), G: uint8(g / n), B: uint8(bl / n), A: uint8(a / n)})
		}
	}
	var out bytes.Buffer
	if err := (&png.Encoder{CompressionLevel: png.BestCompression}).Encode(&out, dst); err != nil {
		return nil, "", "", err
	}
	if out.Len() > 65536 {
		return nil, "", "", errors.New("resized picture too large")
	}
	sum := sha256.Sum256(out.Bytes())
	return out.Bytes(), "image/png", hex.EncodeToString(sum[:8]), nil
}
