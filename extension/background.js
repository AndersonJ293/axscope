// Ponte entre o daemon (browser-use) e o navegador.
//
// O daemon fala CDP; aqui traduzimos só o domínio `Target` para a API de abas
// (chrome.tabs) e repassamos todo o resto para o chrome.debugger, que entrega
// CDP de verdade na aba. Resultado: o driver inteiro — snapshot da árvore de
// acessibilidade, clique por coordenada, digitação — funciona sem mudar nada,
// no navegador já logado do usuário.

const BRIDGE_URL = 'ws://127.0.0.1:8787/cdp';
const PROTOCOL = '1.3';
const HEARTBEAT_MS = 20000;

let ws = null;
let reconnectTimer = null;
let heartbeatTimer = null;

/** sessionId (o que o daemon fala) -> tabId (o que a API usa). */
const tabBySession = new Map();
/** targetId -> sessionId, para responder createTarget/attach. */
const sessionByTarget = new Map();

function sessionOf(tabId) {
  return `t${tabId}`;
}

// ---------------------------------------------------------------- transporte

function setStatus(status, detail) {
  chrome.storage.local.set({ status, detail: detail || '', at: Date.now() });
}

function connect() {
  if (ws && (ws.readyState === WebSocket.OPEN || ws.readyState === WebSocket.CONNECTING)) {
    return;
  }
  clearTimeout(reconnectTimer);
  try {
    ws = new WebSocket(BRIDGE_URL);
  } catch (err) {
    setStatus('erro', String(err));
    scheduleReconnect();
    return;
  }

  ws.onopen = () => {
    setStatus('conectado', BRIDGE_URL);
    startHeartbeat();
  };
  ws.onclose = () => {
    stopHeartbeat();
    ws = null;
    setStatus('desconectado', `sem daemon em ${BRIDGE_URL}`);
    scheduleReconnect();
  };
  ws.onerror = () => {
    // onclose cuida da reconexão; aqui só evitamos poluir o console.
  };
  ws.onmessage = (event) => {
    let msg;
    try {
      msg = JSON.parse(event.data);
    } catch {
      return;
    }
    handleRpc(msg).catch((err) => respondError(msg.id, String(err)));
  };
}

function scheduleReconnect() {
  clearTimeout(reconnectTimer);
  reconnectTimer = setTimeout(connect, 2000);
}

function startHeartbeat() {
  stopHeartbeat();
  // Mantém o service worker vivo e detecta queda rápido.
  heartbeatTimer = setInterval(() => {
    if (ws && ws.readyState === WebSocket.OPEN) {
      ws.send(JSON.stringify({ method: '__ping' }));
    }
  }, HEARTBEAT_MS);
}

function stopHeartbeat() {
  if (heartbeatTimer) {
    clearInterval(heartbeatTimer);
    heartbeatTimer = null;
  }
}

function sendRaw(obj) {
  if (ws && ws.readyState === WebSocket.OPEN) {
    ws.send(JSON.stringify(obj));
  }
}

function respond(id, result) {
  if (id === undefined || id === null) return;
  sendRaw({ id, result: result === undefined ? {} : result });
}

function respondError(id, message) {
  if (id === undefined || id === null) return;
  sendRaw({ id, error: { code: -32000, message } });
}

// ------------------------------------------------------------------- roteador

async function handleRpc(msg) {
  const { id, method, params, sessionId } = msg;

  if (!method) return;

  if (method.startsWith('Target.')) {
    const result = await handleTarget(method, params || {}, sessionId);
    respond(id, result);
    return;
  }

  if (!sessionId) {
    throw new Error(`comando ${method} sem sessão (aba)`);
  }
  const tabId = tabBySession.get(sessionId);
  if (tabId === undefined) {
    throw new Error(`sessão ${sessionId} não está mais ativa`);
  }
  const result = await chrome.debugger.sendCommand({ tabId }, method, params || {});
  respond(id, result);
}

