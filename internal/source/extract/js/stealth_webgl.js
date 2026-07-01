(function() {
  const getParameter = WebGLRenderingContext.prototype.getParameter;
  WebGLRenderingContext.prototype.getParameter = function(param) {
    if (param === 37445) return '__WEBGL_VENDOR__';
    if (param === 37446) return '__WEBGL_RENDERER__';
    return getParameter.call(this, param);
  };
  if (typeof __cloak !== 'undefined') __cloak(WebGLRenderingContext.prototype.getParameter, getParameter, 'getParameter');

  const getParameter2 = WebGL2RenderingContext.prototype.getParameter;
  WebGL2RenderingContext.prototype.getParameter = function(param) {
    if (param === 37445) return '__WEBGL_VENDOR__';
    if (param === 37446) return '__WEBGL_RENDERER__';
    return getParameter2.call(this, param);
  };
  if (typeof __cloak !== 'undefined') __cloak(WebGL2RenderingContext.prototype.getParameter, getParameter2, 'getParameter');
})();