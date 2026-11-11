(async () => {
  const vw = document.documentElement.clientWidth;
  const out = { url: location.href, title: document.title, vw, vh: innerHeight, scrollH: document.documentElement.scrollHeight };
  const vis = (el) => { const s = getComputedStyle(el); const r = el.getBoundingClientRect(); return s.display !== 'none' && s.visibility !== 'hidden' && +s.opacity > 0 && r.width > 0 && r.height > 0; };
  const sel = (el) => { let s = el.tagName.toLowerCase(); if (el.id) s += '#' + el.id; else if (el.classList.length) s += '.' + [...el.classList].slice(0, 2).join('.'); return s; };
  const overflow = [];
  for (const el of document.querySelectorAll('body *')) {
    const r = el.getBoundingClientRect();
    if (r.width && (r.right > vw + 1 || r.left < -1) && vis(el)) {
      let p = el.parentElement, clipped = false;
      while (p && p !== document.body) { const o = getComputedStyle(p).overflowX; if (o === 'hidden' || o === 'clip' || o === 'auto' || o === 'scroll') { clipped = true; break; } p = p.parentElement; }
      if (!clipped) overflow.push({ el: sel(el), left: Math.round(r.left), right: Math.round(r.right), y: Math.round(r.top + scrollY) });
    }
  }
  out.pageScrollsHorizontally = document.documentElement.scrollWidth > vw + 1;
  out.overflow = overflow.slice(0, 15);
  const lum = (c) => { const m = c.match(/[\d.]+/g); if (!m) return null; const [r, g, b] = m.slice(0, 3).map((v) => { v = v / 255; return v <= 0.03928 ? v / 12.92 : ((v + 0.055) / 1.055) ** 2.4; }); return { L: 0.2126 * r + 0.7152 * g + 0.0722 * b, a: m[3] === undefined ? 1 : +m[3] }; };
  const bgOf = (el) => { while (el) { const b = getComputedStyle(el).backgroundColor; const l = lum(b); if (l && l.a > 0.5) return l; el = el.parentElement; } return lum('rgb(255,255,255)'); };
  const contrast = [], small = [];
  const seen = new Set();
  for (const el of document.querySelectorAll('p,span,a,li,button,label,h1,h2,h3,h4,h5,h6,small,td,th,div')) {
    if (!vis(el)) continue;
    const t = [...el.childNodes].filter((n) => n.nodeType === 3).map((n) => n.textContent.trim()).join(' ').trim();
    if (t.length < 2) continue;
    const s = getComputedStyle(el);
    const fg = lum(s.color), bg = bgOf(el);
    if (fg && bg) {
      const r = (Math.max(fg.L, bg.L) + 0.05) / (Math.min(fg.L, bg.L) + 0.05);
      const fs = parseFloat(s.fontSize), bold = +s.fontWeight >= 700;
      const need = fs >= 24 || (fs >= 18.66 && bold) ? 3 : 4.5;
      const key = s.color + '|' + fs;
      if (r < need && !seen.has(key)) { seen.add(key); contrast.push({ text: t.slice(0, 40), ratio: +r.toFixed(2), need, color: s.color, size: fs, y: Math.round(el.getBoundingClientRect().top + scrollY) }); }
    }
    if (parseFloat(s.fontSize) < 12) small.push({ text: t.slice(0, 30), size: parseFloat(s.fontSize) });
  }
  out.lowContrast = contrast.slice(0, 20);
  out.lowContrastCount = contrast.length;
  out.tinyText = small.slice(0, 10);
  out.tinyTextCount = small.length;
  out.imgsNoAlt = [...document.images].filter((i) => !i.hasAttribute('alt')).map((i) => i.currentSrc.slice(0, 80)).slice(0, 10);
  out.imgs = [...document.images].map((i) => ({ src: i.currentSrc.split('/').pop().slice(0, 50), natural: i.naturalWidth + 'x' + i.naturalHeight, shown: Math.round(i.getBoundingClientRect().width) + 'x' + Math.round(i.getBoundingClientRect().height), lazy: i.loading })).filter((x) => x.natural !== '0x0').slice(0, 20);
  out.headings = [...document.querySelectorAll('h1,h2,h3')].filter(vis).map((h) => h.tagName + ': ' + h.textContent.trim().replace(/\s+/g, ' ').slice(0, 60));
  out.h1Count = document.querySelectorAll('h1').length;
  const tap = [];
  for (const el of document.querySelectorAll('a,button,input,select,[role=button]')) {
    if (!vis(el)) continue;
    const r = el.getBoundingClientRect();
    if (r.width < 24 || r.height < 24) tap.push({ el: sel(el), name: (el.getAttribute('aria-label') || el.textContent || '').trim().slice(0, 25), size: Math.round(r.width) + 'x' + Math.round(r.height) });
  }
  out.smallTargets = tap.slice(0, 15);
  out.smallTargetsCount = tap.length;
  out.unnamedControls = [...document.querySelectorAll('a,button')].filter((el) => vis(el) && !(el.getAttribute('aria-label') || el.textContent.trim() || el.querySelector('img[alt]') || el.title)).map(sel).slice(0, 10);
  out.inputsNoLabel = [...document.querySelectorAll('input,textarea,select')].filter((el) => vis(el) && el.type !== 'hidden' && !(el.labels && el.labels.length) && !el.getAttribute('aria-label') && !el.getAttribute('aria-labelledby')).map(sel);
  const m = (n) => document.querySelector(`meta[name="${n}"],meta[property="${n}"]`)?.content || null;
  out.meta = { description: m('description'), ogTitle: m('og:title'), ogImage: m('og:image'), twitter: m('twitter:card'), viewport: m('viewport'), canonical: document.querySelector('link[rel=canonical]')?.href || null, lang: document.documentElement.lang, themeColor: m('theme-color'), alternates: [...document.querySelectorAll('link[rel=alternate][hreflang]')].map((l) => l.hreflang) };
  const nav = performance.getEntriesByType('navigation')[0];
  const res = performance.getEntriesByType('resource');
  const sum = (f) => Math.round(res.filter(f).reduce((a, r) => a + (r.transferSize || 0), 0) / 1024);
  out.perf = {
    ttfb: nav && Math.round(nav.responseStart), dcl: nav && Math.round(nav.domContentLoadedEventEnd), load: nav && Math.round(nav.loadEventEnd),
    requests: res.length, transferKB: Math.round(((nav?.transferSize || 0) + res.reduce((a, r) => a + (r.transferSize || 0), 0)) / 1024),
    jsKB: sum((r) => r.initiatorType === 'script' || /\.m?js(\?|$)/.test(r.name)), cssKB: sum((r) => /\.css(\?|$)/.test(r.name)), fontKB: sum((r) => /\.(woff2?|ttf|otf)(\?|$)/.test(r.name)), imgKB: sum((r) => r.initiatorType === 'img' || /\.(png|jpe?g|webp|avif|gif|svg)(\?|$)/.test(r.name)),
    thirdParty: [...new Set(res.map((r) => new URL(r.name).host).filter((h) => !h.endsWith(location.hostname.replace(/^www\./, ''))))],
    fonts: [...new Set([...document.fonts].filter((f) => f.status === 'loaded').map((f) => f.family.replace(/"/g, '') + ' ' + f.weight + (f.style !== 'normal' ? ' ' + f.style : '')))],
  };
  out.perf.lcp = await new Promise((r) => { let v = null; try { new PerformanceObserver((l) => { const e = l.getEntries(); v = e[e.length - 1]; }).observe({ type: 'largest-contentful-paint', buffered: true }); } catch {} setTimeout(() => r(v ? { ms: Math.round(v.startTime), el: v.element ? sel(v.element) : null, size: v.size } : null), 300); });
  out.perf.cls = await new Promise((r) => { let c = 0; try { new PerformanceObserver((l) => { for (const e of l.getEntries()) if (!e.hadRecentInput) c += e.value; }).observe({ type: 'layout-shift', buffered: true }); } catch {} setTimeout(() => r(+c.toFixed(3)), 300); });
  out.longTasks = performance.getEntriesByType('longtask')?.length;
  out.animations = document.getAnimations().filter((a) => a.playState === 'running').length;
  out.reducedMotionRules = [...document.styleSheets].some((s) => { try { return [...s.cssRules].some((r) => r.conditionText && r.conditionText.includes('prefers-reduced-motion')); } catch { return false; } });
  const links = [...new Set([...document.querySelectorAll('a[href]')].map((a) => a.href).filter((h) => h.startsWith(location.origin) && !h.includes('#')))];
  out.brokenLinks = (await Promise.all(links.filter((h) => !/log-?out|sign-?out|logoff|delete|remove|unsubscribe|destroy|action=/i.test(h)).slice(0, 40).map(async (h) => { try { const r = await fetch(h, { method: 'HEAD', redirect: 'follow', credentials: 'omit' }); return r.ok ? null : h + ' ' + r.status; } catch (e) { return h + ' ERR'; } }))).filter(Boolean);
  out.internalLinks = links;
  out.anchors = [...document.querySelectorAll('a[href^="#"]')].map((a) => a.getAttribute('href')).filter((h) => h.length > 1 && !document.getElementById(h.slice(1)));
  return out;
})()
