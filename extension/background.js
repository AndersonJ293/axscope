// Ponte entre os daemons (browser-use) e o navegador.
//
// Cada sessão do browser-use ocupa uma porta da faixa 8787..8802 e recebe o seu
// próprio grupo de abas. A extensão mantém uma conexão por porta e traduz só o
// domínio `Target` para a API de abas; todo o resto vai para o chrome.debugger.
//
// Consequência: vários agentes rodam ao mesmo tempo, cada um enxergando apenas
// as abas do seu grupo — as abas pessoais do usuário ficam intocadas.

const BASE_PORT = 8787;
const PORT_SPAN = 16;
const PROTOCOL = '1.3';
const HEARTBEAT_MS = 20000;
const RETRY_MS = 2500;

const GROUP_COLORS = ['blue', 'purple', 'green', 'orange', 'red', 'cyan', 'pink', 'yellow'];

/** port -> estado da conexão daquela sessão. */
const conns = new Map();
/** tabId -> estado da conexão dona daquela aba. */
const ownerByTab = new Map();

let heartbeatTimer = null;

// ------------------------------------------------------------------ contexto

function groupTitle(session) {
  return `browser-use · ${session}`;
}

function groupColor(session) {
  let hash = 0;
  for (let i = 0; i < session.length; i++) hash = (hash * 31 + session.charCodeAt(i)) | 0;
  return GROUP_COLORS[Math.abs(hash) % GROUP_COLORS.length];
}

function connectedCount() {
  let n = 0;
  for (const st of conns.values()) {
    if (st.ws && st.ws.readyState === WebSocket.OPEN) n++;
  }
  return n;
}

function refreshStatus() {
  const sessions = [];
  for (const st of conns.values()) {
    if (st.ws && st.ws.readyState === WebSocket.OPEN) {
      sessions.push({ session: st.session || '(aguardando)', port: st.port });
    }
  }
  chrome.storage.local.set({ connected: sessions.length > 0, sessions, at: Date.now() });
}

// ---------------------------------------------------------------- transporte

function connectAll() {
  for (let i = 0; i < PORT_SPAN; i++) {
    connectPort(BASE_PORT + i);
  }
}

function connectPort(port) {
  const existing = conns.get(port);
  if (existing) {
    const rs = existing.ws && existing.ws.readyState;
    if (rs === WebSocket.OPEN || rs === WebSocket.CONNECTING) return;
  }
  if (!existing) {
    conns.set(port, { port, ws: null, session: null, groupId: null, tabBySession: new Map() });
  }
  const st = conns.get(port);

  let ws;
  try {
    ws = new WebSocket(`ws://127.0.0.1:${port}/cdp`);
  } catch {
    return; // porta sem daemon: tenta de novo no próximo ciclo
  }
  st.ws = ws;

  ws.onopen = () => {
    refreshStatus();
  };
  ws.onclose = () => {
    st.ws = null;
    // A sessão caiu: solta as abas dela para o mapa de donos.
    for (const [tabId, owner] of ownerByTab) {
      if (owner === st) ownerByTab.delete(tabId);
    }
    st.tabBySession.clear();
    refreshStatus();
  };
  ws.onerror = () => {
    /* onclose cuida da reconexão */
  };
  ws.onmessage = (event) => {
    let msg;
    try {
      msg = JSON.parse(event.data);
    } catch {
      return;
    }
    handleMessage(st, msg).catch((err) => respondError(st, msg.id, String(err)));
  };
}

function sendOn(st, obj) {
  if (st.ws && st.ws.readyState === WebSocket.OPEN) {
    st.ws.send(JSON.stringify(obj));
  }
}

function respond(st, id, result) {
  if (id === undefined || id === null) return;
  sendOn(st, { id, result: result === undefined ? {} : result });
}

function respondError(st, id, message) {
  if (id === undefined || id === null) return;
  sendOn(st, { id, error: { code: -32000, message } });
}

