package stealth

import (
	"fmt"
	"math/rand/v2"
	"strconv"
	"strings"
)

// Profile is a coherent set of browser fingerprint values for one browser.
type Profile struct {
	uaOS                string
	grease              string
	userAgent           string
	brands              [][2]string // [brand, majorVersion]
	fullVersionList     [][2]string // [brand, fullVersion]
	platform            string      // Client Hints platform (e.g. "Windows")
	platformVersion     string      // Client Hints platform version
	architecture        string
	bitness             string
	navigatorPlatform   string // navigator.platform value
	acceptLanguage      string
	languages           []string
	hardwareConcurrency int64
	deviceMemory        int
	screenWidth         int
	screenHeight        int
	webGLVendor         string
	webGLRenderer       string
	timezoneID          string
	noiseSeed           uint32  // per-session PRNG seed for canvas/audio/rect/font noise
	fontNoisePx         float64 // sub-pixel offset for measureText [0.001, 0.099]
	rectNoisePx         float64 // sub-pixel offset for getClientRects [0.001, 0.099]
	audioNoiseMag       float64 // noise magnitude for AudioContext [0.00001, 0.0001]
}

type platformPreset struct {
	uaOS              string // OS fragment inside the UA string
	navigatorPlatform string
	chPlatform        string
	chPlatformVersion string
	architecture      string
	bitness           string
	webGLRenderers    []webGLPreset
}

type webGLPreset struct {
	vendor   string
	renderer string
}

var platformPresets = []platformPreset{
	{
		uaOS:              "Windows NT 10.0; Win64; x64",
		navigatorPlatform: "Win32",
		chPlatform:        "Windows",
		chPlatformVersion: "10.0.0",
		architecture:      "x86",
		bitness:           "64",
		webGLRenderers: []webGLPreset{
			{"Google Inc. (Intel)", "ANGLE (Intel, Intel(R) UHD Graphics 630 Direct3D11 vs_5_0 ps_5_0, D3D11)"},
			{"Google Inc. (Intel)", "ANGLE (Intel, Intel(R) UHD Graphics 770 Direct3D11 vs_5_0 ps_5_0, D3D11)"},
			{"Google Inc. (NVIDIA)", "ANGLE (NVIDIA, NVIDIA GeForce GTX 1650 Direct3D11 vs_5_0 ps_5_0, D3D11)"},
			{"Google Inc. (NVIDIA)", "ANGLE (NVIDIA, NVIDIA GeForce RTX 3060 Direct3D11 vs_5_0 ps_5_0, D3D11)"},
		},
	},
	{
		uaOS:              "Windows NT 10.0; Win64; x64",
		navigatorPlatform: "Win32",
		chPlatform:        "Windows",
		chPlatformVersion: "15.0.0",
		architecture:      "x86",
		bitness:           "64",
		webGLRenderers: []webGLPreset{
			{"Google Inc. (Intel)", "ANGLE (Intel, Intel(R) UHD Graphics 630 Direct3D11 vs_5_0 ps_5_0, D3D11)"},
			{"Google Inc. (Intel)", "ANGLE (Intel, Intel(R) UHD Graphics 770 Direct3D11 vs_5_0 ps_5_0, D3D11)"},
			{"Google Inc. (NVIDIA)", "ANGLE (NVIDIA, NVIDIA GeForce GTX 1650 Direct3D11 vs_5_0 ps_5_0, D3D11)"},
			{"Google Inc. (NVIDIA)", "ANGLE (NVIDIA, NVIDIA GeForce RTX 3060 Direct3D11 vs_5_0 ps_5_0, D3D11)"},
		},
	},
	{
		uaOS:              "Macintosh; Intel Mac OS X 10_15_7",
		navigatorPlatform: "MacIntel",
		chPlatform:        "macOS",
		chPlatformVersion: "14.5.0",
		architecture:      "arm",
		bitness:           "64",
		webGLRenderers: []webGLPreset{
			{"Google Inc. (Apple)", "ANGLE (Apple, Apple M1, OpenGL 4.1)"},
		},
	},
	{
		uaOS:              "Macintosh; Intel Mac OS X 10_15_7",
		navigatorPlatform: "MacIntel",
		chPlatform:        "macOS",
		chPlatformVersion: "14.5.0",
		architecture:      "x86",
		bitness:           "64",
		webGLRenderers: []webGLPreset{
			{"Google Inc. (Intel Inc.)", "ANGLE (Intel Inc., Intel Iris Plus Graphics, OpenGL 4.1)"},
		},
	},
}

