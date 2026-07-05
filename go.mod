module github.com/stupside/castor

go 1.26.0

require (
	github.com/charmbracelet/bubbles v1.0.0
	github.com/charmbracelet/bubbletea v1.3.10
	github.com/charmbracelet/lipgloss v1.1.0
	github.com/chromedp/cdproto v0.0.0-20250803210736-d308e07a266d
	github.com/chromedp/chromedp v0.14.2
	github.com/eliukblau/pixterm v1.3.2
	github.com/ggerganov/whisper.cpp/bindings/go v0.0.0-00010101000000-000000000000
	github.com/go-playground/validator/v10 v10.30.1
	github.com/go-viper/mapstructure/v2 v2.5.0
	github.com/huin/goupnp v1.3.0
	github.com/knadh/koanf/parsers/yaml v1.1.0
	github.com/knadh/koanf/providers/confmap v1.0.0
	github.com/knadh/koanf/providers/env v1.1.0
	github.com/knadh/koanf/providers/file v1.2.1
	github.com/knadh/koanf/v2 v2.3.2
	github.com/urfave/cli/v3 v3.6.2
	github.com/vishen/go-chromecast v0.3.4
	golang.org/x/sync v0.20.0
)

// The whisper.cpp Go bindings are vendored as a git submodule at
// third_party/whisper.cpp. They are cgo-based and require a pre-built
// libwhisper.a; see the top-level Makefile.
replace github.com/ggerganov/whisper.cpp/bindings/go => ./third_party/whisper.cpp/bindings/go

require (
	github.com/atotto/clipboard v0.1.4 // indirect
	github.com/aymanbagabas/go-osc52/v2 v2.0.1 // indirect
	github.com/buger/jsonparser v1.1.1 // indirect
	github.com/charmbracelet/colorprofile v0.4.1 // indirect
	github.com/charmbracelet/x/ansi v0.11.6 // indirect
	github.com/charmbracelet/x/cellbuf v0.0.15 // indirect
	github.com/charmbracelet/x/term v0.2.2 // indirect
	github.com/chromedp/sysutil v1.1.0 // indirect
	github.com/clipperhouse/displaywidth v0.9.0 // indirect
	github.com/clipperhouse/stringish v0.1.1 // indirect
	github.com/clipperhouse/uax29/v2 v2.5.0 // indirect
	github.com/disintegration/imaging v1.6.2 // indirect
	github.com/erikgeiser/coninput v0.0.0-20211004153227-1c3628e74d0f // indirect
	github.com/fsnotify/fsnotify v1.9.0 // indirect
	github.com/gabriel-vasile/mimetype v1.4.13 // indirect
	github.com/go-json-experiment/json v0.0.0-20260214004413-d219187c3433 // indirect
	github.com/go-playground/locales v0.14.1 // indirect
	github.com/go-playground/universal-translator v0.18.1 // indirect
	github.com/gobwas/httphead v0.1.0 // indirect
	github.com/gobwas/pool v0.2.1 // indirect
	github.com/gobwas/ws v1.4.0 // indirect
	github.com/gogo/protobuf v1.3.2 // indirect
	github.com/h2non/filetype v1.1.3 // indirect
	github.com/knadh/koanf/maps v0.1.2 // indirect
	github.com/leodido/go-urn v1.4.0 // indirect
	github.com/lucasb-eyer/go-colorful v1.3.0 // indirect
	github.com/mattn/go-isatty v0.0.20 // indirect
	github.com/mattn/go-localereader v0.0.1 // indirect
	github.com/mattn/go-runewidth v0.0.19 // indirect
	github.com/mitchellh/copystructure v1.2.0 // indirect
	github.com/mitchellh/go-homedir v1.1.0 // indirect
	github.com/mitchellh/reflectwalk v1.0.2 // indirect
	github.com/muesli/ansi v0.0.0-20230316100256-276c6243b2f6 // indirect
	github.com/muesli/cancelreader v0.2.2 // indirect
	github.com/muesli/termenv v0.16.0 // indirect
	github.com/pkg/errors v0.9.1 // indirect
	github.com/rivo/uniseg v0.4.7 // indirect
	github.com/sahilm/fuzzy v0.1.1 // indirect
	github.com/sirupsen/logrus v1.9.4 // indirect
	github.com/xo/terminfo v0.0.0-20220910002029-abceb7e1c41e // indirect
	go.yaml.in/yaml/v3 v3.0.4 // indirect
	golang.org/x/crypto v0.48.0 // indirect
	golang.org/x/image v0.41.0 // indirect
	golang.org/x/sys v0.41.0 // indirect
	golang.org/x/text v0.37.0 // indirect
	gopkg.in/ini.v1 v1.67.1 // indirect
)
