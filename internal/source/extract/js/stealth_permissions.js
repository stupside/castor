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
})();