function startHeartbeat() {
  if (heartbeatTimer) return;
  heartbeatTimer = setInterval(() => {
    for (const st of conns.values()) {
      if (st.ws && st.ws.readyState === WebSocket.OPEN) sendOn(st, { method: '__ping' });
    }
  }, HEARTBEAT_MS);
}

// ------------------------------------------------------------------- roteador

async function handleMessage(st, msg) {
  const { id, method, params, sessionId } = msg;

  if (method === '__session') {
    st.session = (params && params.session) || 'default';
    await adoptExistingGroup(st);
    refreshStatus();
    return;
  }
  if (!method) return;

  if (method.startsWith('Target.')) {
    respond(st, id, await handleTarget(st, method, params || {}));
    return;
  }

  if (!sessionId) throw new Error(`comando ${method} sem sessão (aba)`);
  const tabId = st.tabBySession.get(sessionId);
  if (tabId === undefined) throw new Error(`sessão ${sessionId} não está mais ativa`);
  respond(st, id, await chrome.debugger.sendCommand({ tabId }, method, params || {}));
}

// ---------------------------------------------------------------------- grupo

async function adoptExistingGroup(st) {
  if (!st.session) return;
  try {
    const groups = await chrome.tabGroups.query({ title: groupTitle(st.session) });
    if (groups.length > 0) {
      st.groupId = groups[0].id;
      await syncGroupTabs(st);
    }
  } catch {
    /* tabGroups indisponível: segue sem grupo */
  }
}

/** Reassocia as abas do grupo que já existia na sessão recém-conectada. */
async function syncGroupTabs(st) {
  if (st.groupId === null || st.groupId === undefined) return;
  const tabs = await chrome.tabs.query({});
  for (const tab of tabs) {
    if (tab.groupId === st.groupId) ownerByTab.set(tab.id, st);
  }
}

async function addTabToGroup(st, tabId) {
  if (!st.session) return;
  try {
    if (st.groupId === null || st.groupId === undefined) {
      const gid = await chrome.tabs.group({ tabIds: [tabId] });
      st.groupId = gid;
      await chrome.tabGroups.update(gid, {
        title: groupTitle(st.session),
        color: groupColor(st.session),
        collapsed: false,
      });
    } else {
      await chrome.tabs.group({ tabIds: [tabId], groupId: st.groupId });
    }
    ownerByTab.set(tabId, st);
  } catch {
    /* sem permissão de grupo: a aba continua utilizável */
  }
}

/** Só as abas deste grupo — é o isolamento entre sessões. */
async function listGroupTabs(st) {
  if (st.groupId === null || st.groupId === undefined) return [];
  const tabs = await chrome.tabs.query({});
  return tabs.filter((t) => t.groupId === st.groupId);
}

// -------------------------------------------------------------------- Target

