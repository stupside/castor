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
})();