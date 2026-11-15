package hls

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A device starts startFragments behind the end of a playlist that goes on, so it is handed one only once it lists them.
func TestAPlaylistLandsOnceADeviceCanStartOnIt(t *testing.T) {
	listing := func(fragments int, ended bool) string {
		var b strings.Builder
		b.WriteString("#EXTM3U\n#EXT-X-VERSION:7\n#EXT-X-TARGETDURATION:4\n")
		for range fragments {
			b.WriteString("#EXTINF:4.000000,\nseg_00000.m4s\n")
		}
		if ended {
			b.WriteString("#EXT-X-ENDLIST\n")
		}
		return b.String()
	}
	for _, tt := range []struct {
		name     string
		playlist *string
		listed   bool
	}{
		{name: "none written"},
		{name: "no fragment listed", playlist: new(listing(0, false))},
		{name: "one fragment", playlist: new(listing(1, false))},
		{name: "one fragment less than it takes", playlist: new(listing(startFragments-1, false))},
		{name: "as many fragments as it takes", playlist: new(listing(startFragments, false)), listed: true},
		{name: "more fragments than it takes", playlist: new(listing(startFragments+5, false)), listed: true},
		{name: "a short stream that ended", playlist: new(listing(1, true)), listed: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			if tt.playlist != nil {
				if err := os.WriteFile(filepath.Join(dir, "stream.m3u8"), []byte(*tt.playlist), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			store, err := openSegmentStore(dir, "stream.m3u8")
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			landed, err := store.landed()
			if err != nil {
				t.Fatal(err)
			}
			switch want := tt.listed; {
			case want && landed != int64(len(*tt.playlist)):
				t.Errorf("landed %d bytes, want the playlist's %d", landed, len(*tt.playlist))
			case !want && landed != 0:
				t.Errorf("landed %d bytes, want none until a device can start on it", landed)
			}
		})
	}
}
