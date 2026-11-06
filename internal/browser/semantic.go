package browser

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

const snapshotJS = `(() => {
const R = window.__carbon || (window.__carbon = {n: 0, m: new Map(), w: new WeakMap()});
const out = [];
const vw = innerWidth, vh = innerHeight;
const interactiveRoles = new Set(['button','link','textbox','checkbox','radio','combobox','listbox','option','menuitem','menuitemcheckbox','menuitemradio','tab','switch','slider','searchbox','spinbutton','treeitem','gridcell']);
const sel = 'a[href],button,input,select,textarea,summary,[role],[contenteditable=""],[contenteditable="true"],[tabindex]:not([tabindex="-1"]),iframe,[onclick]';
function name(el) {
  const l = el.getAttribute('aria-label'); if (l) return l;
  const lb = el.getAttribute('aria-labelledby');
  if (lb) { const t = lb.split(/\s+/).map(i => document.getElementById(i)).filter(Boolean).map(e => e.innerText).join(' '); if (t) return t; }
  if (el.labels && el.labels.length) return Array.from(el.labels).map(l => l.innerText).join(' ');
  if (el.tagName === 'IMG') return el.alt || '';
  if (el.tagName === 'IFRAME') return el.title || el.src || '';
  const t = (el.innerText || el.value || el.placeholder || el.title || el.alt || '').trim();
  return t;
}
function role(el) {
  const r = el.getAttribute('role'); if (r) return r;
  const t = el.tagName.toLowerCase();
  if (t === 'a') return 'link';
  if (t === 'button' || t === 'summary') return 'button';
  if (t === 'select') return 'combobox';
  if (t === 'textarea') return 'textbox';
  if (t === 'iframe') return 'iframe';
  if (t === 'input') { const ty = (el.type || 'text').toLowerCase(); if (['button','submit','reset','image'].includes(ty)) return 'button'; if (ty === 'checkbox' || ty === 'radio') return ty; if (ty === 'range') return 'slider'; if (ty === 'file') return 'file'; return 'textbox'; }
  if (el.isContentEditable) return 'textbox';
  return t;
}
function visible(el, r) {
  if (r.width < 1 || r.height < 1) return false;
  const s = getComputedStyle(el);
  if (s.visibility === 'hidden' || s.display === 'none' || +s.opacity === 0) return false;
  return true;
}
function walk(root, ox, oy, depth) {
  const els = root.querySelectorAll(sel);
  for (const el of els) {
    const r = el.getBoundingClientRect();
    if (!visible(el, r)) continue;
    const rl = role(el);
    if (el.hasAttribute('role') && !interactiveRoles.has(rl) && !el.matches('a[href],button,input,select,textarea,[tabindex],[onclick]')) continue;
    let id = R.w.get(el);
    if (!id) { id = 'e' + (++R.n); R.w.set(el, id); R.m.set(id, new WeakRef(el)); }
    const x = Math.round(r.x + ox), y = Math.round(r.y + oy);
    const inView = x < vw && y < vh && x + r.width > 0 && y + r.height > 0;
    const it = {ref: id, role: rl, name: name(el).replace(/\s+/g, ' ').slice(0, 80), box: [x, y, Math.round(r.width), Math.round(r.height)]};
    if (!inView) it.offscreen = true;
    if (el.disabled) it.disabled = true;
    if (el.checked) it.checked = true;
    if (rl === 'textbox' || rl === 'combobox' || rl === 'searchbox') { it.value = (el.value || el.innerText || '').slice(0, 80); if (el.type === 'password') it.value = it.value ? '••••' : ''; }
    if (el.tagName === 'IFRAME') { try { const d = el.contentDocument; if (d && depth < 3) walk(d, x, y, depth + 1); } catch (e) {} }
    out.push(it);
  }
  for (const h of root.querySelectorAll('*')) { if (h.shadowRoot && depth < 4) walk(h.shadowRoot, ox, oy, depth + 1); }
}
walk(document, 0, 0, 0);
const text = (document.body ? document.body.innerText : '').replace(/\n{3,}/g, '\n\n').slice(0, %d);
return {url: location.href, title: document.title, viewport: [vw, vh], scroll: [scrollX, scrollY, document.documentElement.scrollHeight], elements: out.slice(0, %d), text};
})()`

