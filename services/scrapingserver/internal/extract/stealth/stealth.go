// Package stealth disguises an automated browser as one profile of an ordinary desktop Chrome.
package stealth

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"strconv"
	"strings"

	"github.com/chromedp/cdproto/browser"
	"github.com/chromedp/cdproto/emulation"
	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/chromedp"
)

// scripts patch the page's view of the browser, in the order their names sort: toString first, so every later patch reads as native.
//
//go:embed js/*.js
var scripts embed.FS

// Flags are the browser flags that keep automation from showing, and the profile's window.
func (p *Profile) Flags() []chromedp.ExecAllocatorOption {
	return []chromedp.ExecAllocatorOption{
		chromedp.Flag("disable-blink-features", "AutomationControlled"),
		chromedp.Flag("disable-infobars", true),
		chromedp.Flag("webrtc-ip-handling-policy", "disable_non_proxied_udp"),
		chromedp.WindowSize(p.screenWidth, p.screenHeight),
	}
}

// Apply pins the profile to the running browser's version and disguises every page it opens before any page script runs.
func (p *Profile) Apply() chromedp.Action {
	return chromedp.Tasks{p.identifyBrowser(), p.inject(), p.override()}
}

// identifyBrowser pins the profile's user agent to the version of the browser actually running.
func (p *Profile) identifyBrowser() chromedp.ActionFunc {
	return func(ctx context.Context) error {
		_, product, _, _, _, err := browser.GetVersion().Do(ctx)
		if err != nil {
			return fmt.Errorf("asking the browser its version: %w", err)
		}
		return p.identify(product)
	}
}

func (p *Profile) inject() chromedp.ActionFunc {
	return func(ctx context.Context) error {
		js, err := p.script()
		if err != nil {
			return err
		}
		_, err = page.AddScriptToEvaluateOnNewDocument(js).Do(ctx)
		return err
	}
}

func (p *Profile) script() (string, error) {
	names, err := fs.Glob(scripts, "js/*.js")
	if err != nil {
		return "", err
	}
	snippets := make([]string, len(names))
	for i, name := range names {
		b, err := scripts.ReadFile(name)
		if err != nil {
			return "", err
		}
		snippets[i] = string(b)
	}
	// One closure, so the helpers the snippets share never reach the page's globals.
	joined := "(() => {\n" + strings.Join(snippets, "\n") + "\n})();"
	return strings.NewReplacer(
		"__DEVICE_MEMORY__", strconv.Itoa(p.deviceMemory),
		"__WEBGL_VENDOR__", p.webGLVendor,
		"__WEBGL_RENDERER__", p.webGLRenderer,
		"__NOISE_SEED__", strconv.FormatUint(uint64(p.noiseSeed), 10),
		"__FONT_NOISE_PX__", fmt.Sprintf("%.6f", p.fontNoisePx),
		"__RECT_NOISE_PX__", fmt.Sprintf("%.6f", p.rectNoisePx),
		"__AUDIO_NOISE_MAG__", fmt.Sprintf("%.10f", p.audioNoiseMag),
	).Replace(joined), nil
}

// override sets what a page reads of the browser that no script can mask.
func (p *Profile) override() chromedp.ActionFunc {
	return func(ctx context.Context) error {
		if err := emulation.SetAutomationOverride(false).Do(ctx); err != nil {
			return err
		}
		if err := emulation.SetFocusEmulationEnabled(true).Do(ctx); err != nil {
			return err
		}
		if err := emulation.SetHardwareConcurrencyOverride(p.hardwareConcurrency).Do(ctx); err != nil {
			return err
		}
		if err := emulation.SetTimezoneOverride(p.timezoneID).Do(ctx); err != nil {
			return err
		}
		if err := emulation.SetLocaleOverride().WithLocale(p.languages[0]).Do(ctx); err != nil {
			return err
		}
		ua := emulation.SetUserAgentOverride(p.userAgent)
		ua.AcceptLanguage = p.acceptLanguage
		ua.Platform = p.navigatorPlatform
		ua.UserAgentMetadata = &emulation.UserAgentMetadata{
			Brands:          brandVersions(p.brands),
			FullVersionList: brandVersions(p.fullVersionList),
			Platform:        p.platform,
			PlatformVersion: p.platformVersion,
			Architecture:    p.architecture,
			Bitness:         p.bitness,
		}
		return ua.Do(ctx)
	}
}

func brandVersions(brands [][2]string) []*emulation.UserAgentBrandVersion {
	out := make([]*emulation.UserAgentBrandVersion, len(brands))
	for i, b := range brands {
		out[i] = &emulation.UserAgentBrandVersion{Brand: b[0], Version: b[1]}
	}
	return out
}
