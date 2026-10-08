module github.com/stupside/castor

go 1.27.1

require (
	buf.build/gen/go/bufbuild/protovalidate/protocolbuffers/go v1.36.12-20260825204119-511051f7f437.2
	buf.build/go/protovalidate v1.4.0
	charm.land/bubbles/v2 v2.2.1
	charm.land/bubbletea/v2 v2.0.10
	charm.land/lipgloss/v2 v2.0.6
	charm.land/log/v2 v2.0.1
	connectrpc.com/connect v1.21.0
	connectrpc.com/grpchealth v1.5.0
	connectrpc.com/grpcreflect v1.3.1
	connectrpc.com/validate v0.7.0
	github.com/Eyevinn/dash-mpd v0.18.1
	github.com/Eyevinn/hls-m3u8 v0.6.5
	github.com/Eyevinn/mp4ff v0.59.0
	github.com/at-wat/ebml-go v0.19.4
	github.com/charmbracelet/colorprofile v0.4.3
	github.com/charmbracelet/x/exp/teatest/v2 v2.0.0-20261004011457-ad85c59fdf4e
	github.com/chromedp/cdproto v0.0.0-20260922220944-a19bff23514f
	github.com/chromedp/chromedp v0.16.0
	github.com/eliukblau/pixterm v1.3.3
	github.com/ggerganov/whisper.cpp/bindings/go v0.0.0-00010101000000-000000000000
	github.com/go-playground/validator/v10 v10.30.5
	github.com/go-viper/mapstructure/v2 v2.5.0
	github.com/huin/goupnp v1.3.0
	github.com/icholy/digest v1.2.0
	github.com/knadh/koanf/parsers/yaml v1.1.1
	github.com/knadh/koanf/providers/env v1.1.0
	github.com/knadh/koanf/providers/file v1.2.1
	github.com/knadh/koanf/v2 v2.3.8
	github.com/looplab/fsm v1.0.4
	github.com/urfave/cli/v3 v3.14.0
	github.com/vishen/go-chromecast v0.3.4
	go.yaml.in/yaml/v3 v3.0.5
	golang.org/x/net v0.59.0
	golang.org/x/sync v0.23.0
	google.golang.org/protobuf v1.36.12
)

// The whisper.cpp Go bindings are vendored as a git submodule at
// third_party/whisper.cpp. They are cgo-based and require a pre-built
// libwhisper.a; see the top-level Makefile.
replace github.com/ggerganov/whisper.cpp/bindings/go => ./third_party/whisper.cpp/bindings/go

require (
	cel.dev/cel-go v0.32.0 // indirect
	cel.dev/expr v0.25.3 // indirect
	github.com/antlr4-go/antlr/v4 v4.13.1 // indirect
	github.com/atotto/clipboard v0.1.4 // indirect
	github.com/aymanbagabas/go-udiff v0.4.1 // indirect
	github.com/barkimedes/go-deepcopy v0.0.0-20220514131651-17c30cfc62df // indirect
	github.com/buger/jsonparser v1.6.1 // indirect
	github.com/cenkalti/backoff v2.2.1+incompatible // indirect
	github.com/charmbracelet/ultraviolet v0.0.0-20260811164956-006e29f97886 // indirect
	github.com/charmbracelet/x/ansi v0.11.8 // indirect
	github.com/charmbracelet/x/exp/golden v0.1.0 // indirect
	github.com/charmbracelet/x/term v0.2.2 // indirect
	github.com/charmbracelet/x/termios v0.1.1 // indirect
	github.com/charmbracelet/x/windows v0.2.2 // indirect
	github.com/chromedp/sysutil v1.1.0 // indirect
	github.com/clipperhouse/displaywidth v0.11.0 // indirect
	github.com/clipperhouse/uax29/v2 v2.7.0 // indirect
	github.com/disintegration/imaging v1.6.2 // indirect
	github.com/fsnotify/fsnotify v1.10.1 // indirect
	github.com/gabriel-vasile/mimetype v1.4.15 // indirect
	github.com/go-json-experiment/json v0.0.0-20260820222146-c27c302e5fc3 // indirect
	github.com/go-logfmt/logfmt v0.6.1 // indirect
	github.com/go-playground/locales v0.14.2 // indirect
	github.com/go-playground/universal-translator v0.18.2 // indirect
	github.com/gobwas/httphead v0.1.0 // indirect
	github.com/gobwas/pool v0.2.1 // indirect
	github.com/gobwas/ws v1.4.0 // indirect
	github.com/gogo/protobuf v1.3.2 // indirect
	github.com/grandcat/zeroconf v1.0.0 // indirect
	github.com/knadh/koanf/maps v0.1.3 // indirect
	github.com/leodido/go-urn v1.5.0 // indirect
	github.com/lucasb-eyer/go-colorful v1.4.1 // indirect
	github.com/mattn/go-runewidth v0.0.30 // indirect
	github.com/miekg/dns v1.1.73 // indirect
	github.com/mitchellh/copystructure v1.2.0 // indirect
	github.com/mitchellh/reflectwalk v1.0.2 // indirect
	github.com/muesli/cancelreader v0.2.2 // indirect
	github.com/pkg/errors v0.9.1 // indirect
	github.com/rivo/uniseg v0.4.7 // indirect
	github.com/sahilm/fuzzy v0.1.3 // indirect
	github.com/sirupsen/logrus v1.10.2 // indirect
	github.com/xo/terminfo v1.2.0 // indirect
	golang.org/x/crypto v0.57.0 // indirect
	golang.org/x/exp v0.0.0-20260908205506-85c1c2202aba // indirect
	golang.org/x/image v0.46.0 // indirect
	golang.org/x/sys v0.48.0 // indirect
	golang.org/x/text v0.42.0 // indirect
	google.golang.org/genproto/googleapis/api v0.0.0-20260819154853-08b0e4226688 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20260819154853-08b0e4226688 // indirect
)

tool (
	connectrpc.com/connect/cmd/protoc-gen-connect-go
	google.golang.org/protobuf/cmd/protoc-gen-go
)

ignore ./node_modules
