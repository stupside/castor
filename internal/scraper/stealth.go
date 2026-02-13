package scraper

import (
	"context"
	"fmt"
	"strings"

	"github.com/chromedp/cdproto/emulation"
	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/chromedp"

	"github.com/stupside/castor/internal/app"
)

// Stealth JS snippets — injected before any page script runs to mask headless
// Chrome fingerprints. Placeholders ({{…}}) are replaced per-session by
// buildStealthJS.
//
// Overrides already handled at CDP level are NOT duplicated here:
//   - navigator.webdriver      → SetAutomationOverride
//   - document.hasFocus()      → SetFocusEmulationEnabled
//   - navigator.hardwareConcurrency → SetHardwareConcurrencyOverride
//   - navigator.platform       → SetUserAgentOverride
//   - navigator.languages      → SetUserAgentOverride

// #1 — Must be first: defines __cloak used by subsequent snippets.
const stealthToStringJS = `
(function() {
  const registry = new Map();
  const origToString = Function.prototype.toString;
  const replacement = function toString() {
    if (registry.has(this)) return registry.get(this);
    return origToString.call(this);
  };
  registry.set(replacement, 'function toString() { [native code] }');
  Function.prototype.toString = replacement;
  __cloak = function(fn, orig, name) {
    const n = name || (orig && orig.name) || fn.name || '';
    registry.set(fn, 'function ' + n + '() { [native code] }');
  };
})();
var __cloak;`

// #2 — Correct prototype chain for plugins and mimeTypes.
const stealthPluginsJS = `
(function() {
  const pluginData = [
    { name: 'Chrome PDF Plugin', filename: 'internal-pdf-viewer', description: 'Portable Document Format',
      mimes: [{ type: 'application/x-google-chrome-pdf', suffixes: 'pdf', description: 'Portable Document Format' }] },
    { name: 'Chrome PDF Viewer', filename: 'mhjfbmdgcfjbbpaeojofohoefgiehjai', description: '',
      mimes: [{ type: 'application/pdf', suffixes: 'pdf', description: '' }] },
    { name: 'Native Client', filename: 'internal-nacl-plugin', description: '',
      mimes: [{ type: 'application/x-nacl', suffixes: '', description: 'Native Client Executable' },
              { type: 'application/x-pnacl', suffixes: '', description: 'Portable Native Client Executable' }] }
  ];

  const allMimes = [];
  const plugins = pluginData.map(function(pd) {
    const plugin = Object.create(Plugin.prototype);
    const mimes = pd.mimes.map(function(md) {
      const mime = Object.create(MimeType.prototype);
      Object.defineProperties(mime, {
        type:        { get: function() { return md.type; }, enumerable: true },
        suffixes:    { get: function() { return md.suffixes; }, enumerable: true },
        description: { get: function() { return md.description; }, enumerable: true },
        enabledPlugin: { get: function() { return plugin; }, enumerable: true }
      });
      allMimes.push(mime);
      return mime;
    });
    Object.defineProperties(plugin, {
      name:        { get: function() { return pd.name; }, enumerable: true },
      filename:    { get: function() { return pd.filename; }, enumerable: true },
      description: { get: function() { return pd.description; }, enumerable: true },
      length:      { get: function() { return mimes.length; }, enumerable: true }
    });
    mimes.forEach(function(m, i) {
      Object.defineProperty(plugin, i, { get: function() { return m; }, enumerable: true });
    });
    plugin.item = function(idx) { return mimes[idx] || null; };
    plugin.namedItem = function(n) { return mimes.find(function(m) { return m.type === n; }) || null; };
    if (typeof __cloak !== 'undefined') { __cloak(plugin.item, null, 'item'); __cloak(plugin.namedItem, null, 'namedItem'); }
    return plugin;
  });

  const pluginArr = Object.create(PluginArray.prototype);
  Object.defineProperty(pluginArr, 'length', { get: function() { return plugins.length; }, enumerable: true });
  plugins.forEach(function(pl, i) {
    Object.defineProperty(pluginArr, i, { get: function() { return pl; }, enumerable: true });
  });
  pluginArr.item = function(idx) { return plugins[idx] || null; };
  pluginArr.namedItem = function(n) { return plugins.find(function(p) { return p.name === n; }) || null; };
  pluginArr.refresh = function() {};
  pluginArr[Symbol.iterator] = function*() { for (const pl of plugins) yield pl; };
  if (typeof __cloak !== 'undefined') { __cloak(pluginArr.item, null, 'item'); __cloak(pluginArr.namedItem, null, 'namedItem'); __cloak(pluginArr.refresh, null, 'refresh'); }
  Object.defineProperty(navigator, 'plugins', { get: function() { return pluginArr; }, enumerable: true, configurable: true });

  const mimeArr = Object.create(MimeTypeArray.prototype);
  Object.defineProperty(mimeArr, 'length', { get: function() { return allMimes.length; }, enumerable: true });
  allMimes.forEach(function(m, i) {
    Object.defineProperty(mimeArr, i, { get: function() { return m; }, enumerable: true });
  });
  mimeArr.item = function(idx) { return allMimes[idx] || null; };
  mimeArr.namedItem = function(n) { return allMimes.find(function(m) { return m.type === n; }) || null; };
  if (typeof __cloak !== 'undefined') { __cloak(mimeArr.item, null, 'item'); __cloak(mimeArr.namedItem, null, 'namedItem'); }
  Object.defineProperty(navigator, 'mimeTypes', { get: function() { return mimeArr; }, enumerable: true, configurable: true });
})();`