async function handleTarget(method, params) {
  switch (method) {
    case 'Target.setDiscoverTargets':
    case 'Target.setAutoAttach':
    case 'Target.setDiscoverTargets':
      return {};

    case 'Target.getBrowserContexts':
      return { browserContextIds: [] };

    case 'Target.getTargets':
      return { targetInfos: await listTargets() };

    case 'Target.createTarget': {
      const tab = await chrome.tabs.create({
        url: params.url || 'about:blank',
        // background: não roubar a aba em foco do usuário.
        active: params.background === true ? false : false,
      });
      ensureSessionFor(tab.id);
      sendRaw({
        method: 'Target.targetCreated',
        params: { targetInfo: toTargetInfo(tab) },
      });
      return { targetId: String(tab.id) };
    }

    case 'Target.closeTarget': {
      const tabId = Number(params.targetId);
      await chrome.tabs.remove(tabId);
      return { success: true };
    }

    case 'Target.activateTarget': {
      // Ativa a aba dentro da janela, mas NÃO levantamos a janela: quem decide
      // isso é o usuário (--focus), não cada comando.
      const tabId = Number(params.targetId);
      await chrome.tabs.update(tabId, { active: true });
      return {};
    }

    case 'Target.attachToTarget':
      return attach(params.targetId);

    case 'Target.detachFromTarget': {
      const tabId = tabBySession.get(params.sessionId);
      if (tabId !== undefined) {
        tabBySession.delete(params.sessionId);
        sessionByTarget.delete(String(tabId));
        try {
          await chrome.debugger.detach({ tabId });
        } catch {
          /* já destacado */
        }
      }
      return {};
    }

    default:
      return {};
  }
}

function toTargetInfo(tab) {
  return {
    targetId: String(tab.id),
    type: 'page',
    title: tab.title || '',
    url: tab.url || '',
    attached: tabBySession.has(sessionOf(tab.id)),
    browserContextId: '',
  };
}

/** Lista as abas com a aba em foco primeiro (o agente começa onde você está). */
async function listTargets() {
  const tabs = await chrome.tabs.query({});
  const active = tabs.find((t) => t.active);
  const ordered = active ? [active, ...tabs.filter((t) => t.id !== active.id)] : tabs;
  return ordered.map(toTargetInfo);
}

async function attach(targetId) {
  const tabId = Number(targetId);
  if (Number.isNaN(tabId)) {
    throw new Error(`targetId inválido: ${targetId}`);
  }
  const existing = tabBySession.get(sessionOf(tabId));
  if (existing) {
    return { sessionId: existing };
  }
  await chrome.debugger.attach({ tabId }, PROTOCOL);
  const sessionId = sessionOf(tabId);
  tabBySession.set(sessionId, tabId);
  sessionByTarget.set(String(tabId), sessionId);
  return { sessionId };
}

function ensureSessionFor(tabId) {
  return sessionOf(tabId);
}

// -------------------------------------------------------------------- eventos

chrome.debugger.onEvent.addListener((source, method, params) => {
  const sessionId = tabBySession.get(sessionOf(source.tabId));
  if (!sessionId) return;
  sendRaw({ method, params, sessionId });
});

chrome.debugger.onDetach.addListener((source, reason) => {
  const sessionId = sessionOf(source.tabId);
  if (tabBySession.delete(sessionId)) {
    sessionByTarget.delete(String(source.tabId));
    sendRaw({
      method: 'Target.detachedFromTarget',
      params: { sessionId, reason },
      sessionId,
    });
  }
});

chrome.tabs.onCreated.addListener((tab) => {
  sendRaw({
    method: 'Target.targetCreated',
    params: { targetInfo: toTargetInfo(tab) },
  });
});

chrome.tabs.onRemoved.addListener((tabId) => {
  const sessionId = sessionOf(tabId);
  tabBySession.delete(sessionId);
  sessionByTarget.delete(String(tabId));
  sendRaw({
    method: 'Target.targetDestroyed',
    params: { targetId: String(tabId) },
  });
});

chrome.tabs.onUpdated.addListener((tabId, changeInfo, tab) => {
  if (!changeInfo.url && !changeInfo.title && changeInfo.status !== 'complete') return;
  sendRaw({
    method: 'Target.targetInfoChanged',
    params: { targetInfo: toTargetInfo(tab) },
  });
});

chrome.runtime.onMessage.addListener((msg, _sender, sendResponse) => {
  if (msg && msg.type === 'status') {
    sendResponse({ connected: !!ws && ws.readyState === WebSocket.OPEN, url: BRIDGE_URL });
    return true;
  }
  if (msg && msg.type === 'reconnect') {
    if (ws) {
      try {
        ws.close();
      } catch {
        /* ignora */
      }
    }
    connect();
    sendResponse({ ok: true });
    return true;
  }
  return false;
});

chrome.runtime.onStartup.addListener(connect);
chrome.runtime.onInstalled.addListener(connect);

// O service worker acorda e reconecta sozinho.
connect();