type Element struct {
	Ref       string `json:"ref"`
	Role      string `json:"role"`
	Name      string `json:"name"`
	Box       [4]int `json:"box"`
	Value     string `json:"value,omitempty"`
	Offscreen bool   `json:"offscreen,omitempty"`
	Disabled  bool   `json:"disabled,omitempty"`
	Checked   bool   `json:"checked,omitempty"`
}

type Snapshot struct {
	URL      string    `json:"url"`
	Title    string    `json:"title"`
	Viewport [2]int    `json:"viewport"`
	Scroll   [3]int    `json:"scroll"`
	Elements []Element `json:"elements"`
	Text     string    `json:"text,omitempty"`
}

func (s *Snapshot) Compact() string {
	var b strings.Builder
	fmt.Fprintf(&b, "url: %s\ntitle: %s\nviewport: %dx%d scroll: %d/%d\n", s.URL, s.Title, s.Viewport[0], s.Viewport[1], s.Scroll[1], s.Scroll[2])
	for _, e := range s.Elements {
		fmt.Fprintf(&b, "[%s] %s %q", e.Ref, e.Role, e.Name)
		if e.Value != "" {
			fmt.Fprintf(&b, " value=%q", e.Value)
		}
		if e.Checked {
			b.WriteString(" checked")
		}
		if e.Disabled {
			b.WriteString(" disabled")
		}
		if e.Offscreen {
			b.WriteString(" offscreen")
		}
		fmt.Fprintf(&b, " @%d,%d %dx%d\n", e.Box[0], e.Box[1], e.Box[2], e.Box[3])
	}
	if s.Text != "" {
		b.WriteString("--- text ---\n")
		b.WriteString(s.Text)
	}
	return b.String()
}

func (t *Tab) Snapshot(ctx context.Context, maxText, maxElements int) (*Snapshot, error) {
	if maxElements == 0 {
		maxElements = 400
	}
	var s Snapshot
	if err := t.Eval(ctx, fmt.Sprintf(snapshotJS, maxText, maxElements), &s); err != nil {
		return nil, err
	}
	return &s, nil
}

type box struct {
	X, Y, W, H float64
	Found      bool
}

func (t *Tab) refBox(ctx context.Context, ref string) (float64, float64, error) {
	js := fmt.Sprintf(`(async () => { const R = window.__carbon; const w = R && R.m.get(%q); const el = w && w.deref(); if (!el || !el.isConnected) return {Found:false};
el.scrollIntoView({block:'center', inline:'center', behavior:'instant'}); await new Promise(r => requestAnimationFrame(() => r()));
let r = el.getBoundingClientRect(); let ox = 0, oy = 0; let win = el.ownerDocument.defaultView;
while (win && win !== window && win.frameElement) { const fr = win.frameElement.getBoundingClientRect(); ox += fr.x; oy += fr.y; win = win.parent; }
return {Found:true, X:r.x+ox, Y:r.y+oy, W:r.width, H:r.height}; })()`, ref)
	var b box
	if err := t.Eval(ctx, js, &b); err != nil {
		return 0, 0, err
	}
	if !b.Found {
		return 0, 0, fmt.Errorf("element %s not found (take a new snapshot)", ref)
	}
	return b.X + b.W/2, b.Y + b.H/2, nil
}

func (t *Tab) ClickRef(ctx context.Context, ref string, button string, clicks int) error {
	x, y, err := t.refBox(ctx, ref)
	if err != nil {
		return err
	}
	return t.Click(ctx, x, y, button, clicks)
}

func (t *Tab) HoverRef(ctx context.Context, ref string) error {
	x, y, err := t.refBox(ctx, ref)
	if err != nil {
		return err
	}
	return t.Hover(ctx, x, y)
}

func (t *Tab) FillRef(ctx context.Context, ref, text string, submit bool) error {
	js := fmt.Sprintf(`(() => { const w = window.__carbon && window.__carbon.m.get(%q); const el = w && w.deref(); if (!el) return false; el.focus();
if ('value' in el && el.tagName !== 'SELECT') { el.select && el.select(); } else if (el.isContentEditable) { const s = getSelection(); const r = document.createRange(); r.selectNodeContents(el); s.removeAllRanges(); s.addRange(r); } return true; })()`, ref)
	var ok bool
	if err := t.Eval(ctx, js, &ok); err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("element %s not found", ref)
	}
	if text == "" {
		if err := t.Press(ctx, "Delete"); err != nil {
			return err
		}
	} else if err := t.InsertText(ctx, text); err != nil {
		return err
	}
	if submit {
		return t.Press(ctx, "Enter")
	}
	return nil
}