async function handleTarget(st, method, params) {
  switch (method) {
    case 'Target.setDiscoverTargets':
    case 'Target.setAutoAttach':
    case 'Target.getBrowserContexts':
      return method === 'Target.getBrowserContexts' ? { browserContextIds: [] } : {};

    case 'Target.getTargets': {
      const tabs = await listGroupTabs(st);
      return { targetInfos: tabs.map((t) => toTargetInfo(st, t)) };
    }

    case 'Target.createTarget': {
      const tab = await chrome.tabs.create({ url: params.url || 'about:blank', active: false });
      await addTabToGroup(st, tab.id);
      sendOn(st, { method: 'Target.targetCreated', params: { targetInfo: toTargetInfo(st, tab) } });
      return { targetId: String(tab.id) };
    }

    case 'Target.closeTarget': {
      await chrome.tabs.remove(Number(params.targetId));
      return { success: true };
    }

    case 'Target.activateTarget': {
      // Ativa a aba no grupo sem levantar a janela: foco só se o usuário pedir.
      await chrome.tabs.update(Number(params.targetId), { active: true });
      return {};
    }

    case 'Target.attachToTarget':
      return attach(st, params.targetId);

    case 'Target.detachFromTarget': {
      const tabId = st.tabBySession.get(params.sessionId);
      if (tabId !== undefined) {
        st.tabBySession.delete(params.sessionId);
        ownerByTab.delete(tabId);
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

function toTargetInfo(st, tab) {
  return {
    targetId: String(tab.id),
    type: 'page',
    title: tab.title || '',
    url: tab.url || '',
    attached: st.tabBySession.has(`t${tab.id}`),
  };
}

async function attach(st, targetId) {
  const tabId = Number(targetId);
  if (Number.isNaN(tabId)) throw new Error(`targetId inválido: ${targetId}`);

  const sessionId = `t${tabId}`;
  if (st.tabBySession.has(sessionId)) return { sessionId };

  await chrome.debugger.attach({ tabId }, PROTOCOL);
  st.tabBySession.set(sessionId, tabId);
  ownerByTab.set(tabId, st);
  return { sessionId };
}

// -------------------------------------------------------------------- eventos

chrome.debugger.onEvent.addListener((source, method, params) => {
  const st = ownerByTab.get(source.tabId);
  if (!st) return;
  sendOn(st, { method, params, sessionId: `t${source.tabId}` });
});

chrome.debugger.onDetach.addListener((source, reason) => {
  const st = ownerByTab.get(source.tabId);
  if (!st) return;
  const sessionId = `t${source.tabId}`;
  st.tabBySession.delete(sessionId);
  ownerByTab.delete(source.tabId);
  sendOn(st, { method: 'Target.detachedFromTarget', params: { sessionId, reason }, sessionId });
});

chrome.tabs.onCreated.addListener((tab) => {
  // Aba nova só interessa à sessão cujo grupo ela entrar (definido adiante).
  const st = ownerByTab.get(tab.id);
  if (!st) return;
  sendOn(st, { method: 'Target.targetCreated', params: { targetInfo: toTargetInfo(st, tab) } });
});

chrome.tabs.onRemoved.addListener((tabId) => {
  const st = ownerByTab.get(tabId);
  if (!st) return;
  st.tabBySession.delete(`t${tabId}`);
  ownerByTab.delete(tabId);
  sendOn(st, { method: 'Target.targetDestroyed', params: { targetId: String(tabId) } });
});

chrome.tabs.onUpdated.addListener((tabId, changeInfo, tab) => {
  const st = ownerByTab.get(tabId);
  if (!st) return;
  if (!changeInfo.url && !changeInfo.title && changeInfo.status !== 'complete') return;
  sendOn(st, { method: 'Target.targetInfoChanged', params: { targetInfo: toTargetInfo(st, tab) } });
});

// -------------------------------------------------------------------- runtime

chrome.runtime.onMessage.addListener((msg, _sender, sendResponse) => {
  if (msg && msg.type === 'status') {
    const sessions = [];
    for (const st of conns.values()) {
      if (st.ws && st.ws.readyState === WebSocket.OPEN) {
        sessions.push({ session: st.session || '(aguardando)', port: st.port });
      }
    }
    sendResponse({ connected: sessions.length > 0, sessions });
    return true;
  }
  if (msg && msg.type === 'reconnect') {
    for (const st of conns.values()) {
      if (st.ws) {
        try {
          st.ws.close();
        } catch {
          /* ignora */
        }
      }
    }
    conns.clear();
    ownerByTab.clear();
    connectAll();
    sendResponse({ ok: true });
    return true;
  }
  return false;
});

chrome.runtime.onStartup.addListener(() => {
  connectAll();
  startHeartbeat();
});
chrome.runtime.onInstalled.addListener(() => {
  connectAll();
  startHeartbeat();
});

// O service worker acorda e reconecta sozinho.
connectAll();
startHeartbeat();
setInterval(connectAll, RETRY_MS);