type screenPreset struct {
	width  int
	height int
}

var screenPresets = []screenPreset{
	{1920, 1080},
	{2560, 1440},
	{1366, 768},
	{1536, 864},
	{1680, 1050},
}

type localePreset struct {
	timezoneID     string
	acceptLanguage string
	languages      []string
}

var localePresets = []localePreset{
	{"America/New_York", "en-US,en;q=0.9", []string{"en-US", "en"}},
	{"America/Chicago", "en-US,en;q=0.9", []string{"en-US", "en"}},
	{"America/Los_Angeles", "en-US,en;q=0.9", []string{"en-US", "en"}},
	{"Europe/London", "en-GB,en;q=0.9,en-US;q=0.8", []string{"en-GB", "en", "en-US"}},
}

var hardwareConcurrencies = []int64{4, 8, 12, 16}

var deviceMemories = []int{4, 8}
var greaseBrands = []string{`Not A(Brand`, `Not/A)Brand`, `Not_A Brand`}

// New draws a profile, everything but the version, which only the running browser can state.
func New() *Profile {
	plat := pick(platformPresets)
	webgl := pick(plat.webGLRenderers)
	scr := pick(screenPresets)
	loc := pick(localePresets)

	return &Profile{
		uaOS:                plat.uaOS,
		grease:              pick(greaseBrands),
		platform:            plat.chPlatform,
		platformVersion:     plat.chPlatformVersion,
		architecture:        plat.architecture,
		bitness:             plat.bitness,
		navigatorPlatform:   plat.navigatorPlatform,
		acceptLanguage:      loc.acceptLanguage,
		languages:           loc.languages,
		hardwareConcurrency: pick(hardwareConcurrencies),
		deviceMemory:        pick(deviceMemories),
		screenWidth:         scr.width,
		screenHeight:        scr.height,
		webGLVendor:         webgl.vendor,
		webGLRenderer:       webgl.renderer,
		timezoneID:          loc.timezoneID,
		noiseSeed:           rand.Uint32(),
		fontNoisePx:         0.001 + rand.Float64()*0.098,
		rectNoisePx:         0.001 + rand.Float64()*0.098,
		audioNoiseMag:       0.00001 + rand.Float64()*0.00009,
	}
}

func pick[T any](choices []T) T { return choices[rand.IntN(len(choices))] }

// identify pins the user agent and client hints to the version a browser reports as its product.
func (p *Profile) identify(product string) error {
	_, full, _ := strings.Cut(product, "/")
	major, _, _ := strings.Cut(full, ".")
	if _, err := strconv.Atoi(major); err != nil || full == "" {
		return fmt.Errorf("browser product %q states no version", product)
	}
	// A real Chrome reports only its major version in the user agent.
	p.userAgent = fmt.Sprintf(
		"Mozilla/5.0 (%s) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/%s.0.0.0 Safari/537.36",
		p.uaOS, major,
	)
	p.brands = [][2]string{{p.grease, "8"}, {"Chromium", major}, {"Google Chrome", major}}
	p.fullVersionList = [][2]string{{p.grease, "8.0.0.0"}, {"Chromium", full}, {"Google Chrome", full}}
	return nil
}

// Screen is the size of the profile's display.
func (p *Profile) Screen() (width, height int) { return p.screenWidth, p.screenHeight }
