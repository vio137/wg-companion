// WireGuard Companion. No remote control server: Chrome native messaging only.
const HOST = 'com.wgcompanion.tunnel';
const SOCKS_PORT = 18080;
let port = null, nextId = 1, poll = null;
let iconTimers = [], reduceMotion = true;
chrome.storage.local.get('reduceMotion').then(v=>{reduceMotion=v.reduceMotion!==false;});
chrome.storage.onChanged.addListener((changes,area)=>{if(area==='local'&&changes.reduceMotion){reduceMotion=changes.reduceMotion.newValue!==false;if(reduceMotion){iconTimers.forEach(clearTimeout);iconTimers=[];}}});
const pending = new Map();
let state = { phase: 'off', wanted: false, config: null, error: null, stats: null, hostOk: null };

function setState(patch) {
  state = { ...state, ...patch };
  const bad = ['blocked', 'error'].includes(state.phase);
  chrome.action.setBadgeBackgroundColor({ color: bad ? '#ef8b78' : '#3ddc97' });
  chrome.action.setBadgeText({ text: bad ? '!' : '' });
  const icon = ({on:'on',connecting:'busy0',ready:'ready',error:'error',blocked:'blocked'})[state.phase] || 'off';
  chrome.action.setIcon({ path: { 16: `icons/${icon}16.png`, 32: `icons/${icon}32.png` } });
  // Bounded icon transition. No idle animation or high-frequency alarms.
  if (state.phase === 'connecting' && !reduceMotion && !iconTimers.length) {
    for (let i=1;i<=6;i++) iconTimers.push(setTimeout(()=>{
      if(state.phase==='connecting') chrome.action.setIcon({path:{16:`icons/busy${i%4}16.png`,32:`icons/busy${i%4}32.png`}});
      if(i===6) iconTimers=[];
    },i*350));
  } else if(state.phase!=='connecting') { iconTimers.forEach(clearTimeout); iconTimers=[]; }
  chrome.action.setTitle({ title: `WireGuard Companion: ${state.phase}` });
  chrome.runtime.sendMessage({ type: 'state', state }).catch(() => {});
}

function ensurePort() {
  if (port) return port;
  const p = chrome.runtime.connectNative(HOST);
  port = p;
  p.onMessage.addListener(m => {
    const req = pending.get(m.id);
    if (req) { pending.delete(m.id); m.ok ? req.resolve(m.data) : req.reject(new Error(m.error || 'Companion command failed')); }
  });
  p.onDisconnect.addListener(() => {
    const error = chrome.runtime.lastError?.message || 'Companion closed';
    if (port !== p) return;
    port = null;
    for (const req of pending.values()) req.reject(new Error(error));
    pending.clear();
    stopPolling();
    if (state.wanted) setState({ hostOk: false, phase: 'blocked', error: 'Companion stopped. The proxy is still enabled. Reconnect or turn off to browse directly.' });
    else setState({ hostOk: false });
  });
  return p;
}

function rpc(cmd, args = {}) {
  return new Promise((resolve, reject) => {
    let p;
    try { p = ensurePort(); } catch (e) { reject(e); return; }
    const id = nextId++;
    const timer = setTimeout(() => { pending.delete(id); reject(new Error('Companion timed out')); }, cmd === 'register_warp' ? 110000 : 20000);
    pending.set(id, {
      resolve: v => { clearTimeout(timer); resolve(v); },
      reject: e => { clearTimeout(timer); reject(e); }
    });
    try { p.postMessage({ id, cmd, ...args }); }
    catch (e) { clearTimeout(timer); pending.delete(id); reject(e); }
  });
}

function callbackOp(fn) {
  return new Promise((resolve, reject) => fn(() => {
    const error = chrome.runtime.lastError;
    error ? reject(new Error(error.message)) : resolve();
  }));
}