// #3 — unchanged
const stealthChromeJS = `
if (!window.chrome) {
  window.chrome = {
    runtime: {
      onMessage: { addListener: () => {}, removeListener: () => {} },
      sendMessage: () => {},
      connect: () => ({ onMessage: { addListener: () => {} }, postMessage: () => {} })
    },
    loadTimes: () => ({}),
    csi: () => ({})
  };
}`

// #4 — IIFE + __cloak
const stealthPermissionsJS = `
(function() {
  const origQuery = window.Permissions && Permissions.prototype.query;
  if (origQuery) {
    Permissions.prototype.query = function(params) {
      if (params.name === 'notifications') {
        return Promise.resolve({ state: 'prompt', onchange: null });
      }
      return origQuery.call(this, params);
    };
    if (typeof __cloak !== 'undefined') __cloak(Permissions.prototype.query, origQuery, 'query');
  }
})();`

// #5 — IIFE + __cloak for both WebGL1 and WebGL2
const stealthWebGLJS = `
(function() {
  const getParameter = WebGLRenderingContext.prototype.getParameter;
  WebGLRenderingContext.prototype.getParameter = function(param) {
    if (param === 37445) return '{{WEBGL_VENDOR}}';
    if (param === 37446) return '{{WEBGL_RENDERER}}';
    return getParameter.call(this, param);
  };
  if (typeof __cloak !== 'undefined') __cloak(WebGLRenderingContext.prototype.getParameter, getParameter, 'getParameter');

  const getParameter2 = WebGL2RenderingContext.prototype.getParameter;
  WebGL2RenderingContext.prototype.getParameter = function(param) {
    if (param === 37445) return '{{WEBGL_VENDOR}}';
    if (param === 37446) return '{{WEBGL_RENDERER}}';
    return getParameter2.call(this, param);
  };
  if (typeof __cloak !== 'undefined') __cloak(WebGL2RenderingContext.prototype.getParameter, getParameter2, 'getParameter');
})();`

// #6 — unchanged
const stealthDeviceMemoryJS = `
Object.defineProperty(navigator, 'deviceMemory', { get: () => {{DEVICE_MEMORY}} });`

// #7 — unchanged
const stealthNotificationJS = `
Object.defineProperty(Notification, 'permission', { get: () => 'default' });`

// #8 — unchanged
const stealthScreenJS = `
Object.defineProperty(screen, 'colorDepth', { get: () => {{COLOR_DEPTH}} });
Object.defineProperty(screen, 'pixelDepth', { get: () => {{COLOR_DEPTH}} });`

// #9 — unchanged
const stealthWebRTCJS = `
if (window.RTCPeerConnection) {
  const OrigRTC = window.RTCPeerConnection;
  window.RTCPeerConnection = function(config, constraints) {
    if (config && config.iceServers) { config.iceServers = []; }
    return new OrigRTC(config, constraints);
  };
  window.RTCPeerConnection.prototype = OrigRTC.prototype;
  Object.defineProperty(window.RTCPeerConnection, 'name', { value: 'RTCPeerConnection' });
}`

