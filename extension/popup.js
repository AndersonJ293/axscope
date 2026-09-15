// Popup: mostra se a ponte está conectada e permite reconectar na mão.

function render(state) {
  const pill = document.getElementById('pill');
  const label = document.getElementById('label');
  const detail = document.getElementById('detail');

  pill.classList.remove('on', 'off');
  if (state && state.connected) {
    pill.classList.add('on');
    label.textContent = 'conectado';
  } else {
    pill.classList.add('off');
    label.textContent = 'desconectado';
  }
  detail.textContent = state && state.url ? state.url : '';
}

function refresh() {
  chrome.runtime.sendMessage({ type: 'status' }, (state) => {
    if (chrome.runtime.lastError) {
      render({ connected: false, url: '' });
      return;
    }
    render(state);
  });
}

document.getElementById('reconnect').addEventListener('click', () => {
  chrome.runtime.sendMessage({ type: 'reconnect' }, () => {
    setTimeout(refresh, 400);
  });
});

refresh();
setInterval(refresh, 1000);
