// One connection + tab group per axscope session (ports 8787..8802), so agents
// see only their own tabs. Only the `Target` domain is emulated; rest goes to chrome.debugger.

const BASE_PORT = 8787;
const PORT_SPAN = 16;
const PROTOCOL = '1.3';
const HEARTBEAT_MS = 20000;
const RETRY_MS = 2500;

const GROUP_COLORS = ['blue', 'purple', 'green', 'orange', 'red', 'cyan', 'pink', 'yellow'];

/** port -> state of that session's connection. */
const conns = new Map();
/** tabId -> state of the connection that owns that tab. */
const ownerByTab = new Map();

let heartbeatTimer = null;

// ------------------------------------------------------------------- context

const AGENT_DEFAULT = 'axscope';

/**
 * Next free number for the display title ("Opencode 1", "Opencode 2", …). The
 * title is cosmetic: group ownership is the session, not this name.
 */
async function nextTitleFor(agent) {
  const name = agent || AGENT_DEFAULT;
  let max = 0;
  try {
    const groups = await chrome.tabGroups.query({});
    const re = new RegExp(`^${name.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')} (\\d+)$`);
    for (const g of groups) {
      const m = re.exec(g.title || '');
      if (m) max = Math.max(max, parseInt(m[1], 10));
    }
  } catch {
    /* no tabGroups: use 1 */
  }
  return `${name} ${max + 1}`;
}

function groupColor(agent) {
  const name = agent || AGENT_DEFAULT;
  let hash = 0;
  for (let i = 0; i < name.length; i++) hash = (hash * 31 + name.charCodeAt(i)) | 0;
  return GROUP_COLORS[Math.abs(hash) % GROUP_COLORS.length];
}

function refreshStatus() {
  const sessions = [];
  for (const st of conns.values()) {
    if (st.ws && st.ws.readyState === WebSocket.OPEN) {
      sessions.push({ session: st.session || '(waiting)', port: st.port });
    }
  }
  chrome.storage.local.set({ connected: sessions.length > 0, sessions, at: Date.now() });
}

// ----------------------------------------------------------------- transport

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
    return; // port without a daemon: retry on the next cycle
  }
  st.ws = ws;

  ws.onopen = () => {
    refreshStatus();
  };
  ws.onclose = () => {
    st.ws = null;
    // The daemon died: release the debugger from this session's tabs. Without
    // this the tab stays stuck and the next daemon cannot attach.
    for (const tabId of st.tabBySession.values()) {
      chrome.debugger.detach({ tabId }).catch(() => {});
    }
    for (const [tabId, owner] of ownerByTab) {
      if (owner === st) ownerByTab.delete(tabId);
    }
    st.tabBySession.clear();
    refreshStatus();
  };
  ws.onerror = () => {
    /* onclose handles reconnection */
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

// -------------------------------------------------------------------- router

async function handleMessage(st, msg) {
  const { id, method, params, sessionId } = msg;

  if (method === '__session') {
    st.session = (params && params.session) || 'default';
    const agent = (params && params.agent) || AGENT_DEFAULT;
    if (st.agent !== agent) {
      st.agent = agent;
      // Already has a group? Rename keeping the number ("Opencode 2" stays 2).
      if (st.groupId !== null && st.groupId !== undefined) {
        const m = / (\d+)$/.exec(st.groupTitle || '');
        st.groupTitle = `${agent}${m ? ' ' + m[1] : ''}`;
        try {
          await chrome.tabGroups.update(st.groupId, { title: st.groupTitle });
        } catch {
          /* group is gone */
        }
      } else {
        st.groupTitle = null;
      }
    }
    await adoptExistingGroup(st);
    refreshStatus();
    return;
  }
  if (!method) return;

  // Downloads: the debugger refuses the browser-level download commands, so the
  // extension saves the file itself with chrome.downloads (saveAs:false never
  // prompts, even when the browser asks where to save). The daemon polls the
  // status for the absolute path.
  if (method === 'Axscope.download') {
    const { url, filename } = params || {};
    const opts = { url, saveAs: false, conflictAction: 'uniquify' };
    if (filename) opts.filename = filename;
    respond(st, id, await chrome.downloads.download(opts));
    return;
  }
  if (method === 'Axscope.downloadStatus') {
    const items = await chrome.downloads.search({ id: (params || {}).id });
    respond(st, id, items[0] || null);
    return;
  }

  if (method.startsWith('Target.')) {
    respond(st, id, await handleTarget(st, method, params || {}));
    return;
  }

  if (!sessionId) throw new Error(`command ${method} without a session (tab)`);
  const tabId = st.tabBySession.get(sessionId);
  if (tabId === undefined) throw new Error(`session ${sessionId} is no longer active`);
  respond(st, id, await chrome.debugger.sendCommand({ tabId }, method, params || {}));
}

// ---------------------------------------------------------------------- group

async function adoptExistingGroup(st) {
  if (!st.session) return;
  const key = `group:${st.session}`;

  // 1) Try the group that already belonged to this session (survives a rename).
  try {
    const stored = (await chrome.storage.local.get(key))[key];
    if (stored !== undefined && stored !== null) {
      const g = await chrome.tabGroups.get(stored);
      if (g) {
        st.groupId = g.id;
        st.groupTitle = g.title || st.groupTitle;
        await syncGroupTabs(st);
        return;
      }
    }
  } catch {
    await chrome.storage.local.remove(key);
  }

  // The remembered group is gone (the user ungrouped or closed it): drop the
  // stale id so the next tab creates a fresh group instead of failing to join a
  // dead one.
  st.groupId = null;
  st.groupTitle = null;

  // No title fallback on purpose: a group belongs to a session (the stored key),
  // never to an agent name. Two sessions of the same agent (two "Opencode"
  // instances) must not adopt each other's group.
}

/** Re-associates the tabs of a group that already existed in the new session. */
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
    if (st.groupId !== null && st.groupId !== undefined) {
      try {
        await chrome.tabs.group({ tabIds: [tabId], groupId: st.groupId });
        ownerByTab.set(tabId, st);
        return;
      } catch {
        // The group is gone (the user ungrouped or closed it): forget the dead
        // id and create a fresh group below, instead of failing to join it
        // silently forever.
        st.groupId = null;
        st.groupTitle = null;
      }
    }
    const gid = await chrome.tabs.group({ tabIds: [tabId] });
    st.groupId = gid;
    if (!st.groupTitle) st.groupTitle = await nextTitleFor(st.agent);
    await chrome.tabGroups.update(gid, {
      title: st.groupTitle,
      color: groupColor(st.agent),
      collapsed: false,
    });
    await chrome.storage.local.set({ [`group:${st.session}`]: gid });
    ownerByTab.set(tabId, st);
  } catch {
    /* no group permission: the tab stays usable */
  }
}

