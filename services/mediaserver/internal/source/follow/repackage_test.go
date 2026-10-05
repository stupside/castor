package follow

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"testing/iotest"
	"time"

	"github.com/stupside/castor/services/mediaserver/internal/source/timeline"
)

// box is one ISO BMFF box around its children.
func box(kind string, children ...[]byte) []byte {
	payload := bytes.Join(children, nil)
	out := binary.BigEndian.AppendUint32(nil, uint32(8+len(payload)))
	return append(append(out, kind...), payload...)
}

// initWith is an init section describing one track per sample entry.
func initWith(entries ...string) []byte {
	var traks [][]byte
	for _, e := range entries {
		stsd := box("stsd", []byte{0, 0, 0, 0, 0, 0, 0, 1}, box(e))
		traks = append(traks, box("trak", box("mdia", box("minf", box("stbl", stsd)))))
	}
	return box("moov", traks...)
}

// stored is an origin serving resources by URI, and one window of fMP4 segments under an init.
type stored struct {
	resources map[string][]byte
	window    timeline.Window
	// fail answers every fragment read, when set.
	fail *timeline.Failure
	// breaks makes every fragment body fail before its first byte, when set.
	breaks bool
	reads  map[string]int
}

func (m *stored) Window(context.Context) (timeline.Window, error) { return m.window, nil }

func (m *stored) Read(_ context.Context, uri string, r timeline.Range) (io.ReadCloser, error) {
	if m.reads == nil {
		m.reads = map[string]int{}
	}
	m.reads[uri]++
	if m.fail != nil && strings.HasSuffix(uri, ".m4s") {
		return nil, m.fail
	}
	if m.breaks && strings.HasSuffix(uri, ".m4s") {
		return io.NopCloser(iotest.ErrReader(io.ErrUnexpectedEOF)), nil
	}
	b, ok := m.resources[uri]
	if !ok {
		return nil, &timeline.Failure{Status: http.StatusNotFound, Err: errors.New("no such resource")}
	}
	if r.Length > 0 {
		b = b[r.Offset : r.Offset+r.Length]
	}
	return io.NopCloser(bytes.NewReader(b)), nil
}

// fragmented is an origin of three fragments under one init whose tracks are entries.
func fragmented(entries ...string) *stored {
	m := &stored{resources: map[string][]byte{"https://origin.example/init.mp4": initWith(entries...)}}
	for n := range 3 {
		uri := fmt.Sprintf("https://origin.example/%d.m4s", n)
		m.resources[uri] = []byte(fmt.Sprintf("fragment %d", n))
		m.window.Segments = append(m.window.Segments, timeline.Segment{
			URI: uri, Duration: time.Second, Map: &timeline.Map{URI: "https://origin.example/init.mp4"},
			Place: timeline.Place{Start: int64(n), End: int64(n + 1)},
		})
	}
	return m
}

// labelled repackages by prefixing what it was given, so a test sees exactly what reached it.
func labelled(_ context.Context, fmp4 io.Reader, ts io.Writer) error {
	_, _ = io.WriteString(ts, "TS:")
	_, err := io.Copy(ts, fmp4)
	return err
}

func TestOnlyAnInitWhoseEveryTrackTSCarriesIsRepackaged(t *testing.T) {
	for _, tc := range []struct {
		entries []string
		want    bool
	}{
		{[]string{"avc1", "mp4a"}, true},
		{[]string{"hvc1", "ec-3"}, true},
		{[]string{"av01", "mp4a"}, false},
		{[]string{"encv", "enca"}, false},
		{nil, false},
	} {
		if got := tsCarries(initWith(tc.entries...)); got != tc.want {
			t.Errorf("tsCarries(%v) = %v, want %v", tc.entries, got, tc.want)
		}
	}
}

func TestAFragmentTSCannotCarryIsRelayedWithItsInit(t *testing.T) {
	m := fragmented("av01", "mp4a")
	server := serving(t, m, labelled)
	_, playlist := fetch(t, server.URL("primary").String())
	if !strings.Contains(playlist, `#EXT-X-MAP:URI="primary/init/0"`) || !strings.Contains(playlist, "\nprimary/2\n") {
		t.Fatalf("an AV1 timeline is not relayed through castor with its init:\n%s", playlist)
	}
	if _, init := fetch(t, server.base.JoinPath("primary", "init", "0").String()); init != string(m.resources["https://origin.example/init.mp4"]) {
		t.Errorf("the init section = %q, want the origin's", init)
	}
	if _, body := fetch(t, server.base.JoinPath("primary", "2").String()); body != "fragment 2" {
		t.Errorf("segment 2 = %q, want the origin's fragment as it is", body)
	}
}

func TestAFeedWithoutARepackagerRelaysTheFragmentAndInit(t *testing.T) {
	m := fragmented("avc1")
	server := serving(t, m, nil)
	_, playlist := fetch(t, server.URL("primary").String())
	if !strings.Contains(playlist, "#EXT-X-MAP:") || strings.Contains(playlist, "primary/1.ts") {
		t.Fatalf("playlist promises repackaging with no repackager: %s", playlist)
	}
	if status, body := fetch(t, server.base.JoinPath("primary", "1").String()); status != http.StatusOK || body != "fragment 1" {
		t.Errorf("segment = %d %q, want the original fragment", status, body)
	}
}

func TestAnAES128FragmentIsDecryptedBeforeItIsRepackaged(t *testing.T) {
	m := fragmented("avc1")
	secret, iv := bytes.Repeat([]byte{7}, 16), bytes.Repeat([]byte{9}, 16)
	clear := []byte("fragment 1")
	padded := append(bytes.Clone(clear), bytes.Repeat([]byte{6}, 6)...)
	block, _ := aes.NewCipher(secret)
	sealed := make([]byte, len(padded))
	cipher.NewCBCEncrypter(block, iv).CryptBlocks(sealed, padded)
	m.resources["https://origin.example/1.m4s"], m.resources["https://origin.example/key"] = sealed, secret
	for i := range m.window.Segments {
		m.window.Segments[i].Key = timeline.Key{Method: aes128, URI: "https://origin.example/key", IV: fmt.Sprintf("0x%x", iv)}
	}
	server := serving(t, m, labelled)
	_, playlist := fetch(t, server.URL("primary").String())
	if strings.Contains(playlist, "#EXT-X-KEY:METHOD=AES-128") {
		t.Errorf("castor serves the segments decrypted, yet the playlist still names the key:\n%s", playlist)
	}
	_, body := fetch(t, server.base.JoinPath("primary", "1.ts").String())
	if !strings.HasSuffix(body, string(clear)) {
		t.Errorf("segment 1 reached the repackager as %q, want it decrypted", body)
	}
}

func TestNothingIsListedBeforeCastorKnowsHowToServeIt(t *testing.T) {
	m := fragmented("avc1")
	init := m.resources["https://origin.example/init.mp4"]
	delete(m.resources, "https://origin.example/init.mp4")
	feed := newFeed("primary", m, time.Second, labelled)
	if err := feed.refresh(t.Context()); err == nil || feed.render != nil {
		t.Fatalf("rendered %q before the init section deciding its addressing could be read", feed.render)
	}
	m.resources["https://origin.example/init.mp4"] = init
	if err := feed.refresh(t.Context()); err != nil || !strings.Contains(string(feed.render), "primary/0.ts") {
		t.Errorf("once the init was read, render = %q (%v), want castor's MPEG-TS", feed.render, err)
	}
}
