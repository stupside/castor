// Package rokuchannel builds Castor's Roku channel: a SceneGraph app whose only
// job is to play a stream URL passed over ECP. It owns the launch-parameter names
// the launcher must use, and renders the channel from them so there is one source
// of truth.
package rokuchannel

import (
	"archive/zip"
	"bytes"
	"embed"
	"io/fs"
	"strings"
	"text/template"
)

// Launch parameter names the channel reads and the launcher must send.
const (
	ParamURL    = "url"
	ParamFormat = "format"
)

const title = "Castor"

//go:embed assets
var assets embed.FS

var data = struct {
	Title       string
	ParamURL    string
	ParamFormat string
}{title, ParamURL, ParamFormat}

// Zip renders the channel and packs it into a sideload archive with the manifest
// at the root.
func Zip() ([]byte, error) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)

	err := fs.WalkDir(assets, "assets", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		raw, err := assets.ReadFile(p)
		if err != nil {
			return err
		}
		name, content, err := render(strings.TrimPrefix(p, "assets/"), raw)
		if err != nil {
			return err
		}
		w, err := zw.Create(name)
		if err != nil {
			return err
		}
		_, err = w.Write(content)
		return err
	})
	if err != nil {
		return nil, err
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// render expands a .tmpl source and drops the suffix; anything else passes
// through unchanged.
func render(name string, raw []byte) (string, []byte, error) {
	if !strings.HasSuffix(name, ".tmpl") {
		return name, raw, nil
	}
	t, err := template.New(name).Option("missingkey=error").Parse(string(raw))
	if err != nil {
		return "", nil, err
	}
	var out bytes.Buffer
	if err := t.Execute(&out, data); err != nil {
		return "", nil, err
	}
	return strings.TrimSuffix(name, ".tmpl"), out.Bytes(), nil
}