// #10 — seeded xorshift32 PRNG, multi-pixel multi-channel noise, OffscreenCanvas
const stealthCanvasJS = `
(function() {
  var seed = ({{NOISE_SEED}} >>> 0) || 1;
  function xorshift32() {
    seed ^= seed << 13;
    seed ^= seed >> 17;
    seed ^= seed << 5;
    return seed >>> 0;
  }

  function noiseCanvas(canvas, ctx) {
    if (!ctx || canvas.width <= 0 || canvas.height <= 0) return;
    try {
      var w = Math.min(canvas.width, 16);
      var imageData = ctx.getImageData(0, 0, w, 1);
      var d = imageData.data;
      for (var i = 0; i < w * 4; i += 4) {
        d[i]     = Math.max(0, Math.min(255, d[i]     + (xorshift32() % 3) - 1));
        d[i + 1] = Math.max(0, Math.min(255, d[i + 1] + (xorshift32() % 3) - 1));
        d[i + 2] = Math.max(0, Math.min(255, d[i + 2] + (xorshift32() % 3) - 1));
      }
      ctx.putImageData(imageData, 0, 0);
    } catch(e) {}
  }

  var origToDataURL = HTMLCanvasElement.prototype.toDataURL;
  HTMLCanvasElement.prototype.toDataURL = function(type, quality) {
    noiseCanvas(this, this.getContext('2d'));
    return origToDataURL.call(this, type, quality);
  };
  if (typeof __cloak !== 'undefined') __cloak(HTMLCanvasElement.prototype.toDataURL, origToDataURL, 'toDataURL');

  var origToBlob = HTMLCanvasElement.prototype.toBlob;
  HTMLCanvasElement.prototype.toBlob = function(callback, type, quality) {
    noiseCanvas(this, this.getContext('2d'));
    return origToBlob.call(this, callback, type, quality);
  };
  if (typeof __cloak !== 'undefined') __cloak(HTMLCanvasElement.prototype.toBlob, origToBlob, 'toBlob');

  if (typeof OffscreenCanvas !== 'undefined') {
    var origConvertToBlob = OffscreenCanvas.prototype.convertToBlob;
    if (origConvertToBlob) {
      OffscreenCanvas.prototype.convertToBlob = function(options) {
        try {
          var ctx = this.getContext('2d');
          noiseCanvas(this, ctx);
        } catch(e) {}
        return origConvertToBlob.call(this, options);
      };
      if (typeof __cloak !== 'undefined') __cloak(OffscreenCanvas.prototype.convertToBlob, origConvertToBlob, 'convertToBlob');
    }
  }
})();`

// #11 — AudioContext fingerprint noise
const stealthAudioJS = `
(function() {
  var seed = (({{NOISE_SEED}} >>> 0) ^ 0xDEADBEEF) || 1;
  function xorshift32() {
    seed ^= seed << 13;
    seed ^= seed >> 17;
    seed ^= seed << 5;
    return seed >>> 0;
  }
  function noiseFloat() {
    return (xorshift32() / 0xFFFFFFFF - 0.5) * 2 * {{AUDIO_NOISE_MAG}};
  }

  var perturbed = new WeakSet();

  if (typeof AnalyserNode !== 'undefined') {
    var origGetFloat = AnalyserNode.prototype.getFloatFrequencyData;
    AnalyserNode.prototype.getFloatFrequencyData = function(arr) {
      origGetFloat.call(this, arr);
      for (var i = 0; i < arr.length; i++) arr[i] += noiseFloat();
    };
    if (typeof __cloak !== 'undefined') __cloak(AnalyserNode.prototype.getFloatFrequencyData, origGetFloat, 'getFloatFrequencyData');

    var origGetByte = AnalyserNode.prototype.getByteFrequencyData;
    AnalyserNode.prototype.getByteFrequencyData = function(arr) {
      origGetByte.call(this, arr);
      for (var i = 0; i < arr.length; i++) arr[i] = Math.max(0, Math.min(255, arr[i] + ((xorshift32() % 3) - 1)));
    };
    if (typeof __cloak !== 'undefined') __cloak(AnalyserNode.prototype.getByteFrequencyData, origGetByte, 'getByteFrequencyData');

    var origGetFloatTD = AnalyserNode.prototype.getFloatTimeDomainData;
    AnalyserNode.prototype.getFloatTimeDomainData = function(arr) {
      origGetFloatTD.call(this, arr);
      for (var i = 0; i < arr.length; i++) arr[i] += noiseFloat();
    };
    if (typeof __cloak !== 'undefined') __cloak(AnalyserNode.prototype.getFloatTimeDomainData, origGetFloatTD, 'getFloatTimeDomainData');
  }

  if (typeof AudioBuffer !== 'undefined') {
    var origGetChannelData = AudioBuffer.prototype.getChannelData;
    AudioBuffer.prototype.getChannelData = function(channel) {
      var buf = origGetChannelData.call(this, channel);
      if (!perturbed.has(buf)) {
        perturbed.add(buf);
        for (var i = 0; i < buf.length; i++) buf[i] += noiseFloat();
      }
      return buf;
    };
    if (typeof __cloak !== 'undefined') __cloak(AudioBuffer.prototype.getChannelData, origGetChannelData, 'getChannelData');
  }
})();`

