(function() {
  if (typeof Error.prepareStackTrace === 'undefined' && !Error.prepareStackTrace) {
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
})();