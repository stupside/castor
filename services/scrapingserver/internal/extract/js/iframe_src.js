(function() {
  const iframes = document.querySelectorAll('iframe');
  let best = null, maxArea = 0;
  for (const f of iframes) {
    if (!f.src || !/^https?:\/\//i.test(f.src)) continue;
    const style = getComputedStyle(f);
    if (style.display === 'none' || style.visibility !== 'visible' || Number(style.opacity) === 0) continue;
    const r = f.getBoundingClientRect();
    if (r.bottom <= 0 || r.right <= 0 || r.top >= innerHeight || r.left >= innerWidth) continue;
    const a = r.width * r.height;
    if (a > maxArea && r.width > 100 && r.height > 100) { maxArea = a; best = f; }
  }
  if (best) return best.src;
  return null;
})()
