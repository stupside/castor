(function() {
  const iframes = document.querySelectorAll('iframe');
  let best = null, maxArea = 0;
  for (const f of iframes) {
    const r = f.getBoundingClientRect();
    const a = r.width * r.height;
    if (a > maxArea && r.width > 100 && r.height > 100) { maxArea = a; best = f; }
  }
  if (best && best.src && !best.src.startsWith('about:')) return best.src;
  return null;
})()
