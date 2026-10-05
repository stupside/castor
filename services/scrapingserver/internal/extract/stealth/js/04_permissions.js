(function() {
  const origQuery = window.Permissions && Permissions.prototype.query;
  if (origQuery) {
    Permissions.prototype.query = function(params) {
      const result = origQuery.call(this, params);
      if (params && params.name === 'notifications') {
        return result.then(function(status) {
          Object.defineProperty(status, 'state', { get: function() {
            const permission = typeof Notification === 'undefined' ? 'default' : Notification.permission;
            return permission === 'default' ? 'prompt' : permission;
          }});
          return status;
        });
      }
      return result;
    };
    if (typeof __cloak !== 'undefined') __cloak(Permissions.prototype.query, origQuery, 'query');
  }
})();
