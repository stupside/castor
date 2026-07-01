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
var __cloak;