// #12 — getBoundingClientRect/getClientRects sub-pixel noise
const stealthClientRectsJS = `
(function() {
  var seed = (({{NOISE_SEED}} >>> 0) ^ 0xCAFEBABE) || 1;
  function xorshift32() {
    seed ^= seed << 13;
    seed ^= seed >> 17;
    seed ^= seed << 5;
    return seed >>> 0;
  }
  var noisePx = {{RECT_NOISE_PX}};
  function noise() {
    return (xorshift32() / 0xFFFFFFFF - 0.5) * 2 * noisePx;
  }

  var origGetBCR = Element.prototype.getBoundingClientRect;
  Element.prototype.getBoundingClientRect = function() {
    var r = origGetBCR.call(this);
    var nx = noise(), ny = noise();
    return new DOMRect(r.x + nx, r.y + ny, r.width + noise(), r.height + noise());
  };
  if (typeof __cloak !== 'undefined') __cloak(Element.prototype.getBoundingClientRect, origGetBCR, 'getBoundingClientRect');

  var origGetCR = Element.prototype.getClientRects;
  Element.prototype.getClientRects = function() {
    var rects = origGetCR.call(this);
    var result = [];
    for (var i = 0; i < rects.length; i++) {
      var r = rects[i];
      result.push(new DOMRect(r.x + noise(), r.y + noise(), r.width + noise(), r.height + noise()));
    }
    Object.defineProperty(result, 'item', { value: function(idx) { return result[idx] || null; } });
    return result;
  };
  if (typeof __cloak !== 'undefined') __cloak(Element.prototype.getClientRects, origGetCR, 'getClientRects');
})();`

// #13 — measureText noise, returns TextMetrics prototype object
const stealthFontMetricJS = `
(function() {
  var seed = (({{NOISE_SEED}} >>> 0) ^ 0x8BADF00D) || 1;
  function xorshift32() {
    seed ^= seed << 13;
    seed ^= seed >> 17;
    seed ^= seed << 5;
    return seed >>> 0;
  }
  var noisePx = {{FONT_NOISE_PX}};
  function noise() {
    return (xorshift32() / 0xFFFFFFFF - 0.5) * 2 * noisePx;
  }

  var origMeasureText = CanvasRenderingContext2D.prototype.measureText;
  CanvasRenderingContext2D.prototype.measureText = function(text) {
    var m = origMeasureText.call(this, text);
    var result = Object.create(TextMetrics.prototype);
    var props = ['width', 'actualBoundingBoxLeft', 'actualBoundingBoxRight',
                 'actualBoundingBoxAscent', 'actualBoundingBoxDescent',
                 'fontBoundingBoxAscent', 'fontBoundingBoxDescent',
                 'alphabeticBaseline', 'hangingBaseline', 'ideographicBaseline',
                 'emHeightAscent', 'emHeightDescent'];
    for (var i = 0; i < props.length; i++) {
      var p = props[i];
      var val = m[p];
      if (typeof val === 'number') {
        Object.defineProperty(result, p, { value: val + noise(), enumerable: true, configurable: true });
      }
    }
    return result;
  };
  if (typeof __cloak !== 'undefined') __cloak(CanvasRenderingContext2D.prototype.measureText, origMeasureText, 'measureText');
})();`

