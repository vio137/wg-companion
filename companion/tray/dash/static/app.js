'use strict';
const $=id=>document.getElementById(id);
const token=new URLSearchParams(location.hash.slice(1)).get('t')||'';
history.replaceState(null,'',location.pathname);
let busy=false;
async function api(path,mutate=false){
 if(!token)throw new Error('Open this panel from the tray icon to authorize local controls.');
 const r=await fetch('/api/'+path,{method:mutate?'POST':'GET',headers:{'X-WG-Token':token,...(mutate?{'Content-Type':'application/json'}:{})},credentials:'omit',cache:'no-store'});
 if(!r.ok)throw new Error(r.status===403?'Control session expired. Reopen from the tray icon.':await r.text());
 return r.json();
}
function bytes(n){if(n==null)return '-';if(n<1024)return n+' B';for(const [d,u]of [[2**30,'GB'],[2**20,'MB'],[1024,'KB']])if(n>=d)return(n/d).toFixed(1)+' '+u;}
function render(data){const s=data.stats||{};const down=data.svc_down;document.body.dataset.state=down?'error':s.state||'disconnected';
 const states={connected:['Connected','Chrome through\nWireGuard.','The peer completed a real handshake. Enable browser routing in Chrome to use this tunnel.'],ready:['Waiting for peer','Tunnel ready.\nHandshake pending.','The device is ready. Send browser traffic to reach your peer; no handshake yet.'],connecting:['Connecting','Opening your\nlocal tunnel.','The service is starting a userspace WireGuard device.'],disconnected:['Tunnel off','Your tunnel.\nYour control.','A local WireGuard tunnel for your browser. Other apps keep their own connection.'],error:['Needs attention','Check your\nconnection.','The tunnel could not be reached. Review the message below.']};
 const [label,head,desc]=down?['Service unavailable','Start the\nbackground service.','The service is not responding. Installation or administrator help may be needed.']:states[s.state]||states.disconnected;
 $('status').textContent=label;$('headline').textContent=head;$('description').textContent=desc;
 $('rx').textContent=bytes(s.rx_bytes);$('tx').textContent=bytes(s.tx_bytes);$('handshake').textContent=s.last_handshake_unix?Math.max(0,Math.floor(Date.now()/1000-s.last_handshake_unix))+'s ago':'-';$('endpoint').textContent=s.endpoint||'No peer selected';
 const active=['connected','ready','connecting'].includes(s.state);
 $('connect').disabled=busy||down||active||!$('profile').value;$('connect').replaceChildren(document.createTextNode(down?'Service unavailable':!$('profile').value?'Add profile in Chrome':active?'Tunnel active':'Connect tunnel'));$('disconnect').disabled=busy||down||!active;$('profile').disabled=busy||active;
 if(s.last_error)$('message').textContent=s.last_error;
}
async function refresh(){try{render(await api('status'));}catch(e){$('status').textContent='Control unavailable';$('message').textContent=e.message;$('connect').disabled=true;$('disconnect').disabled=true;}}
async function act(path){busy=true;$('connect').disabled=true;$('disconnect').disabled=true;$('message').textContent='';try{await api(path,true);}catch(e){$('message').textContent=e.message;}finally{busy=false;await refresh();}}
$('connect').onclick=()=>act('connect?name='+encodeURIComponent($('profile').value));$('disconnect').onclick=()=>act('disconnect');$('profile').onchange=refresh;
const mq=matchMedia('(prefers-reduced-motion: reduce)');const saved=localStorage.getItem('wg-reduce-motion');$('motion').checked=saved===null?mq.matches:saved==='true';function motion(){document.body.classList.toggle('reduce-motion',$('motion').checked);}motion();$('motion').onchange=()=>{motion();localStorage.setItem('wg-reduce-motion',$('motion').checked);};
(async()=>{try{const list=await api('configs');$('profile').replaceChildren();for(const c of list)$('profile').add(new Option(c.name,c.name));if(!list.length)$('profile').add(new Option('Add a profile in Chrome',''));}catch(e){$('message').textContent=e.message;}await refresh();})();
// Poll only while visible, never animate an idle state or synthesize metrics.
setInterval(()=>{if(!document.hidden&&!busy)refresh();},3000);document.addEventListener('visibilitychange',()=>{if(!document.hidden)refresh();});
