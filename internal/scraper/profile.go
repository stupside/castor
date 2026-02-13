package scraper

import (
	"fmt"
	"math/rand/v2"
)

// Profile holds a coherent set of browser fingerprint values for a single
// scraping session. Every field is internally consistent — UA, platform,
// WebGL, locale, Client Hints, etc. all match the same virtual identity.
type Profile struct {
	UserAgent           string
	Brands              [][2]string // [brand, majorVersion]
	FullVersionList     [][2]string // [brand, fullVersion]
	Platform            string      // Client Hints platform (e.g. "Windows")
	PlatformVersion     string      // Client Hints platform version
	Architecture        string
	Bitness             string
	NavigatorPlatform   string // navigator.platform value
	AcceptLanguage      string
	Languages           []string
	HardwareConcurrency int64
	DeviceMemory        int
	ScreenWidth         int
	ScreenHeight        int
	CenterX             float64 // ScreenWidth/2, pre-computed for MouseClickXY
	CenterY             float64 // ScreenHeight/2, pre-computed for MouseClickXY
	ColorDepth          int
	WebGLVendor         string
	WebGLRenderer       string
	TimezoneID          string
	NoiseSeed           uint32  // per-session PRNG seed for canvas/audio/rect/font noise
	FontNoisePx         float64 // sub-pixel offset for measureText [0.001, 0.099]
	RectNoisePx         float64 // sub-pixel offset for getClientRects [0.001, 0.099]
	AudioNoiseMag       float64 // noise magnitude for AudioContext [0.00001, 0.0001]
}

// --- platform presets ---

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
			{"Google Inc. (Intel Inc.)", "ANGLE (Intel Inc., Intel Iris Plus Graphics, OpenGL 4.1)"},
		},
	},
}

// --- screen presets ---

type screenPreset struct {
	width   int
	height  int
	centerX float64
	centerY float64
}

var screenPresets = []screenPreset{
	{1920, 1080, 960, 540},
	{2560, 1440, 1280, 720},
	{1366, 768, 683, 384},
	{1536, 864, 768, 432},
	{1680, 1050, 840, 525},
}

// --- locale presets ---

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

// --- chrome version pool ---

type chromeVersion struct {
	major string
	full  string
}

var chromeVersions = []chromeVersion{
	{"131", "131.0.0.0"},
	{"132", "132.0.0.0"},
	{"133", "133.0.0.0"},
}

// --- other pools ---

var hardwareConcurrencies = []int64{4, 8, 12, 16}
var deviceMemories = []int{4, 8, 16}
var greaseBrands = []string{`Not A(Brand`, `Not/A)Brand`, `Not_A Brand`}

// NewProfile builds a randomized but internally-consistent browser fingerprint.
func NewProfile() *Profile {
	plat := platformPresets[rand.IntN(len(platformPresets))]
	webgl := plat.webGLRenderers[rand.IntN(len(plat.webGLRenderers))]
	scr := screenPresets[rand.IntN(len(screenPresets))]
	loc := localePresets[rand.IntN(len(localePresets))]
	ver := chromeVersions[rand.IntN(len(chromeVersions))]
	grease := greaseBrands[rand.IntN(len(greaseBrands))]
	hwConc := hardwareConcurrencies[rand.IntN(len(hardwareConcurrencies))]
	devMem := deviceMemories[rand.IntN(len(deviceMemories))]

	noiseSeed := rand.Uint32()
	fontNoisePx := 0.001 + rand.Float64()*0.098
	rectNoisePx := 0.001 + rand.Float64()*0.098
	audioNoiseMag := 0.00001 + rand.Float64()*0.00009

	ua := fmt.Sprintf(
		"Mozilla/5.0 (%s) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/%s Safari/537.36",
		plat.uaOS, ver.full,
	)

	brands := buildBrands(grease, ver.major)
	fullVersionList := buildFullVersionList(grease, ver.full)

	return &Profile{
		UserAgent:           ua,
		Brands:              brands,
		FullVersionList:     fullVersionList,
		Platform:            plat.chPlatform,
		PlatformVersion:     plat.chPlatformVersion,
		Architecture:        plat.architecture,
		Bitness:             plat.bitness,
		NavigatorPlatform:   plat.navigatorPlatform,
		AcceptLanguage:      loc.acceptLanguage,
		Languages:           loc.languages,
		HardwareConcurrency: hwConc,
		DeviceMemory:        devMem,
		ScreenWidth:         scr.width,
		ScreenHeight:        scr.height,
		CenterX:             scr.centerX,
		CenterY:             scr.centerY,
		ColorDepth:          24,
		WebGLVendor:         webgl.vendor,
		WebGLRenderer:       webgl.renderer,
		TimezoneID:          loc.timezoneID,
		NoiseSeed:           noiseSeed,
		FontNoisePx:         fontNoisePx,
		RectNoisePx:         rectNoisePx,
		AudioNoiseMag:       audioNoiseMag,
	}
}

func buildBrands(grease, major string) [][2]string {
	return [][2]string{
		{grease, "8"},
		{"Chromium", major},
		{"Google Chrome", major},
	}
}

func buildFullVersionList(grease, full string) [][2]string {
	return [][2]string{
		{grease, "8.0.0.0"},
		{"Chromium", full},
		{"Google Chrome", full},
	}
}

