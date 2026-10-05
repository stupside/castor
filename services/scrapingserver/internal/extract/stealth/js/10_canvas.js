(function() {
  var seed = (__NOISE_SEED__ >>> 0) || 1;
  function xorshift32() {
    seed ^= seed << 13;
    seed ^= seed >> 17;
    seed ^= seed << 5;
    return seed >>> 0;
  }

  function noiseCanvas(canvas, ctx) {
    if (!ctx || canvas.width <= 0 || canvas.height <= 0) return canvas;
    try {
      // Export a copy: changing the page's pixels corrupts later rendering
      // and compounds the noise each time a player exports its canvas.
      var copy = typeof OffscreenCanvas !== 'undefined' && canvas instanceof OffscreenCanvas
        ? new OffscreenCanvas(canvas.width, canvas.height)
        : document.createElement('canvas');
      copy.width = canvas.width; copy.height = canvas.height;
      ctx = copy.getContext('2d');
      ctx.drawImage(canvas, 0, 0);
      var w = Math.min(canvas.width, 16);
      var imageData = ctx.getImageData(0, 0, w, 1);
      var d = imageData.data;
      for (var i = 0; i < w * 4; i += 4) {
        d[i]     = Math.max(0, Math.min(255, d[i]     + (xorshift32() % 3) - 1));
        d[i + 1] = Math.max(0, Math.min(255, d[i + 1] + (xorshift32() % 3) - 1));
        d[i + 2] = Math.max(0, Math.min(255, d[i + 2] + (xorshift32() % 3) - 1));
      }
      ctx.putImageData(imageData, 0, 0);
      return copy;
    } catch(e) {}
    return canvas;
  }

  var origToDataURL = HTMLCanvasElement.prototype.toDataURL;
  HTMLCanvasElement.prototype.toDataURL = function(type, quality) {
    return origToDataURL.call(noiseCanvas(this, this.getContext('2d')), type, quality);
  };
  if (typeof __cloak !== 'undefined') __cloak(HTMLCanvasElement.prototype.toDataURL, origToDataURL, 'toDataURL');

  var origToBlob = HTMLCanvasElement.prototype.toBlob;
  HTMLCanvasElement.prototype.toBlob = function(callback, type, quality) {
    return origToBlob.call(noiseCanvas(this, this.getContext('2d')), callback, type, quality);
  };
  if (typeof __cloak !== 'undefined') __cloak(HTMLCanvasElement.prototype.toBlob, origToBlob, 'toBlob');

  if (typeof OffscreenCanvas !== 'undefined') {
    var origConvertToBlob = OffscreenCanvas.prototype.convertToBlob;
    if (origConvertToBlob) {
      OffscreenCanvas.prototype.convertToBlob = function(options) {
        var canvas = this;
        try {
          var ctx = this.getContext('2d');
          canvas = noiseCanvas(this, ctx);
        } catch(e) {}
        return origConvertToBlob.call(canvas, options);
      };
      if (typeof __cloak !== 'undefined') __cloak(OffscreenCanvas.prototype.convertToBlob, origConvertToBlob, 'convertToBlob');
    }
  }
})();
