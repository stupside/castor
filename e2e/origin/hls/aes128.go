package hls

import (
	"crypto/rand"
	"path/filepath"

	"github.com/stupside/castor/e2e/origin"
)

// AES128 encrypts TS segments under one key behind a tokenised URI; ffmpeg refuses to encrypt fMP4.
type AES128 struct{}

func (AES128) Name() string                 { return "hls-ts-aes128" }
func (AES128) Supports(origin.Layout) error { return nil }
func (AES128) Package(dir string, l origin.Layout) origin.Output {
	// Reload the IV-less keyinfo per segment so ffmpeg derives that segment's sequence IV,
	// rather than fixing every segment's IV to the first one's. ImplicitIV may then omit it safely.
	out := pack(dir, l, segments{ext: ".ts", mime: "video/mp2t", args: []string{"-hls_key_info_file", filepath.Join(dir, "keyinfo"), "-hls_flags", "periodic_rekey"}})
	out.Types[".bin"] = "application/octet-stream"
	key := make([]byte, 16)
	rand.Read(key)
	// With no IV line ffmpeg derives one itself and writes it into EXT-X-KEY.
	out.Files = map[string][]byte{"key.bin": key, "keyinfo": []byte("key.bin?token=e2e\n" + filepath.Join(dir, "key.bin") + "\n")}
	return out
}

func (AES128) Muxes() string { return origin.MPEGTS }
