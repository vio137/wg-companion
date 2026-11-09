const motionQuery = matchMedia('(prefers-reduced-motion: reduce)');
function syncMotion(){chrome.storage.local.set({reduceMotion:motionQuery.matches});}
syncMotion();motionQuery.addEventListener('change',syncMotion);
const $ = id => document.getElementById(id);
let current = {}, configs = [];
async function call(type, args = {}) {
  const r = await chrome.runtime.sendMessage({ type, ...args });
  if (r?.ok === false) throw new Error(r.error);
  return r;
}
function message(s) { $('message').textContent = s || ''; }
function bytes(n) {
  if (n === undefined) return '-';
  if (n < 1024) return n + ' B';
  for (const [div, unit] of [[1024 ** 3, 'GB'], [1024 ** 2, 'MB'], [1024, 'KB']])
    if (n >= div) return (n / div).toFixed(1) + ' ' + unit;
}
function render(s) {
  current = s;
  document.body.dataset.phase = s.phase;
  const titles = {
    off: ['Off', 'Your browser.\nYour tunnel.', 'Route Chrome through WireGuard. Other apps keep their own connection.'],
    connecting: ['Starting', 'Opening the\ntunnel.', 'Chrome is set to the local proxy while WireGuard starts.'],
    ready: ['Waiting for peer', 'Tunnel ready.\nHandshake pending.', 'Open a website to send traffic. Connected status appears only after a real WireGuard handshake.'],
    on: ['Connected', 'Chrome through\nWireGuard.', 'The WireGuard peer has completed a handshake. Browser proxy is enabled.'],
    blocked: ['Companion stopped', 'Connection\nblocked.', 'Chrome still points to the local proxy. Reconnect, or turn off to browse directly.'],
    error: ['Needs attention', 'Check your\nconnection.', 'Review the error below. Turn off to restore your previous Chrome settings.']
  };
  const [label, head, desc] = titles[s.phase] || titles.off;
  $('status').textContent = label;
  $('headline').textContent = head;
  $('headline').style.whiteSpace = 'pre-line';
  $('description').textContent = desc;
  $('toggle').querySelector('span').textContent = s.wanted ? 'Turn off' : 'Connect';
  $('toggle').disabled = s.phase === 'connecting' || (!s.wanted && !configs.length);
  $('profile').disabled = !!s.wanted;
  if (s.config && configs.some(c => c.name === s.config)) $('profile').value = s.config;
  message(s.error);
  for (const row of document.querySelectorAll('.profile-row')) row.classList.toggle('selected', row.dataset.name === s.config && s.wanted);
  $('host').textContent = s.phase === 'blocked' ? 'Proxy retained · traffic blocked' : s.hostOk ? 'Companion available' : s.hostOk === false ? 'Companion not found' : 'Companion not checked';
  const st = s.stats;
  $('rx').textContent = bytes(st?.rx_bytes);
  $('tx').textContent = bytes(st?.tx_bytes);
  $('handshake').textContent = st?.last_handshake_unix ? Math.max(0, Math.floor(Date.now() / 1000 - st.last_handshake_unix)) + 's ago' : '-';
}
async function loadProfiles() {
  try {
    const r = await call('list');
    configs = r.data.configs;
    const select = $('profile'), previous = select.value;
    select.replaceChildren();
    if (!configs.length) select.add(new Option('No profile yet', ''));
    for (const c of configs) select.add(new Option(c.name, c.name));
    if (configs.some(c => c.name === previous)) select.value = previous;
    $('profileList').replaceChildren();
    if (!configs.length) {
      const p = document.createElement('p'); p.className = 'muted'; p.textContent = 'Add a WARP or WireGuard profile'; $('profileList').append(p);
    }
    for (const c of configs) {
      const row = document.createElement('div'); row.dataset.name = c.name; row.className = 'profile-row' + (c.name === current.config ? ' selected' : '');
      const dot = document.createElement('span'); dot.className = 'indicator';
      const meta = document.createElement('div'); meta.className = 'profile-meta';
      const name = document.createElement('b'); name.textContent = c.name;
      const endpoint = document.createElement('small'); endpoint.textContent = c.endpoint;
      meta.append(name, endpoint);
      const del = document.createElement('button'); del.className = 'delete'; del.textContent = '×'; del.title = 'Remove ' + c.name; del.setAttribute('aria-label', 'Remove ' + c.name);
      del.onclick = async () => {
        if (!confirm('Remove profile ' + c.name + '?')) return;
        try { await call('remove', { name: c.name }); await loadProfiles(); } catch (e) { message(e.message); }
      };
      row.append(dot, meta, del); $('profileList').append(row);
    }
    render(current);
  } catch (e) {
    configs = [];
    $('host').textContent = 'Companion not found';
    $('profileList').replaceChildren();
    const p = document.createElement('p'); p.className = 'muted'; p.textContent = 'Install the companion and start its service, then reopen this panel'; $('profileList').append(p);
    message(e.message);
  }
}
$('toggle').onclick = async () => {
  $('toggle').disabled = true;
  try { await call(current.wanted ? 'disconnect' : 'connect', { name: $('profile').value }); }
  catch (e) { message(e.message); }
  render((await call('get')).state);
};
$('help').onclick = () => { $('setup').open = !$('setup').open; $('setup').scrollIntoView({ block: 'nearest' }); };
$('add').onclick = () => $('addDialog').showModal();
$('closeDialog').onclick = () => $('addDialog').close();
$('terms').onchange = () => { $('warp').disabled = !$('terms').checked; };
$('file').onchange = () => {
  const f = $('file').files[0];
  if (f && !$('name').value) $('name').value = f.name.replace(/\.[^.]+$/, '').replace(/[^A-Za-z0-9_.-]/g, '-').slice(0, 64);
};
$('importForm').onsubmit = async e => {
  e.preventDefault(); $('dialogMessage').textContent = ''; $('importBtn').disabled = true;
  try {
    const f = $('file').files[0];
    if (!f || f.size > 65536) throw new Error('Choose a .conf file smaller than 64 KB');
    await call('import', { name: $('name').value, contents: await f.text() });
    $('file').value = ''; $('name').value = '';
    $('addDialog').close(); await loadProfiles();
  } catch (e) { $('dialogMessage').textContent = e.message; }
  finally { $('importBtn').disabled = false; }
};
$('warp').onclick = async () => {
  if (!$('terms').checked) return;
  $('warp').disabled = true; $('importBtn').disabled = true; $('dialogMessage').textContent = 'Registering with Cloudflare. This can take up to 90 seconds.';
  try {
    await call('warp', { acceptTerms: true });
    $('dialogMessage').textContent = ''; $('terms').checked = false; $('addDialog').close(); await loadProfiles();
  } catch (e) { $('dialogMessage').textContent = e.message; }
  finally { $('warp').disabled = !$('terms').checked; $('importBtn').disabled = false; }
};
chrome.runtime.onMessage.addListener(m => { if (m.type === 'state') render(m.state); });
(async () => { render((await call('get')).state); await loadProfiles(); })();