async function setProxy(on) {
  if (!on) return callbackOp(cb => chrome.proxy.settings.clear({ scope: 'regular' }, cb));
  const setting = await chrome.proxy.settings.get({ incognito: false });
  if (!['controllable_by_this_extension', 'controlled_by_this_extension'].includes(setting.levelOfControl))
    throw new Error('Chrome proxy is managed by a policy or another extension. No tunnel was enabled.');
  await callbackOp(cb => chrome.proxy.settings.set({ scope: 'regular', value: {
    mode: 'fixed_servers', rules: { singleProxy: { scheme: 'socks5', host: '127.0.0.1', port: SOCKS_PORT }, bypassList: ['<-loopback>'] }
  } }, cb));
}

async function privacy(on) {
  const controls = [[chrome.privacy.network.webRTCIPHandlingPolicy, 'disable_non_proxied_udp'], [chrome.privacy.network.networkPredictionEnabled, false]];
  for (const [api, value] of controls) {
    if (on) {
      const s = await api.get({});
      if (!['controllable_by_this_extension', 'controlled_by_this_extension'].includes(s.levelOfControl))
        throw new Error('A Chrome policy prevents the required privacy setting. Traffic remains blocked.');
      await api.set({ value, scope: 'regular' });
    } else await api.clear({ scope: 'regular' });
  }
}

async function connect(name) {
  if (state.phase === 'connecting') return;
  await chrome.storage.local.set({ wanted: true, config: name || null });
  setState({ wanted: true, phase: 'connecting', error: null, stats: null, config: name || null });
  try {
    // Fail closed before creating a tunnel. No DIRECT fallback in the rule.
    await setProxy(true);
    await privacy(true);
    let st = await rpc('status');
    if (!['connected', 'ready'].includes(st.state)) st = await rpc('connect', name ? { name } : {});
    setState({ phase: st.state === 'connected' ? 'on' : 'ready', stats: st, config: st.config, hostOk: true });
    startPolling();
  } catch (e) { setState({ phase: 'error', error: e.message }); }
}

async function disconnect() {
  // Explicitly opting out restores Chrome's previous underlying settings.
  stopPolling();
  await chrome.storage.local.set({ wanted: false });
  setState({ wanted: false });
  try { if (port) await rpc('disconnect'); } catch (_) {}
  await setProxy(false);
  await privacy(false);
  setState({ phase: 'off', stats: null, error: null });
}

function startPolling() {
  stopPolling();
  poll = setInterval(async () => {
    if (!state.wanted) return;
    try {
      const st = await rpc('stats');
      setState({ phase: st.state === 'connected' ? 'on' : st.state === 'ready' ? 'ready' : 'blocked', stats: st });
    } catch (_) { /* Port disconnect reports this and retains the proxy. */ }
  }, 2500);
}
function stopPolling() { clearInterval(poll); poll = null; }

let queue = Promise.resolve();
function serialize(f) {
  const result = queue.then(f);
  queue = result.catch(() => {});
  return result;
}

// Executed once whenever this worker starts, including a browser restart.
const boot = (async () => {
  const s = await chrome.storage.local.get(['wanted', 'config']);
  state.config = s.config || null;
  if (s.wanted) await connect(s.config);
  else setState({ phase: 'off' });
})();

chrome.runtime.onMessage.addListener((msg, sender, send) => {
  if (sender.id !== chrome.runtime.id || msg.type === 'state') return false;
  (async () => {
    await boot;
    try {
      let data;
      switch (msg.type) {
        case 'get': send({ state }); return;
        case 'ping': data = await rpc('ping'); setState({ hostOk: true, error: state.wanted ? state.error : null }); break;
        case 'list': data = await rpc('list_configs'); setState({ hostOk: true }); break;
        case 'import': data = await serialize(() => rpc('import_config', { name: msg.name, contents: msg.contents })); break;
        case 'warp': data = await serialize(() => rpc('register_warp', { accept_terms: msg.acceptTerms })); break;
        case 'remove': data = await serialize(() => rpc('remove_config', { name: msg.name })); break;
        case 'connect': await serialize(() => connect(msg.name)); break;
        case 'disconnect': await serialize(disconnect); break;
        default: return;
      }
      send({ ok: true, data });
    } catch (e) { send({ ok: false, error: e.message }); }
  })();
  return true;
});
