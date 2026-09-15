// Overlay injetado na página: cursor renderizado, halo, ripple de clique,
// spotlight no alvo e um HUD com abas/ação.
//
// Cuidados deliberados, porque roda em páginas reais e hostis:
//   - sem innerHTML (páginas com Trusted Types recusariam);
//   - CSS por adoptedStyleSheets (não é bloqueado por `style-src`);
//   - posicionamento por CSSOM (el.style.*), não por atributo style;
//   - shadow DOM + pointer-events:none: não intercepta nada da página;
//   - aria-hidden: não polui a árvore de acessibilidade (o `snap`).
(() => {
  if (window.__bu && window.__bu.__v) return;

  const SVGNS = 'http://www.w3.org/2000/svg';
  const CURSOR_PATH =
    'M6 2.6 L6 22.2 L10.9 17.6 L14.3 24.6 L17.6 23.0 L14.2 16.2 L21.6 15.9 Z';

  const CSS = `
    .cursor, .halo, .ripple, .spotlight, .hud {
      position: fixed; left: 0; top: 0; pointer-events: none;
    }

    /* ---- cursor ---- */
    .cursor {
      width: 28px; height: 28px; z-index: 5;
      transform: translate(-200px, -200px);
      transition: transform 260ms cubic-bezier(.22,1,.36,1);
      will-change: transform;
      filter: drop-shadow(0 3px 6px rgba(2,6,23,.45))
              drop-shadow(0 1px 1px rgba(2,6,23,.35));
    }
    .cursor svg {
      display: block; transform-origin: 6px 3px;
      transition: transform 110ms cubic-bezier(.3,1.5,.5,1);
    }
    .cursor.press svg { transform: scale(.74); }

    /* ---- halo que acompanha o ponteiro ---- */
    .halo {
      width: 44px; height: 44px; margin: -22px 0 0 -22px; z-index: 1;
      border-radius: 50%;
      background: radial-gradient(circle at 50% 50%,
        rgba(99,102,241,.30) 0%, rgba(99,102,241,.14) 42%, rgba(99,102,241,0) 70%);
      opacity: 0;
      transform: translate(-200px, -200px);
      transition: transform 340ms cubic-bezier(.22,1,.36,1), opacity 200ms ease;
      will-change: transform;
    }
    .halo.on { opacity: 1; }
    .halo.pulse { animation: bu-halo 620ms cubic-bezier(.2,.8,.2,1); }
    @keyframes bu-halo {
      0%   { box-shadow: 0 0 0 0 rgba(99,102,241,.55); }
      100% { box-shadow: 0 0 0 26px rgba(99,102,241,0); }
    }

    /* ---- ripple do clique ---- */
    .ripple {
      width: 22px; height: 22px; margin: -11px 0 0 -11px; z-index: 2;
      border-radius: 50%;
      border: 2px solid rgba(199,210,254,.95);
      background: rgba(99,102,241,.28);
      box-shadow: 0 0 18px rgba(99,102,241,.55);
      opacity: 0; transform: scale(.3);
    }
    .ripple.on { animation: bu-ripple 560ms cubic-bezier(.2,.8,.2,1); }
    @keyframes bu-ripple {
      0%   { opacity: .95; transform: scale(.30); }
      70%  { opacity: .35; }
      100% { opacity: 0;   transform: scale(3.6); }
    }

    /* ---- spotlight no alvo (borda em gradiente) ---- */
    .spotlight {
      z-index: 0; border-radius: 10px;
      border: 1.5px solid transparent;
      background:
        linear-gradient(rgba(99,102,241,.10), rgba(139,92,246,.10)) padding-box,
        linear-gradient(135deg, #818cf8, #a78bfa) border-box;
      box-shadow:
        0 0 0 3px rgba(99,102,241,.12),
        0 10px 34px rgba(99,102,241,.30),
        inset 0 0 24px rgba(99,102,241,.10);
      opacity: 0; transform: scale(.985);
      transition: opacity 170ms ease, transform 170ms cubic-bezier(.22,1,.36,1);
    }
    .spotlight.on { opacity: 1; transform: scale(1); }

    /* ---- HUD ---- */
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
      animation: bu-pulse 2.4s ease-in-out infinite;
    }
    @keyframes bu-pulse {
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

    /* Respeita quem pediu menos movimento. */
    @media (prefers-reduced-motion: reduce) {
      .cursor, .halo, .spotlight, .hud { transition-duration: 1ms; }
      .cursor svg { transition-duration: 1ms; }
      .halo.pulse, .ripple.on, .hud .dot { animation: none; }
    }
  `;

  let host = null, root = null;
  let cursorEl = null, haloEl = null, rippleEl = null, spotEl = null;
  let hudEl = null, hudTabs = null, hudLabel = null;
  let visible = true, hudVisible = true;

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
    host.setAttribute('data-bu-root', '');
    // Fora da árvore de acessibilidade: o overlay não pode poluir o `snap`.
    host.setAttribute('aria-hidden', 'true');
    host.style.cssText =
      'position:fixed;left:0;top:0;width:0;height:0;z-index:2147483647;' +
      'pointer-events:none;contain:style;';

    try {
      root = host.attachShadow({ mode: 'open' });
    } catch {
      root = host.attachShadow({ mode: 'closed' });
    }

    // CSS: declarative stylesheet (imune a `style-src`) com fallback para <style>.
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

    haloEl = div('halo');
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
    grad.setAttribute('id', 'bu-grad');
    grad.setAttribute('x1', '0');
    grad.setAttribute('y1', '0');
    grad.setAttribute('x2', '0.35');
    grad.setAttribute('y2', '1');
    const stop1 = document.createElementNS(SVGNS, 'stop');
    stop1.setAttribute('offset', '0');
    stop1.setAttribute('stop-color', '#ffffff');
    const stop2 = document.createElementNS(SVGNS, 'stop');
    stop2.setAttribute('offset', '1');
    stop2.setAttribute('stop-color', '#dbe4fb');
    grad.appendChild(stop1);
    grad.appendChild(stop2);
    defs.appendChild(grad);

    const path = document.createElementNS(SVGNS, 'path');
    path.setAttribute('d', CURSOR_PATH);
    path.setAttribute('fill', 'url(#bu-grad)');
    path.setAttribute('stroke', '#0b1220');
    path.setAttribute('stroke-width', '1.4');
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

    root.appendChild(haloEl);
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
    if (haloEl) haloEl.style.display = visible ? '' : 'none';
    if (spotEl) spotEl.style.display = visible ? '' : 'none';
    if (hudEl) hudEl.style.display = hudVisible ? '' : 'none';
  }

  function place(el, x, y) {
    el.style.transform = `translate(${Math.round(x)}px, ${Math.round(y)}px)`;
  }

  function moveCursor(x, y) {
    if (!build()) return;
    place(cursorEl, x, y);
    haloEl.classList.add('on');
    place(haloEl, x, y);
  }

  function ripple(x, y) {
    if (!build()) return;
    rippleEl.style.left = `${Math.round(x)}px`;
    rippleEl.style.top = `${Math.round(y)}px`;
    rippleEl.classList.remove('on');
    void rippleEl.offsetWidth; // reinicia a animação
    rippleEl.classList.add('on');

    haloEl.classList.remove('pulse');
    void haloEl.offsetWidth;
    haloEl.classList.add('pulse');
  }

  window.__bu = {
    __v: 1,
    ready: build,
    show(value) { visible = !!value; applyVisibility(); },
    hudVisible(value) { hudVisible = !!value; applyVisibility(); },
    cursor: moveCursor,
    press(x, y, kind) {
      moveCursor(x, y);
      ripple(x, y);
      if (!build()) return;
      cursorEl.classList.add('press');
      setTimeout(() => cursorEl && cursorEl.classList.remove('press'), 150);
      if (kind === 'right') ripple(x, y);
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
