(function() {
  var seed = ((__NOISE_SEED__ >>> 0) ^ 0xCAFEBABE) || 1;
  function xorshift32() {
    seed ^= seed << 13;
    seed ^= seed >> 17;
    seed ^= seed << 5;
    return seed >>> 0;
  }
  var noisePx = __RECT_NOISE_PX__;
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
})();