/** Only the tabs of this group — that is the isolation between sessions. */
async function listGroupTabs(st) {
  if (st.groupId === null || st.groupId === undefined) return [];
  const tabs = await chrome.tabs.query({});
  return tabs.filter((t) => t.groupId === st.groupId);
}

// --------------------------------------------------------------------- Target

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
      // Activates the tab in the group without raising the window: focus only
      // if the user asks for it.
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
          /* already detached */
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
  if (Number.isNaN(tabId)) throw new Error(`invalid targetId: ${targetId}`);

  const sessionId = `t${tabId}`;
  if (st.tabBySession.has(sessionId)) return { sessionId };

  // Stuck attachment (a previous daemon died without releasing): detach and
  // try again.
  try {
    await chrome.debugger.attach({ tabId }, PROTOCOL);
  } catch (err) {
    if (!String(err).includes('Another debugger is already attached')) throw err;
    try {
      await chrome.debugger.detach({ tabId });
    } catch {
      /* it was not ours (DevTools open, for example) */
    }
    await chrome.debugger.attach({ tabId }, PROTOCOL);
  }
  st.tabBySession.set(sessionId, tabId);
  ownerByTab.set(tabId, st);
  return { sessionId };
}

// --------------------------------------------------------------------- events

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
  // A tab the page opens (target=_blank, window.open) can be born already in the
  // session's group, so onUpdated never reports a group change; claim it by its
  // group here. Without this the daemon only learns about it on a restart.
  let st = ownerByTab.get(tab.id);
  if (!st && tab.groupId !== undefined && tab.groupId !== chrome.tabGroups.TAB_GROUP_ID_NONE) {
    for (const s of conns.values()) {
      if (s.groupId !== null && s.groupId !== undefined && s.groupId === tab.groupId) {
        ownerByTab.set(tab.id, s);
        st = s;
        break;
      }
    }
  }
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

chrome.tabs.onUpdated.addListener(async (tabId, changeInfo, tab) => {
  const st = ownerByTab.get(tabId);

  // A group change is the grant/revoke gesture: dragging a tab into the group
  // gives the agent access; dragging it out takes it away.
  if (changeInfo.groupId !== undefined) {
    if (st && st.groupId !== null && st.groupId !== undefined && changeInfo.groupId !== st.groupId) {
      // Left the group: loses access and we release the debugger.
      st.tabBySession.delete(`t${tabId}`);
      ownerByTab.delete(tabId);
      try {
        await chrome.debugger.detach({ tabId });
      } catch {
        /* was not attached */
      }
      sendOn(st, { method: 'Target.targetDestroyed', params: { targetId: String(tabId) } });
      return;
    }
    if (!st) {
      // Entered the group of some connected session: it now sees the tab.
      for (const s of conns.values()) {
        if (s.groupId !== null && s.groupId !== undefined && s.groupId === changeInfo.groupId) {
          ownerByTab.set(tabId, s);
          sendOn(s, { method: 'Target.targetCreated', params: { targetInfo: toTargetInfo(s, tab) } });
          return;
        }
      }
    }
  }

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
        sessions.push({ session: st.session || '(waiting)', port: st.port });
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
          /* ignore */
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

// The service worker wakes up and reconnects on its own.
connectAll();
startHeartbeat();
setInterval(connectAll, RETRY_MS);
