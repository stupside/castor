package hls

import (
	"bytes"
	"compress/gzip"
	"encoding/binary"
	"fmt"
	"io"

	"github.com/stupside/castor/services/mediaserver/internal/media"
)

const pixelLimit = 32 << 20
const tsPacketSize = 188

// unwrapSegment reads media only from a recognized envelope. Plain HLS resources
// remain owned by their normal demuxer, including ordinary PNG and WebP images.
func unwrapSegment(body []byte) ([]byte, media.HLSExtraSegmentFormat, error) {
	switch {
	case bytes.HasPrefix(body, []byte("\x89PNG\r\n\x1a\n")):
		return unwrapPNG(body)
	case len(body) >= 12 && string(body[:4]) == "RIFF" && string(body[8:12]) == "WEBP":
		return unwrapWebP(body)
	case len(body) >= tsPacketSize*2 && body[0] == 0x47 && body[tsPacketSize] == 0x47:
		return body, 0, nil
	case bytes.HasPrefix(body, []byte{0x1a, 0x45, 0xdf, 0xa3}) || bytes.HasPrefix(body, []byte("ID3")):
		return body, 0, nil
	}
	if len(body) >= 8 {
		switch string(body[4:8]) {
		case "ftyp", "styp", "moof", "mdat", "sidx", "moov", "emsg", "free":
			return body, 0, nil
		}
	}
	return unwrapMarked(body)
}

func unwrapMarked(body []byte) ([]byte, media.HLSExtraSegmentFormat, error) {
	for start := 0; start < len(body); {
		at := bytes.Index(body[start:], []byte("TIKTIK"))
		if at < 0 {
			break
		}
		start += at + len("TIKTIK")
		if ts, ok := bytes.CutPrefix(body[start:], []byte("RAW")); ok && validTS(ts) {
			return ts, media.HLSExtraSegmentRawMarker, nil
		}
		if gz, ok := bytes.CutPrefix(body[start:], []byte("TSGZ")); ok && bytes.HasPrefix(gz, []byte{0x1f, 0x8b, 8}) {
			return inflateTS(gz, media.HLSExtraSegmentGzipMarker)
		}
	}
	return body, 0, nil
}

func unwrapWebP(body []byte) ([]byte, media.HLSExtraSegmentFormat, error) {
	size := binary.LittleEndian.Uint32(body[4:])
	if size < 4 || uint64(size) > uint64(len(body)-8) {
		return nil, 0, fmt.Errorf("invalid WebP RIFF length")
	}
	chunks := body[12 : 8+int(size)]
	var ts []byte
	for len(chunks) > 0 {
		if len(chunks) < 8 {
			return nil, 0, fmt.Errorf("truncated WebP chunk header")
		}
		n := uint64(binary.LittleEndian.Uint32(chunks[4:]))
		end := 8 + n + n%2
		if end > uint64(len(chunks)) {
			return nil, 0, fmt.Errorf("truncated WebP chunk")
		}
		if string(chunks[:4]) == "EXIF" && validTS(chunks[8:8+n]) {
			ts = chunks[8 : 8+n]
		}
		chunks = chunks[end:]
	}
	if ts != nil {
		return ts, media.HLSExtraSegmentWebPExif, nil
	}
	return body, 0, nil
}

func inflateTS(payload []byte, format media.HLSExtraSegmentFormat) ([]byte, media.HLSExtraSegmentFormat, error) {
	z, err := gzip.NewReader(bytes.NewReader(payload))
	if err != nil {
		return nil, 0, fmt.Errorf("opening compressed segment: %w", err)
	}
	defer z.Close()
	ts, err := io.ReadAll(io.LimitReader(z, pixelLimit+1))
	if err != nil {
		return nil, 0, fmt.Errorf("expanding compressed segment: %w", err)
	}
	return checkedTS(ts, format)
}

func checkedTS(ts []byte, format media.HLSExtraSegmentFormat) ([]byte, media.HLSExtraSegmentFormat, error) {
	if !validTS(ts) {
		return nil, 0, fmt.Errorf("segment payload exceeds the limit or is not packet-aligned MPEG-TS")
	}
	return ts, format, nil
}

func validTS(ts []byte) bool {
	if len(ts) < 3*tsPacketSize || len(ts) > pixelLimit || len(ts)%tsPacketSize != 0 {
		return false
	}
	for i := 0; i < len(ts); i += tsPacketSize {
		if ts[i] != 0x47 {
			return false
		}
	}
	return true
}