// #14 — Should be last: filters injected script frames from stack traces.
const stealthStackTraceJS = `
(function() {
  if (typeof Error.prepareStackTrace === 'undefined' && !Error.prepareStackTrace) {
    // V8 structured stack trace API
    Error.prepareStackTrace = function(err, stack) {
      var filtered = stack.filter(function(frame) {
        var fn = frame.getFileName();
        if (!fn || fn === '') return false;
        if (fn.startsWith('http://') || fn.startsWith('https://') || fn.startsWith('file://') || fn === 'native') return true;
        return false;
      });
      var lines = ['Error: ' + (err.message || '')];
      for (var i = 0; i < filtered.length; i++) {
        lines.push('    at ' + filtered[i].toString());
      }
      return lines.join('\n');
    };
  }

  // Fallback: string-based stack getter
  var origStackDesc = Object.getOwnPropertyDescriptor(Error.prototype, 'stack');
  if (origStackDesc && origStackDesc.get) {
    var origGet = origStackDesc.get;
    Object.defineProperty(Error.prototype, 'stack', {
      get: function() {
        var s = origGet.call(this);
        if (typeof s !== 'string') return s;
        var lines = s.split('\n');
        var filtered = lines.filter(function(line) {
          var trimmed = line.trim();
          if (!trimmed.startsWith('at ')) return true;
          if (trimmed.indexOf('http://') !== -1 || trimmed.indexOf('https://') !== -1 ||
              trimmed.indexOf('file://') !== -1 || trimmed.indexOf('(native)') !== -1 ||
              trimmed.indexOf('<anonymous>') !== -1) return true;
          return false;
        });
        return filtered.join('\n');
      },
      set: origStackDesc.set,
      configurable: true
    });
  }
})();`

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
		"{{DEVICE_MEMORY}}", fmt.Sprintf("%d", profile.DeviceMemory),
		"{{COLOR_DEPTH}}", fmt.Sprintf("%d", profile.ColorDepth),
		"{{WEBGL_VENDOR}}", profile.WebGLVendor,
		"{{WEBGL_RENDERER}}", profile.WebGLRenderer,
		"{{NOISE_SEED}}", fmt.Sprintf("%d", profile.NoiseSeed),
		"{{FONT_NOISE_PX}}", fmt.Sprintf("%.6f", profile.FontNoisePx),
		"{{RECT_NOISE_PX}}", fmt.Sprintf("%.6f", profile.RectNoisePx),
		"{{AUDIO_NOISE_MAG}}", fmt.Sprintf("%.10f", profile.AudioNoiseMag),
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

		// Use new headless mode (less detectable than legacy)
		chromedp.Flag("headless", headlessVal),
		chromedp.Flag("no-sandbox", cfg.NoSandbox),

		// Anti-detection flags
		chromedp.Flag("disable-blink-features", "AutomationControlled"),
		chromedp.Flag("disable-infobars", true),
		chromedp.Flag("enable-features", "NetworkService,NetworkServiceInProcess"),
		chromedp.Flag("disable-background-timer-throttling", true),
		chromedp.Flag("disable-backgrounding-occluded-windows", true),
		chromedp.Flag("disable-renderer-backgrounding", true),

		// WebRTC leak prevention at browser level
		chromedp.Flag("webrtc-ip-handling-policy", "disable_non_proxied_udp"),

		// Media autoplay
		chromedp.Flag("autoplay-policy", "no-user-gesture-required"),

		// Window size from profile
		chromedp.WindowSize(profile.ScreenWidth, profile.ScreenHeight),

		// User-Agent from profile
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
		// Disable the internal automation flag (navigator.webdriver at CDP level).
		if err := emulation.SetAutomationOverride(false).Do(ctx); err != nil {
			return err
		}

		// Simulate a focused window so document.hasFocus() returns true.
		if err := emulation.SetFocusEmulationEnabled(true).Do(ctx); err != nil {
			return err
		}

		// CDP-level hardware concurrency override.
		if err := emulation.SetHardwareConcurrencyOverride(profile.HardwareConcurrency).Do(ctx); err != nil {
			return err
		}

		// Timezone override.
		if err := emulation.SetTimezoneOverride(profile.TimezoneID).Do(ctx); err != nil {
			return err
		}

		// Locale override — extract short locale from AcceptLanguage (e.g. "en-US").
		locale := profile.Languages[0]
		if err := emulation.SetLocaleOverride().WithLocale(locale).Do(ctx); err != nil {
			return err
		}

		// Full User-Agent override with Client Hints metadata so
		// navigator.userAgentData doesn't leak "HeadlessChrome".
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
