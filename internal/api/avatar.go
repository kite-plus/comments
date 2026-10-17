package api

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"html"
	"image"
	"image/color"
	_ "image/gif" // decoders for the identity service's pictures
	_ "image/jpeg"
	"image/png"
	"net/url"
	"strings"
	"unicode"
	"unicode/utf8"
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

// sealInks are the colors of generated avatars, one picked per seed.
var sealInks = [...]string{
	"#3a66c4", "#b3432d", "#3f7a5c", "#a8792a", "#7a4f7d", "#4d6170",
	"#8a5a3c", "#2f7f86", "#c0602e", "#5b5fb0", "#a34a6a", "#6b7a32",
}

// generatedURL is the default avatar of a seed, drawn with the first letter
// of name. The c parameter is always present so browsers do not keep showing
// the pixel pattern once cached under the bare path.
func generatedURL(seed, name string) string {
	return "/avatars/generated/" + seed + ".svg?c=" + url.QueryEscape(initialOf(name))
}

// initialOf is the first character of s, upper-cased, when it is a letter or
// a digit, and "" otherwise.
func initialOf(s string) string {
	r, _ := utf8.DecodeRuneInString(strings.TrimSpace(s))
	if !unicode.IsLetter(r) && !unicode.IsDigit(r) {
		return ""
	}
	return string(unicode.ToUpper(r))
}

// generatedAvatar draws a seal, as the account pages do: a slightly tilted
// rounded square in one ink with the initial in paper white, or the Kite
// Plus mark when there is no initial.
func generatedAvatar(seed, initial string) []byte {
	sum := sha256.Sum256([]byte(seed))
	ink := sealInks[int(sum[0])%len(sealInks)]
	var b strings.Builder
	fmt.Fprintf(&b, `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 96 96" width="96" height="96"><g transform="rotate(%d 48 48)">`, -7+int(sum[1])%5)
	fmt.Fprintf(&b, `<rect x="9" y="9" width="78" height="78" rx="18" fill="%s"/>`, ink)
	b.WriteString(`<rect x="15.5" y="15.5" width="65" height="65" rx="13" fill="none" stroke="#fffdf8" stroke-opacity=".3" stroke-width="2.5"/>`)
	r, _ := utf8.DecodeRuneInString(initial)
	switch {
	case initial == "":
		b.WriteString(`<g transform="translate(24 24) scale(.75)" fill="#fffdf8" stroke="#fffdf8" stroke-width="5" stroke-linejoin="round">` +
			`<path d="M10 14.5 L27 21 L27 30 L10 23.5 Z"/><path d="M10 32 L27 38.5 L27 49 L10 42.5 Z"/><path d="M37 21 L54 14.5 L54 42.5 L37 49 Z"/></g>`)
	case unicode.In(r, unicode.Han, unicode.Hiragana, unicode.Katakana, unicode.Hangul):
		fmt.Fprintf(&b, `<text x="48" y="64" text-anchor="middle" font-family="'Noto Serif SC','Source Han Serif SC','Songti SC','STSong','SimSun',serif" font-weight="900" font-size="42" fill="#fffdf8">%s</text>`, html.EscapeString(initial))
	default:
		fmt.Fprintf(&b, `<text x="48" y="63" text-anchor="middle" font-family="Georgia,'Times New Roman',serif" font-weight="700" font-size="46" fill="#fffdf8">%s</text>`, html.EscapeString(initial))
	}
	b.WriteString(`</g></svg>`)
	return []byte(b.String())
}
