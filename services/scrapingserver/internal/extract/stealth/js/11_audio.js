(function() {
  var seed = ((__NOISE_SEED__ >>> 0) ^ 0xDEADBEEF) || 1;
  function xorshift32() {
    seed ^= seed << 13;
    seed ^= seed >> 17;
    seed ^= seed << 5;
    return seed >>> 0;
  }
  function noiseFloat() {
    return (xorshift32() / 0xFFFFFFFF - 0.5) * 2 * __AUDIO_NOISE_MAG__;
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
})();