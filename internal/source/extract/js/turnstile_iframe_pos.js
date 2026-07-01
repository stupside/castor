(function() {
    const c = document.querySelector('.cf-turnstile');
    if (!c) return null;
    const f = c.querySelector('iframe');
    if (!f) return null;
    const r = f.getBoundingClientRect();
    if (r.width < 10 || r.height < 10) return null;
    return {x: Math.round(r.x + r.width/2), y: Math.round(r.y + r.height/2)};
})()
