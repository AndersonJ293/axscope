// Popup: mostra as sessões conectadas (uma por agente) e permite reconectar.

function render(state) {
  const versionEl = document.getElementById('version');
  if (versionEl) versionEl.textContent = `v${chrome.runtime.getManifest().version}`;

  const pill = document.getElementById('pill');
  const label = document.getElementById('label');
  const detail = document.getElementById('detail');
  const list = document.getElementById('list');

  const sessions = (state && state.sessions) || [];

  pill.classList.remove('on', 'off');
  if (sessions.length > 0) {
    pill.classList.add('on');
    label.textContent = sessions.length === 1 ? 'conectado' : `${sessions.length} sessões`;
  } else {
    pill.classList.add('off');
    label.textContent = 'desconectado';
  }

  list.replaceChildren(
    ...sessions.map((s) => {
      const row = document.createElement('div');
      row.className = 'row';
      const name = document.createElement('span');
      name.className = 'name';
      name.textContent = s.session;
      const port = document.createElement('span');
      port.className = 'port';
      port.textContent = `:${s.port}`;
      row.append(name, port);
      return row;
    }),
  );

  detail.textContent = sessions.length
    ? 'Arraste uma aba para dentro do grupo para dar acesso a esse agente.'
    : 'Nenhum daemon ativo. Rode um comando axscope.';
}

function refresh() {
  chrome.runtime.sendMessage({ type: 'status' }, (state) => {
    if (chrome.runtime.lastError) {
      render({ sessions: [] });
      return;
    }
    render(state);
  });
}

document.getElementById('reconnect').addEventListener('click', () => {
  chrome.runtime.sendMessage({ type: 'reconnect' }, () => {
    setTimeout(refresh, 600);
  });
});

refresh();
setInterval(refresh, 1000);
