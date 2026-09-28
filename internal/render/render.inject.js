// Overlay injected into the page: rendered cursor, click ripple, spotlight and
// HUD. Built with createElement/CSSOM/adoptedStyleSheets, never innerHTML.
(() => {
  const VERSION = 2;
  if (window.__axscope && window.__axscope.__v >= VERSION) return;
  // An older overlay (a daemon upgraded under a live page) steps aside instead of
  // leaving a second cursor behind.
  document.querySelectorAll('[data-axscope-root]').forEach((el) => el.remove());

  const SVGNS = 'http://www.w3.org/2000/svg';
  // A rounded pointer with no tail: the tip is at (5, 3), where the click lands.
  const CURSOR_PATH =
    'M5 4.6C5 3.2 6.6 2.4 7.7 3.3L22.3 15.1C23.5 16 22.8 17.9 21.3 17.9H15.1' +
    'C14.5 17.9 13.9 18.2 13.6 18.7L10 23.8C9.1 25.1 7 24.5 7 22.9Z';

  const CSS = `
    .cursor, .ripple, .spotlight, .hud {
      position: fixed; left: 0; top: 0; pointer-events: none;
    }

    /* ---- cursor ---- */
    .cursor {
      width: 28px; height: 28px; z-index: 5;
      transform: translate(-200px, -200px);
      /* The duration is set per move (glide): short hops are quick, long ones
         a little longer, and the Go side waits exactly that long. */
      transition: transform 0ms cubic-bezier(.22,1,.36,1);
      will-change: transform;
      filter: drop-shadow(0 4px 8px rgba(76,29,149,.35))
              drop-shadow(0 1px 2px rgba(2,6,23,.40));
    }
    .cursor svg { display: block; transform-origin: 5px 3px; }
    /* The click: a quick tilt around the tip and a spring back. */
    .cursor.press svg { animation: axscope-tilt 240ms cubic-bezier(.3,1.4,.5,1); }
    @keyframes axscope-tilt {
      0%   { transform: rotate(0) scale(1); }
      35%  { transform: rotate(-16deg) scale(.88); }
      100% { transform: rotate(0) scale(1); }
    }

    /* ---- click ripple ---- */
    /* The click is the only attention moment: the cursor sinks and a short ring
       confirms the point. */
    .ripple {
      width: 20px; height: 20px; margin: -10px 0 0 -10px; z-index: 2;
      border-radius: 50%;
      border: 1.5px solid rgba(199,210,254,.85);
      background: rgba(99,102,241,.22);
      box-shadow: 0 0 12px rgba(99,102,241,.40);
      opacity: 0; transform: scale(.35);
    }
    .ripple.on { animation: axscope-ripple 460ms cubic-bezier(.2,.8,.2,1); }
    @keyframes axscope-ripple {
      0%   { opacity: .9; transform: scale(.35); }
      100% { opacity: 0;  transform: scale(2.6); }
    }

    /* Pure outline, no background: a gradient border needs an opaque interior
       and would cover the content. */
    .spotlight {
      z-index: 0; border-radius: 10px;
      border: 2px solid rgba(129,140,248,.95);
      background: transparent;
      box-shadow:
        0 0 0 3px rgba(99,102,241,.16),
        0 0 20px rgba(99,102,241,.35);
      opacity: 0; transform: scale(.985);
      transition: opacity 170ms ease, transform 170ms cubic-bezier(.22,1,.36,1);
    }
    .spotlight.on { opacity: 1; transform: scale(1); }

    /* ---- HUD ---- */
    /* top:auto cancels the shared top:0 so the bottom anchor applies. */
    .hud {
      top: auto; left: 14px; bottom: 14px; z-index: 6;
      display: flex; align-items: center; gap: 10px;
      padding: 7px 13px 7px 11px; border-radius: 999px;
      font: 12px/1.3 ui-monospace, "SF Mono", Menlo, Consolas, monospace;
      color: #e8edf7;
      background: linear-gradient(180deg, rgba(17,24,39,.94), rgba(10,14,24,.94));
      -webkit-backdrop-filter: blur(10px) saturate(1.25);
      backdrop-filter: blur(10px) saturate(1.25);
      border: 1px solid rgba(148,163,184,.26);
      box-shadow:
        0 12px 34px rgba(2,6,23,.50),
        inset 0 1px 0 rgba(255,255,255,.07);
      max-width: 72vw;
      opacity: 0; transform: translateY(6px);
      transition: opacity 200ms ease, transform 200ms cubic-bezier(.22,1,.36,1);
      white-space: nowrap;
    }
    .hud.on { opacity: 1; transform: translateY(0); }
    .hud .dot {
      flex: none; width: 7px; height: 7px; border-radius: 50%;
      background: linear-gradient(135deg, #818cf8, #a78bfa);
      box-shadow: 0 0 10px rgba(129,140,248,.9);
      animation: axscope-pulse 2.4s ease-in-out infinite;
    }
    @keyframes axscope-pulse {
      0%, 100% { opacity: 1;   transform: scale(1); }
      50%      { opacity: .45; transform: scale(.82); }
    }
    .hud .tabs {
      color: #c7d2fe; font-weight: 700; letter-spacing: .02em;
      background: rgba(99,102,241,.16);
      border: 1px solid rgba(129,140,248,.30);
      padding: 1px 7px; border-radius: 999px;
    }
    .hud .sep { flex: none; width: 1px; height: 13px; background: rgba(148,163,184,.28); }
    .hud .label {
      overflow: hidden; text-overflow: ellipsis; max-width: 54vw; color: #cbd5e1;
    }
    .hud .label:empty { display: none; }
    .hud .sep:has(+ .label:empty) { display: none; }

    /* Respects those who asked for less motion. */
    @media (prefers-reduced-motion: reduce) {
      .cursor, .spotlight, .hud { transition-duration: 1ms; }
      .ripple.on, .hud .dot, .cursor.press svg { animation: none; }
    }
  `;

  let host = null, root = null;
  let cursorEl = null, rippleEl = null, spotEl = null;
  let rippleTimer = 0;
  let hudEl = null, hudTabs = null, hudLabel = null;
  let visible = true, hudVisible = true;
  // Where the cursor is drawn. A new document starts unplaced; the first move
  // carries the point the previous page left it at, so it does not pop in.
  let placed = false, curX = 0, curY = 0;
  const reducedMotion = () =>
    !!(window.matchMedia && matchMedia('(prefers-reduced-motion: reduce)').matches);

  function div(cls) {
    const el = document.createElement('div');
    el.className = cls;
    return el;
  }

  function build() {
    if (host && host.isConnected) return true;
    const docEl = document.documentElement;
    if (!docEl) return false;

    host = document.createElement('div');
    host.setAttribute('data-axscope-root', '');
    // Outside the accessibility tree: the overlay must not pollute the `snap`.
    host.setAttribute('aria-hidden', 'true');
    host.style.cssText =
      'position:fixed;left:0;top:0;width:0;height:0;z-index:2147483647;' +
      'pointer-events:none;contain:style;';

    // Shadow DOM isolates the overlay styles; pointer-events:none on the host
    // keeps it from intercepting page clicks.
    try {
      root = host.attachShadow({ mode: 'open' });
    } catch {
      root = host.attachShadow({ mode: 'closed' });
    }

    // CSS: declarative stylesheet (immune to `style-src`) with a <style> fallback.
    let styled = false;
    try {
      const sheet = new CSSStyleSheet();
      sheet.replaceSync(CSS);
      root.adoptedStyleSheets = [sheet];
      styled = true;
    } catch {
      styled = false;
    }
    if (!styled) {
      const style = document.createElement('style');
      style.textContent = CSS;
      root.appendChild(style);
    }

    spotEl = div('spotlight');
    rippleEl = div('ripple');
    cursorEl = div('cursor');

    const svg = document.createElementNS(SVGNS, 'svg');
    svg.setAttribute('viewBox', '0 0 28 28');
    svg.setAttribute('width', '28');
    svg.setAttribute('height', '28');
    svg.setAttribute('aria-hidden', 'true');

    const defs = document.createElementNS(SVGNS, 'defs');
    const grad = document.createElementNS(SVGNS, 'linearGradient');
    grad.setAttribute('id', 'axscope-grad');
    grad.setAttribute('x1', '0');
    grad.setAttribute('y1', '0');
    grad.setAttribute('x2', '0.35');
    grad.setAttribute('y2', '1');
    // The HUD's indigo → violet, so cursor and HUD read as one thing.
    const stop1 = document.createElementNS(SVGNS, 'stop');
    stop1.setAttribute('offset', '0');
    stop1.setAttribute('stop-color', '#818cf8');
    const stop2 = document.createElementNS(SVGNS, 'stop');
    stop2.setAttribute('offset', '1');
    stop2.setAttribute('stop-color', '#8b5cf6');
    grad.appendChild(stop1);
    grad.appendChild(stop2);
    defs.appendChild(grad);

    const path = document.createElementNS(SVGNS, 'path');
    path.setAttribute('d', CURSOR_PATH);
    path.setAttribute('fill', 'url(#axscope-grad)');
    // A white rim keeps the pointer legible on dark and light pages alike.
    path.setAttribute('stroke', '#ffffff');
    path.setAttribute('stroke-width', '1.8');
    path.setAttribute('stroke-linejoin', 'round');
    path.setAttribute('stroke-linecap', 'round');
    svg.appendChild(defs);
    svg.appendChild(path);
    cursorEl.appendChild(svg);

    hudEl = div('hud');
    const dot = div('dot');
    hudTabs = document.createElement('span');
    hudTabs.className = 'tabs';
    const sep = div('sep');
    hudLabel = document.createElement('span');
    hudLabel.className = 'label';
    hudEl.appendChild(dot);
    hudEl.appendChild(hudTabs);
    hudEl.appendChild(sep);
    hudEl.appendChild(hudLabel);

    root.appendChild(spotEl);
    root.appendChild(rippleEl);
    root.appendChild(cursorEl);
    root.appendChild(hudEl);

    docEl.appendChild(host);
    applyVisibility();
    return true;
  }

  function applyVisibility() {
    if (!cursorEl) return;
    cursorEl.style.display = visible ? '' : 'none';

    if (spotEl) spotEl.style.display = visible ? '' : 'none';
    if (hudEl) hudEl.style.display = hudVisible ? '' : 'none';
  }

  // CSSOM positioning via el.style, never the style attribute.
  function place(el, x, y) {
    el.style.transform = `translate(${Math.round(x)}px, ${Math.round(y)}px)`;
  }

  // glide moves the cursor to (x, y) and returns how long the move takes (ms),
  // so the real input can land when the drawn cursor arrives. opts.from is the
  // point the cursor had before a navigation rebuilt the overlay; opts.max caps
  // the duration (0 = jump).
  function glide(x, y, opts) {
    if (!build()) return 0;
    const from = opts && opts.from;
    const max = opts && typeof opts.max === 'number' ? opts.max : 0;
    if (!placed && from) {
      cursorEl.style.transitionDuration = '0ms';
      place(cursorEl, from[0], from[1]);
      void cursorEl.offsetWidth; // commits the start point before the glide
      curX = from[0]; curY = from[1]; placed = true;
    }
    const dist = placed ? Math.hypot(x - curX, y - curY) : 0;
    let ms = 0;
    if (visible && max > 0 && dist >= 1 && !reducedMotion()) {
      ms = Math.round(Math.min(max, Math.max(40, 40 + dist / 20)));
    }
    cursorEl.style.transitionDuration = `${ms}ms`;
    place(cursorEl, x, y);
    curX = x; curY = y; placed = true;
    return ms;
  }

  function ripple(x, y) {
    if (!build()) return;
    rippleEl.style.left = `${Math.round(x)}px`;
    rippleEl.style.top = `${Math.round(y)}px`;
    rippleEl.classList.remove('on');
    void rippleEl.offsetWidth; // restarts the animation
    rippleEl.classList.add('on');
    // Do not rely on the animation finishing: a background tab freezes it, so
    // clear the class after a fixed timeout.
    clearTimeout(rippleTimer);
    rippleTimer = setTimeout(() => rippleEl && rippleEl.classList.remove('on'), 520);
  }

  window.__axscope = {
    __v: VERSION,
    ready: build,
    show(value) { visible = !!value; applyVisibility(); },
    hudVisible(value) { hudVisible = !!value; applyVisibility(); },
    cursor: glide,
    // press glides to the point, then sinks and ripples on arrival: the ring
    // must mark where the click lands, not where the cursor was headed.
    press(x, y, kind, opts) {
      const ms = glide(x, y, opts);
      if (!cursorEl) return ms;
      setTimeout(() => {
        ripple(x, y);
        if (!cursorEl) return;
        cursorEl.classList.remove('press');
        void cursorEl.offsetWidth; // restarts the tilt on a quick second click
        cursorEl.classList.add('press');
        setTimeout(() => cursorEl && cursorEl.classList.remove('press'), 260);
      }, ms);
      return ms;
    },
    spotlight(rect) {
      if (!build()) return;
      if (!rect || rect.width <= 0 || rect.height <= 0) {
        spotEl.classList.remove('on');
        return;
      }
      spotEl.style.left = `${Math.round(rect.x)}px`;
      spotEl.style.top = `${Math.round(rect.y)}px`;
      spotEl.style.width = `${Math.round(rect.width)}px`;
      spotEl.style.height = `${Math.round(rect.height)}px`;
      spotEl.classList.add('on');
    },
    clearSpotlight() { if (spotEl) spotEl.classList.remove('on'); },
    hud(state) {
      if (!build()) return;
      const tabs = state && state.tabs ? String(state.tabs) : '';
      const label = state && state.label ? String(state.label) : '';
      hudTabs.textContent = tabs;
      hudLabel.textContent = label;
      hudEl.classList.toggle('on', hudVisible && (tabs !== '' || label !== ''));
    },
    clear() { if (spotEl) spotEl.classList.remove('on'); if (hudEl) hudEl.classList.remove('on'); },
  };

  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', build, { once: true });
  } else {
    build();
  }
})();
