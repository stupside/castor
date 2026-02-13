(function() {
  var seed = ((__NOISE_SEED__ >>> 0) ^ 0x8BADF00D) || 1;
  function xorshift32() {
    seed ^= seed << 13;
    seed ^= seed >> 17;
    seed ^= seed << 5;
    return seed >>> 0;
  }
  var noisePx = __FONT_NOISE_PX__;
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
})();