package hls

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"image"
	"image/color"
	"image/png"
	"testing"

	"github.com/stupside/castor/services/mediaserver/internal/media"

	"github.com/stupside/castor/services/mediaserver/internal/source/timeline"
)

func compressed(t *testing.T, body []byte) []byte {
	t.Helper()
	var out bytes.Buffer
	w := gzip.NewWriter(&out)
	if _, err := w.Write(body); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

func pixelEnvelope(t *testing.T, payload []byte, alpha uint8) []byte {
	t.Helper()
	rgb := append([]byte("TIKTIKPX"), binary.BigEndian.AppendUint32(nil, uint32(len(payload)))...)
	return pixelPNG(t, append(rgb, payload...), alpha)
}

func pixelPNG(t *testing.T, rgb []byte, alpha uint8) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, 7, (len(rgb)+20)/21))
	for i := range img.Pix {
		img.Pix[i] = 0xff
	}
	for i, v := range rgb {
		img.Pix[i/3*4+i%3] = v
	}
	for i := 3; i < len(img.Pix); i += 4 {
		img.Pix[i] = alpha
	}
	var out bytes.Buffer
	if err := png.Encode(&out, img); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

func TestPixelSegments(t *testing.T) {
	ts := bytes.Repeat(append([]byte{0x47}, make([]byte, 187)...), 4)
	for _, alpha := range []uint8{255, 128, 0} {
		body := pixelEnvelope(t, compressed(t, ts), alpha)
		got, wrapped, err := unwrapSegment(body)
		if err != nil || wrapped != media.HLSExtraSegmentTIKTIKPX || !bytes.Equal(got, ts) {
			t.Fatalf("alpha %d: %v, decoded %d bytes", alpha, err, len(got))
		}
	}
	t.Run("ordinary media", func(t *testing.T) {
		got, wrapped, err := unwrapSegment(ts)
		if err != nil || wrapped != 0 || !bytes.Equal(got, ts) {
			t.Fatalf("changed plain TS: %v", err)
		}
	})
	t.Run("ordinary PNG", func(t *testing.T) {
		img := image.NewRGBA(image.Rect(0, 0, 8, 8))
		img.Set(0, 0, color.RGBA{R: 42, A: 255})
		var b bytes.Buffer
		if err := png.Encode(&b, img); err != nil {
			t.Fatal(err)
		}
		got, wrapped, err := unwrapSegment(b.Bytes())
		if err != nil || wrapped != 0 || !bytes.Equal(got, b.Bytes()) {
			t.Fatalf("changed image: %v", err)
		}
	})
	badSync := bytes.Clone(ts)
	badSync[188] = 0
	checksum := compressed(t, ts)
	checksum[len(checksum)-8] ^= 1
	cases := map[string][]byte{
		"bad sync":        pixelEnvelope(t, compressed(t, badSync), 255),
		"unaligned":       pixelEnvelope(t, compressed(t, ts[:len(ts)-1]), 255),
		"too short":       pixelEnvelope(t, compressed(t, ts[:188]), 255),
		"not gzip":        pixelEnvelope(t, []byte("garbage"), 255),
		"checksum":        pixelEnvelope(t, checksum, 255),
		"expansion limit": pixelEnvelope(t, compressed(t, make([]byte, pixelLimit+1)), 255),
		"zero length":     pixelEnvelope(t, nil, 255),
		"truncated PNG":   []byte("\x89PNG\r\n\x1a\n"),
	}
	// A valid IHDR checksum makes the dimensions reach the allocation guard.
	huge := pixelEnvelope(t, compressed(t, ts), 255)
	binary.BigEndian.PutUint32(huge[16:20], 1<<20)
	binary.BigEndian.PutUint32(huge[20:24], 1<<20)
	binary.BigEndian.PutUint32(huge[29:33], crc32.ChecksumIEEE(huge[12:29]))
	cases["pixel allocation limit"] = huge
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			if _, _, err := unwrapSegment(body); err == nil {
				t.Fatal("accepted malformed envelope")
			}
		})
	}
}

func TestPixelEnvelopeAtDifferentOffsets(t *testing.T) {
	ts := bytes.Repeat(append([]byte{0x47}, make([]byte, 187)...), 4)
	payload := compressed(t, ts)
	header := binary.BigEndian.AppendUint32([]byte("TIKTIKPX"), uint32(len(payload)))
	envelope := append(header, payload...)
	// The fixture is seven RGB pixels (21 bytes) wide.
	for _, offset := range []int{0, 1, 2, 20, 8 * 21, 16 * 21, 32 * 21, 65537} {
		t.Run(fmt.Sprintf("byte_%d", offset), func(t *testing.T) {
			rgb := append(bytes.Repeat([]byte{0x42}, offset), envelope...)
			got, wrapped, err := unwrapSegment(pixelPNG(t, rgb, 128))
			if err != nil || wrapped != media.HLSExtraSegmentTIKTIKPX || !bytes.Equal(got, ts) {
				t.Fatalf("wrapped=%v, error=%v, got %d bytes", wrapped, err, len(got))
			}
		})
	}
	// A mention of the format in the prefix is not a framed gzip payload.
	rgb := append([]byte("TIKTIKPX in the prefix"), envelope...)
	if got, wrapped, err := unwrapSegment(pixelPNG(t, rgb, 255)); err != nil || wrapped != media.HLSExtraSegmentTIKTIKPX || !bytes.Equal(got, ts) {
		t.Fatal("missed envelope after an unframed signature")
	}

	prefix := bytes.Repeat([]byte{0x42}, 20)
	rgb = append(prefix, envelope...)
	binary.BigEndian.PutUint32(rgb[len(prefix)+8:], ^uint32(0))
	if _, _, err := unwrapSegment(pixelPNG(t, rgb, 255)); err == nil {
		t.Fatal("accepted truncated payload after leading rows")
	}
	rgb[len(prefix)] = 'X'
	ordinary := pixelPNG(t, rgb, 255)
	if got, wrapped, err := unwrapSegment(ordinary); err != nil || wrapped != 0 || !bytes.Equal(got, ordinary) {
		t.Fatal("changed PNG without an envelope signature")
	}
	if _, _, err := unwrapSegment(pixelPNG(t, append(make([]byte, 13), "TIKTIKPX"...), 255)); err == nil {
		t.Fatal("accepted signature without a length")
	}
}

func TestEncryptedPixelSegmentIsUnwrappedAfterDecryption(t *testing.T) {
	ts := bytes.Repeat(append([]byte{0x47}, make([]byte, 187)...), 4)
	clear := pixelEnvelope(t, compressed(t, ts), 0)
	secret, iv := bytes.Repeat([]byte{7}, 16), bytes.Repeat([]byte{9}, 16)
	pad := aes.BlockSize - len(clear)%aes.BlockSize
	padded := append(clear, bytes.Repeat([]byte{byte(pad)}, pad)...)
	block, err := aes.NewCipher(secret)
	if err != nil {
		t.Fatal(err)
	}
	sealed := make([]byte, len(padded))
	cipher.NewCBCEncrypter(block, iv).CryptBlocks(sealed, padded)
	got, unwrapped, err := (Decoder{}).Segment(t.Context(), timeline.Segment{URI: "segment", Key: timeline.Key{Method: aes128, URI: "key", IV: fmt.Sprintf("0x%x", iv)}}, sealed,
		func(_ context.Context, uri string) ([]byte, error) { return secret, nil })
	if err != nil || !bytes.Equal(got, ts) || unwrapped != media.HLSExtraSegmentTIKTIKPX {
		t.Fatalf("decrypted envelope: %v, decoded %d bytes", err, len(got))
	}
}