func (t *Tab) SelectRef(ctx context.Context, ref string, values []string) error {
	b, _ := json.Marshal(values)
	js := fmt.Sprintf(`(() => { const w = window.__carbon && window.__carbon.m.get(%q); const el = w && w.deref(); if (!el || el.tagName !== 'SELECT') return false; const vals = %s;
for (const o of el.options) o.selected = vals.includes(o.value) || vals.includes(o.label) || vals.includes(o.text);
el.dispatchEvent(new Event('input', {bubbles:true})); el.dispatchEvent(new Event('change', {bubbles:true})); return true; })()`, ref, b)
	var ok bool
	if err := t.Eval(ctx, js, &ok); err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("element %s is not a select", ref)
	}
	return nil
}

func (t *Tab) SetFiles(ctx context.Context, ref string, files []string) error {
	t.mu.Lock()
	ch := t.chooser
	t.chooser = nil
	t.mu.Unlock()
	if ref == "" && ch != nil {
		if bid, ok := ch["backendNodeId"].(float64); ok {
			return t.call(ctx, "DOM.setFileInputFiles", map[string]any{"files": files, "backendNodeId": int64(bid)}, nil)
		}
	}
	var r struct {
		Result struct {
			ObjectID string `json:"objectId"`
		} `json:"result"`
	}
	if err := t.call(ctx, "Runtime.evaluate", map[string]any{"expression": fmt.Sprintf(`(() => { const w = window.__carbon && window.__carbon.m.get(%q); return w && w.deref(); })()`, ref)}, &r); err != nil {
		return err
	}
	if r.Result.ObjectID == "" {
		return fmt.Errorf("element %s not found", ref)
	}
	return t.call(ctx, "DOM.setFileInputFiles", map[string]any{"files": files, "objectId": r.Result.ObjectID}, nil)
}

func (t *Tab) WaitFor(ctx context.Context, selector, text, urlContains string, timeout time.Duration) error {
	if timeout == 0 {
		timeout = 15 * time.Second
	}
	sb, _ := json.Marshal(selector)
	tb, _ := json.Marshal(text)
	ub, _ := json.Marshal(urlContains)
	js := fmt.Sprintf(`(() => { const s = %s, t = %s, u = %s;
if (u && !location.href.includes(u)) return false;
if (s && !document.querySelector(s)) return false;
if (t && !(document.body && document.body.innerText.includes(t))) return false; return true; })()`, sb, tb, ub)
	deadline := time.Now().Add(timeout)
	for {
		var ok bool
		err := t.Eval(ctx, js, &ok)
		if err == nil && ok {
			return nil
		}
		if time.Now().After(deadline) {
			return errors.New("timeout")
		}
		select {
		case <-time.After(150 * time.Millisecond):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

type Challenge struct {
	Kind string `json:"kind"`
	URL  string `json:"url"`
	Box  [4]int `json:"box"`
}

const challengeJS = `(() => {
const kinds = [['recaptcha', /google\.com\/recaptcha|recaptcha\.net/], ['hcaptcha', /hcaptcha\.com/], ['turnstile', /challenges\.cloudflare\.com/], ['arkose', /arkoselabs\.com|funcaptcha/], ['datadome', /captcha-delivery\.com/]];
for (const f of document.querySelectorAll('iframe')) { const src = f.src || ''; for (const [k, re] of kinds) { if (re.test(src)) { const r = f.getBoundingClientRect(); if (r.width > 0 && r.height > 0) return {kind: k, url: src, box: [Math.round(r.x), Math.round(r.y), Math.round(r.width), Math.round(r.height)]}; } } }
if (/just a moment|attention required|verify you are human|are you a robot/i.test(document.title + ' ' + (document.body ? document.body.innerText.slice(0, 500) : ''))) return {kind: 'interstitial', url: location.href, box: [0, 0, innerWidth, innerHeight]};
const g = document.querySelector('.g-recaptcha,.h-captcha,.cf-turnstile,[data-sitekey]'); if (g) { const r = g.getBoundingClientRect(); return {kind: 'widget', url: location.href, box: [Math.round(r.x), Math.round(r.y), Math.round(r.width), Math.round(r.height)]}; }
return null; })()`

func (t *Tab) DetectChallenge(ctx context.Context) (*Challenge, error) {
	var c *Challenge
	if err := t.Eval(ctx, challengeJS, &c); err != nil {
		return nil, err
	}
	return c, nil
}
