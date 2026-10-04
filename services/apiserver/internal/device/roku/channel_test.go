package roku

import (
	"archive/zip"
	"bytes"
	"io"
	"testing"
)

func TestTheChannelZipRendersTheChannel(t *testing.T) {
	b, err := channelZip()
	if err != nil {
		t.Fatalf("channelZip() error = %v", err)
	}
	zr, err := zip.NewReader(bytes.NewReader(b), int64(len(b)))
	if err != nil {
		t.Fatalf("reading zip: %v", err)
	}
	files := map[string]string{}
	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatalf("open %s: %v", f.Name, err)
		}
		data, _ := io.ReadAll(rc)
		rc.Close()
		files[f.Name] = string(data)
	}
	if files["manifest"] == "" {
		t.Error("manifest missing from the archive root")
	}
}
