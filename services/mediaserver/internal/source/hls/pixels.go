package hls

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"image"
	"image/png"

	"github.com/stupside/castor/services/mediaserver/internal/media"
)

// unwrapPNG checks structural metadata before decoding the image's pixel channels.
func unwrapPNG(body []byte) ([]byte, media.HLSExtraSegmentFormat, error) {
	cfg, err := png.DecodeConfig(bytes.NewReader(body))
	if err != nil {
		return nil, 0, fmt.Errorf("reading PNG segment: %w", err)
	}
	// Bound the decoder's four-byte pixels before allocating the image.
	pixels := uint64(cfg.Width) * uint64(cfg.Height)
	if pixels > pixelLimit/4 {
		return nil, 0, fmt.Errorf("PNG segment exceeds %d decoded bytes", pixelLimit)
	}
	chunks, err := readPNGChunks(body)
	if err != nil {
		return nil, 0, err
	}
	if validTS(chunks.trailer) {
		return chunks.trailer, media.HLSExtraSegmentPNGTrailer, nil
	}
	if len(chunks.trailer) != 0 {
		if ts, format, err := unwrapMarked(chunks.trailer); format != 0 || err != nil {
			return ts, format, err
		}
	}
	if len(chunks.parts) != 0 {
		return chunks.textSegment()
	}
	if body[24] != 8 { // IHDR bit depth
		return body, 0, nil
	}
	img, err := png.Decode(bytes.NewReader(body))
	if err != nil {
		return nil, 0, fmt.Errorf("decoding PNG segment: %w", err)
	}
	// Go's decoder owns these tightly packed pixels. Discard alpha in place;
	// grayscale+alpha is expanded to four channels by image/png.
	var rgb []byte
	switch img := img.(type) {
	case *image.Gray:
		rgb = img.Pix
	case *image.RGBA:
		rgb = colorBytes(img.Pix, 3)
	case *image.NRGBA:
		channels := 3
		if body[25] == 4 || body[25] == 0 { // grayscale with alpha or transparency
			channels = 1
		}
		rgb = colorBytes(img.Pix, channels)
	default:
		return body, 0, nil
	}
	if chunks.hasLength {
		if uint64(chunks.length) > uint64(len(rgb)) {
			return nil, 0, fmt.Errorf("invalid kwLn pixel payload length")
		}
		rgb = rgb[:chunks.length]
	}
	if data, ok := bytes.CutPrefix(rgb, []byte("KWP1")); ok {
		if len(data) < 4 || uint64(binary.BigEndian.Uint32(data)) > uint64(len(data)-4) {
			return nil, 0, fmt.Errorf("invalid KWP1 segment length")
		}
		return checkedTS(data[4:4+int(binary.BigEndian.Uint32(data))], media.HLSExtraSegmentKWP1)
	}
	if chunks.hasLength {
		return checkedTS(rgb, media.HLSExtraSegmentKWP1)
	}
	// Pixel encoders can also fill the final row after the last whole TS packet.
	// Accept only a fully synchronized stream with uniform pixel padding.
	if len(rgb) >= 3*tsPacketSize && rgb[0] == 0x47 {
		end := 0
		for end+tsPacketSize <= len(rgb) && rgb[end] == 0x47 {
			end += tsPacketSize
		}
		padding := rgb[end:]
		if len(padding) < len(rgb)/cfg.Height && (len(bytes.Trim(padding, "\x00")) == 0 || len(bytes.Trim(padding, "\xff")) == 0) && validTS(rgb[:end]) {
			return rgb[:end], media.HLSExtraSegmentKWP1, nil
		}
	}
	magic := []byte("TIKTIKPX")
	_, header, found := bytes.Cut(rgb, magic)
	if !found {
		return body, 0, nil
	}
	for found && len(header) >= 4 {
		length := binary.BigEndian.Uint32(header)
		data := header[4:]
		if length <= uint32(len(data)) && bytes.HasPrefix(data[:length], []byte{0x1f, 0x8b, 0x08}) {
			return inflateTS(data[:length], media.HLSExtraSegmentTIKTIKPX)
		}
		// Skip an unframed marker in the prefix. Inflate only a framed payload.
		_, header, found = bytes.Cut(header, magic)
	}
	return nil, 0, fmt.Errorf("invalid TIKTIKPX segment header")
}

func colorBytes(pix []byte, channels int) []byte {
	out := pix[:0]
	for i := 0; i < len(pix); i += 4 {
		out = append(out, pix[i:i+channels]...)
	}
	return out
}
