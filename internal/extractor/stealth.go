package extractor

import (
	"context"
	_ "embed"
	"fmt"
	"strings"

	"github.com/chromedp/cdproto/emulation"
	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/chromedp"

	"github.com/stupside/castor/internal/app"
)

//go:embed js/stealth_tostring.js
var stealthToStringJS string

//go:embed js/stealth_plugins.js
var stealthPluginsJS string

//go:embed js/stealth_chrome.js
var stealthChromeJS string

//go:embed js/stealth_permissions.js
var stealthPermissionsJS string

//go:embed js/stealth_webgl.js
var stealthWebGLJS string

//go:embed js/stealth_device_memory.js
var stealthDeviceMemoryJS string

//go:embed js/stealth_notification.js
var stealthNotificationJS string

//go:embed js/stealth_screen.js
var stealthScreenJS string

//go:embed js/stealth_webrtc.js
var stealthWebRTCJS string

//go:embed js/stealth_canvas.js
var stealthCanvasJS string

//go:embed js/stealth_audio.js
var stealthAudioJS string

//go:embed js/stealth_client_rects.js
var stealthClientRectsJS string

//go:embed js/stealth_font_metric.js
var stealthFontMetricJS string

//go:embed js/stealth_stack_trace.js
var stealthStackTraceJS string

// buildStealthJS joins all stealth snippets and fills placeholders from a Profile.
func buildStealthJS(profile *Profile) string {
	snippets := []string{
		stealthToStringJS,
		stealthPluginsJS,
		stealthChromeJS,
		stealthPermissionsJS,
		stealthWebGLJS,
		stealthDeviceMemoryJS,
		stealthNotificationJS,
		stealthScreenJS,
		stealthWebRTCJS,
		stealthCanvasJS,
		stealthAudioJS,
		stealthClientRectsJS,
		stealthFontMetricJS,
		stealthStackTraceJS,
	}
	joined := strings.Join(snippets, "\n")

	r := strings.NewReplacer(
		"__DEVICE_MEMORY__", fmt.Sprintf("%d", profile.DeviceMemory),
		"__COLOR_DEPTH__", fmt.Sprintf("%d", profile.ColorDepth),
		"__WEBGL_VENDOR__", profile.WebGLVendor,
		"__WEBGL_RENDERER__", profile.WebGLRenderer,
		"__NOISE_SEED__", fmt.Sprintf("%d", profile.NoiseSeed),
		"__FONT_NOISE_PX__", fmt.Sprintf("%.6f", profile.FontNoisePx),
		"__RECT_NOISE_PX__", fmt.Sprintf("%.6f", profile.RectNoisePx),
		"__AUDIO_NOISE_MAG__", fmt.Sprintf("%.10f", profile.AudioNoiseMag),
	)
	return r.Replace(joined)
}

// allocatorOpts returns chromedp exec-allocator options that avoid common
// headless-detection flags. It reads window size and UA from the profile.
func allocatorOpts(cfg app.BrowserConfig, profile *Profile) []chromedp.ExecAllocatorOption {
	var headlessVal string
	if cfg.Headless {
		headlessVal = "new"
	}

	return []chromedp.ExecAllocatorOption{
		chromedp.ExecPath(cfg.ChromePath),

		chromedp.NoFirstRun,
		chromedp.NoDefaultBrowserCheck,

		chromedp.Flag("headless", headlessVal),
		chromedp.Flag("no-sandbox", cfg.NoSandbox),

		chromedp.Flag("disable-blink-features", "AutomationControlled"),
		chromedp.Flag("disable-infobars", true),
		chromedp.Flag("enable-features", "NetworkService,NetworkServiceInProcess"),
		chromedp.Flag("disable-background-timer-throttling", true),
		chromedp.Flag("disable-backgrounding-occluded-windows", true),
		chromedp.Flag("disable-renderer-backgrounding", true),
		chromedp.Flag("webrtc-ip-handling-policy", "disable_non_proxied_udp"),

		chromedp.Flag("autoplay-policy", "no-user-gesture-required"),

		chromedp.WindowSize(profile.ScreenWidth, profile.ScreenHeight),

		chromedp.UserAgent(profile.UserAgent),
	}
}

// injectStealth returns a chromedp action that injects the stealth script
// before any page JS runs, parameterized by the given profile.
func injectStealth(profile *Profile) chromedp.ActionFunc {
	return func(ctx context.Context) error {
		js := buildStealthJS(profile)
		_, err := page.AddScriptToEvaluateOnNewDocument(js).Do(ctx)
		return err
	}
}

// injectCDPStealth returns a chromedp action that uses CDP-level overrides to
// mask automation signals that cannot be covered by JS injection alone.
func injectCDPStealth(profile *Profile) chromedp.ActionFunc {
	return func(ctx context.Context) error {
		if err := emulation.SetAutomationOverride(false).Do(ctx); err != nil {
			return err
		}

		if err := emulation.SetFocusEmulationEnabled(true).Do(ctx); err != nil {
			return err
		}

		if err := emulation.SetHardwareConcurrencyOverride(profile.HardwareConcurrency).Do(ctx); err != nil {
			return err
		}

		if err := emulation.SetTimezoneOverride(profile.TimezoneID).Do(ctx); err != nil {
			return err
		}

		locale := profile.Languages[0]
		if err := emulation.SetLocaleOverride().WithLocale(locale).Do(ctx); err != nil {
			return err
		}

		ua := emulation.SetUserAgentOverride(profile.UserAgent)
		ua.AcceptLanguage = profile.AcceptLanguage
		ua.Platform = profile.NavigatorPlatform

		brands := make([]*emulation.UserAgentBrandVersion, len(profile.Brands))
		for i, b := range profile.Brands {
			brands[i] = &emulation.UserAgentBrandVersion{Brand: b[0], Version: b[1]}
		}
		fullVersionList := make([]*emulation.UserAgentBrandVersion, len(profile.FullVersionList))
		for i, b := range profile.FullVersionList {
			fullVersionList[i] = &emulation.UserAgentBrandVersion{Brand: b[0], Version: b[1]}
		}

		ua.UserAgentMetadata = &emulation.UserAgentMetadata{
			Brands:          brands,
			FullVersionList: fullVersionList,
			Platform:        profile.Platform,
			PlatformVersion: profile.PlatformVersion,
			Architecture:    profile.Architecture,
			Model:           "",
			Mobile:          false,
			Bitness:         profile.Bitness,
		}
		return ua.Do(ctx)
	}
}
