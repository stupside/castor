package hls_test

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"encoding/binary"
	"encoding/hex"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os/exec"
	"strings"
	"testing"

	"github.com/stupside/castor/e2e/origin"
	"github.com/stupside/castor/e2e/origin/codec"
	"github.com/stupside/castor/e2e/origin/colour"
	"github.com/stupside/castor/e2e/origin/hls"
	"github.com/stupside/castor/e2e/origin/serving/playlist"
)

type capturePlaylist chan string

func (capturePlaylist) Name() string { return "capture" }

func (c capturePlaylist) Wrap(next http.Handler, _ origin.Published) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, ".m3u8") {
			next.ServeHTTP(w, r)
			return
		}
		rec := httptest.NewRecorder()
		next.ServeHTTP(rec, r)
		c <- rec.Body.String()
		maps.Copy(w.Header(), rec.Header())
		w.WriteHeader(rec.Code)
		_, _ = w.Write(rec.Body.Bytes())
	})
}

func TestImplicitIVDoesNotCorruptTheEncryptedSegments(t *testing.T) {
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("requires ffmpeg")
	}
	captured := make(capturePlaylist, 1)
	src := origin.Start(t, ffmpeg, origin.Stream{
		Packager: hls.AES128{}, Video: codec.H264{}, Transfer: colour.SDR{},
		Heights: []int{90}, Depth: 8, Seconds: 2,
	}, []origin.Behaviour{playlist.ImplicitIV{}, captured})
	base, err := url.Parse(src.URL)
	if err != nil {
		t.Fatal(err)
	}
	fetch := func(relative string) []byte {
		t.Helper()
		u, err := base.Parse(relative)
		if err != nil {
			t.Fatal(err)
		}
		resp, err := http.Get(u.String())
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		body, err := io.ReadAll(resp.Body)
		if err != nil || resp.StatusCode != http.StatusOK {
			t.Fatalf("fetching %s: status %d, error %v", u, resp.StatusCode, err)
		}
		return body
	}
	if body := fetch(src.URL); bytes.Contains(body, []byte(",IV=")) {
		t.Fatal("the fixture still advertises an explicit IV")
	}
	key := fetch("key.bin?token=e2e")
	block, err := aes.NewCipher(key)
	if err != nil {
		t.Fatal(err)
	}
	var explicit []byte
	sequence := uint64(0)
	checked := 0
	for line := range strings.SplitSeq(<-captured, "\n") {
		if _, iv, ok := strings.Cut(line, ",IV=0x"); ok {
			explicit, err = hex.DecodeString(iv)
			if err != nil || len(explicit) != aes.BlockSize {
				t.Fatalf("invalid generated IV %q", iv)
			}
		}
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		body := fetch(line)
		implicit := make([]byte, aes.BlockSize)
		binary.BigEndian.PutUint64(implicit[8:], sequence)
		original, played := bytes.Clone(body), bytes.Clone(body)
		cipher.NewCBCDecrypter(block, explicit).CryptBlocks(original, original)
		cipher.NewCBCDecrypter(block, implicit).CryptBlocks(played, played)
		if !bytes.Equal(original, played) {
			t.Fatalf("segment %d ciphertext changes its plaintext when the fixture removes its explicit IV", sequence)
		}
		sequence++
		checked++
	}
	if checked < 2 {
		t.Fatal("the fixture did not publish a segment beyond sequence zero")
	}
}
