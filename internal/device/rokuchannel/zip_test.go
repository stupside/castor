package rokuchannel

import (
	"archive/zip"
	"bytes"
	"io"
	"strings"
	"testing"
)

// TestZipRendersChannel drives the real Zip() to confirm the embedded templates
// expand (missingkey=error would fail on any unknown field), the manifest lands
// at the archive root, and the scene wires the launch params the launcher sends.
func TestZipRendersChannel(t *testing.T) {
	b, err := Zip()
	if err != nil {
		t.Fatalf("Zip() error = %v", err)
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

	if _, ok := files["manifest"]; !ok {
		t.Errorf("manifest must be at the archive root; got files: %v", keys(files))
	}
	if got := files["manifest"]; !strings.Contains(got, "title="+Title) {
		t.Errorf("manifest title not rendered: %q", got)
	}
	// The .tmpl suffix must be dropped and the params injected.
	scene, ok := files["components/MainScene.brs"]
	if !ok {
		t.Fatalf("MainScene.brs missing (template not rendered); files: %v", keys(files))
	}
	if !strings.Contains(scene, `a["`+ParamURL+`"]`) || !strings.Contains(scene, `a["`+ParamFormat+`"]`) {
		t.Errorf("scene did not wire launch params:\n%s", scene)
	}
}

func keys(m map[string]string) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return out
}
