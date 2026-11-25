package hls

import (
	"bytes"
	"compress/zlib"
	"encoding/base64"
	"encoding/binary"
	"hash/crc32"
	"image"
	"image/png"
	"testing"

	"github.com/stupside/castor/services/mediaserver/internal/media"
)

func pngChunk(kind string, data []byte) []byte {
	chunk := binary.BigEndian.AppendUint32(nil, uint32(len(data)))
	chunk = append(chunk, kind...)
	chunk = append(chunk, data...)
	return binary.BigEndian.AppendUint32(chunk, crc32.ChecksumIEEE(chunk[4:]))
}

func beforeIEND(img []byte, chunks ...[]byte) []byte {
	out := bytes.Clone(img[:len(img)-12])
	for _, chunk := range chunks {
		out = append(out, chunk...)
	}
	return append(out, img[len(img)-12:]...)
}

func itxt(t *testing.T, name string, text []byte, compress bool) []byte {
	t.Helper()
	prefix := append([]byte(name), 0, 0, 0, 0, 0)
	if compress {
		prefix[len(name)+1] = 1
		var buf bytes.Buffer
		w := zlib.NewWriter(&buf)
		if _, err := w.Write(text); err != nil {
			t.Fatal(err)
		}
		if err := w.Close(); err != nil {
			t.Fatal(err)
		}
		text = buf.Bytes()
	}
	return pngChunk("iTXt", append(prefix, text...))
}

func webpExifTestFile(exif []byte) []byte {
	body := []byte("WEBPJUNK")
	body = binary.LittleEndian.AppendUint32(body, 1)
	body = append(body, 7, 0) // one byte of RIFF padding
	body = append(body, "EXIF"...)
	body = binary.LittleEndian.AppendUint32(body, uint32(len(exif)))
	body = append(body, exif...)
	if len(exif)%2 != 0 {
		body = append(body, 0)
	}
	return append(binary.LittleEndian.AppendUint32([]byte("RIFF"), uint32(len(body))), body...)
}

func TestHLSSegmentEnvelopes(t *testing.T) {
	ts := bytes.Repeat(append([]byte{0x47}, make([]byte, 187)...), 5)
	encoded := []byte(base64.StdEncoding.EncodeToString(ts))
	img := pixelPNG(t, []byte("ordinary picture"), 255)
	part0 := itxt(t, "payload-0", encoded[:19], true)
	part1 := itxt(t, "payload-1", encoded[19:], false)
	kwp := append(binary.BigEndian.AppendUint32([]byte("KWP1"), uint32(len(ts))), ts...)
	length := pngChunk("kwLn", binary.BigEndian.AppendUint32(nil, uint32(len(kwp))))
	tsLength := pngChunk("kwLn", binary.BigEndian.AppendUint32(nil, uint32(len(ts))))
	gray := image.NewGray(image.Rect(0, 0, 64, (len(kwp)+63)/64))
	copy(gray.Pix, kwp)
	var g bytes.Buffer
	if err := png.Encode(&g, gray); err != nil {
		t.Fatal(err)
	}

	for _, c := range []struct {
		name   string
		input  []byte
		format media.HLSExtraSegmentFormat
	}{
		{"KWP1 RGB", pixelPNG(t, kwp, 255), media.HLSExtraSegmentKWP1},
		{"KWP1 RGBA and length", beforeIEND(pixelPNG(t, kwp, 127), length), media.HLSExtraSegmentKWP1},
		{"KWP1 grayscale", g.Bytes(), media.HLSExtraSegmentKWP1},
		{"raw pixels with length", beforeIEND(pixelPNG(t, ts, 255), tsLength), media.HLSExtraSegmentKWP1},
		{"raw pixels with row padding", pixelPNG(t, ts, 255), media.HLSExtraSegmentKWP1},
		{"out of order mixed iTXt", beforeIEND(img, part1, part0), media.HLSExtraSegmentPNGText},
		{"PNG trailer", append(bytes.Clone(img), ts...), media.HLSExtraSegmentPNGTrailer},
		{"WebP EXIF", webpExifTestFile(ts), media.HLSExtraSegmentWebPExif},
		{"raw marker", append([]byte("prefixTIKTIKRAW"), ts...), media.HLSExtraSegmentRawMarker},
		{"gzip marker", append([]byte("prefixTIKTIKTSGZ"), compressed(t, ts)...), media.HLSExtraSegmentGzipMarker},
	} {
		t.Run(c.name, func(t *testing.T) {
			got, format, err := unwrapSegment(c.input)
			if err != nil || format != c.format || !bytes.Equal(got, ts) {
				t.Fatalf("format=%d, bytes=%d, error=%v", format, len(got), err)
			}
		})
	}
	broken := bytes.Clone(ts)
	broken[3*tsPacketSize] = 0
	ordinary := [][]byte{
		webpExifTestFile([]byte("Exif\x00\x00")),
		webpExifTestFile(broken),
		append(bytes.Clone(img), broken...),
		beforeIEND(img, itxt(t, "payload-credit", []byte("photographer"), false)),
		append([]byte("not a segment TIKTIKRAW"), broken...),
		append([]byte("\x00\x00\x00\x10mdatTIKTIKRAW"), ts...),
	}
	for i, input := range ordinary {
		got, kind, err := unwrapSegment(input)
		if err != nil || kind != 0 || !bytes.Equal(got, input) {
			t.Fatalf("changed ordinary resource %d: %v", i, err)
		}
	}
	corruptCRC := bytes.Clone(part0)
	corruptCRC[len(corruptCRC)-1] ^= 1
	badWebP := webpExifTestFile(ts)
	binary.LittleEndian.PutUint32(badWebP[26:], ^uint32(0))
	badLen := pngChunk("kwLn", []byte{0xff, 0xff, 0xff, 0xff})
	for name, input := range map[string][]byte{
		"checksum":                       beforeIEND(img, corruptCRC),
		"bad RIFF length":                badWebP,
		"duplicate":                      beforeIEND(img, part0, part0),
		"missing zero":                   beforeIEND(img, part1),
		"bad base64":                     beforeIEND(img, itxt(t, "payload-0", []byte("%%%"), false)),
		"bad KWP1 length":                pixelPNG(t, []byte("KWP1\xff\xff\xff\xff"), 255),
		"bad kwLn":                       beforeIEND(img, badLen),
		"bad MPEG-TS in labeled payload": beforeIEND(img, itxt(t, "payload-0", []byte(base64.StdEncoding.EncodeToString(broken)), true)),
		"bad gzip TS":                    append([]byte("TIKTIKTSGZ"), compressed(t, broken)...),
		"cumulative text expansion":      beforeIEND(img, itxt(t, "payload-0", bytes.Repeat([]byte{'A'}, base64.StdEncoding.EncodedLen(pixelLimit)/2+1), true), itxt(t, "payload-1", bytes.Repeat([]byte{'A'}, base64.StdEncoding.EncodedLen(pixelLimit)/2+1), true)),
	} {
		t.Run(name, func(t *testing.T) {
			if _, kind, err := unwrapSegment(input); err == nil || kind != 0 {
				t.Fatalf("accepted malformed envelope: %d %v", kind, err)
			}
		})
	}
}
