const {chromium}=require('playwright');
const {spawn,execFileSync}=require('child_process');
const fs=require('fs'),path=require('path'),assert=require('assert'), http=require('http');
let root=path.resolve(__dirname,'..');if(!fs.existsSync(root+'/extension'))root=path.resolve(root,'..');const companion=fs.existsSync(root+'/companion')?root+'/companion':root+'/source/companion';const delay=ms=>new Promise(r=>setTimeout(r,ms));
(async()=>{
 for(const p of ['/tmp/chrome-fixture-ready','/tmp/chrome-fixture-stop'])fs.rmSync(p,{force:true});
 execFileSync('go',['build','-o','/tmp/wg-companion','.'],{cwd:companion});
 const fixture=spawn('go',['test','-run','^TestChromeFixture$','-v','-count=1'],{cwd:companion,env:{...process.env,WG_CHROME_FIXTURE:'1'},stdio:['ignore','pipe','pipe']});
 let ctx;
 try {
 for(let i=0;i<100&&!fs.existsSync('/tmp/chrome-fixture-ready');i++)await delay(100);
 assert(fs.existsSync('/tmp/chrome-fixture-ready'),'fixture ready');
 const configDir=fs.mkdtempSync('/tmp/wg-host-config-');const userDir=fs.mkdtempSync('/tmp/wg-chrome-e2e-');fs.mkdirSync(userDir+'/NativeMessagingHosts',{recursive:true});
 const manifest=JSON.parse(fs.readFileSync(root+'/extension/manifest.json'));const id=require('crypto').createHash('sha256').update(Buffer.from(manifest.key,'base64')).digest('hex').slice(0,32).replace(/[0-9a-f]/g,c=>'abcdefghijklmnop'[parseInt(c,16)]);
 fs.writeFileSync(userDir+'/NativeMessagingHosts/com.wgcompanion.tunnel.json',JSON.stringify({name:'com.wgcompanion.tunnel',description:'Test host',path:'/tmp/wg-companion',type:'stdio',allowed_origins:[`chrome-extension://${id}/`]}));
 ctx=await chromium.launchPersistentContext(userDir,{headless:true,channel:'chromium',env:{...process.env,XDG_CONFIG_HOME:configDir},viewport:{width:392,height:600},args:[`--disable-extensions-except=${root}/extension`,`--load-extension=${root}/extension`,'--no-sandbox']});
 const sw=ctx.serviceWorkers()[0]||await ctx.waitForEvent('serviceworker');
 const page=await ctx.newPage();await page.goto(`chrome-extension://${id}/popup.html`);
 await page.getByText('Companion available',{exact:true}).waitFor();
 await page.screenshot({path:'/downloads/wg-off.png',fullPage:true});
 // exercise real popup file import, no mock values
 await page.locator('#add').click(); await page.locator('#name').fill('Test-peer-'+process.pid);
 await page.locator('#file').setInputFiles('/tmp/chrome-fixture.conf');await page.locator('#importBtn').click();
 await page.locator('#profile').selectOption('Test-peer-'+process.pid);
 assert.equal(await page.locator('#toggle').isEnabled(),true);
 await page.locator('#toggle').click();await page.getByText('Waiting for peer',{exact:true}).waitFor();
 const browser=await ctx.newPage();await browser.goto('http://peer.test:8080/',{timeout:15000});
 assert((await browser.textContent('body')).includes('hello-from-peer remote=10.9.0.2'));
 console.log('PASS Chrome HTTP via encrypted WireGuard + tunnel DNS');
 await page.getByText('Connected',{exact:true}).waitFor({timeout:10000});
 await page.screenshot({path:'/downloads/wg-connected.png',fullPage:true});
 const proxy=await sw.evaluate(()=>chrome.proxy.settings.get({incognito:false}));assert.equal(proxy.value.mode,'fixed_servers');assert(!JSON.stringify(proxy.value).includes('DIRECT'));
 const rtc=await sw.evaluate(()=>chrome.privacy.network.webRTCIPHandlingPolicy.get({}));assert.equal(rtc.value,'disable_non_proxied_udp');
 console.log('PASS Chrome proxy rule + WebRTC privacy setting');
 let canary=0;const server=http.createServer((req,res)=>{canary++;res.end('LEAK')});await new Promise(r=>server.listen(0,'127.0.0.1',r));
 const canaryURL='http://127.0.0.1:'+server.address().port;
 const canaryPage=await ctx.newPage();try{await canaryPage.goto(canaryURL,{timeout:2000})}catch(_){}
 assert.equal(canary,0,'Chrome loopback bypass must be disabled');await canaryPage.close();server.close();
 console.log('PASS Chrome host-loopback leak canary');
 // Close popup: native connection is owned by worker, not popup.
 await page.close();await browser.reload();assert((await browser.textContent('body')).includes('hello-from-peer'));
 console.log('PASS popup closure keeps tunnel alive');
 // Kill only this test companion process. Retained proxy must block browser.
 const pidText=execFileSync('pgrep',['-f','^/tmp/wg-companion chrome-extension://']).toString().trim();
 const pids=pidText.split('\n').map(Number).filter(Boolean);assert(pids.length);for(const pid of pids)process.kill(pid,'SIGKILL');
 await delay(500);
 let failed=false;try{await browser.goto('http://peer.test:8080/crash',{timeout:5000})}catch(e){failed=true;console.log('blocked error',e.message.split('\n')[0])}assert(failed,'crash must not fall back');
 console.log('PASS companion crash fails closed for proxied Chrome HTTP');
 const panel=await ctx.newPage();await panel.goto(`chrome-extension://${id}/popup.html`);await panel.getByText('Companion stopped',{exact:true}).waitFor();await panel.screenshot({path:'/downloads/wg-blocked.png',fullPage:true});
 await panel.locator('#toggle').click();await panel.getByText('Off',{exact:true}).waitFor();
 const restored=await sw.evaluate(()=>chrome.proxy.settings.get({incognito:false}));assert.notEqual(restored.value.mode,'fixed_servers');
 console.log('PASS explicit turn-off clears extension proxy');
 await panel.locator('#toggle').click();await panel.getByText('Waiting for peer',{exact:true}).waitFor();await browser.goto('http://peer.test:8080/reconnect',{timeout:15000});
 assert((await browser.textContent('body')).includes('hello-from-peer'));console.log('PASS reconnect after native crash');
 await panel.locator('#toggle').click();
 await panel.locator('#add').click();await panel.screenshot({path:'/downloads/wg-add.png',fullPage:true});
 console.log('ALL CHROME INTEGRATION TESTS PASSED');
 }finally {if(ctx)await ctx.close();fs.writeFileSync('/tmp/chrome-fixture-stop','stop');await new Promise(r=>{fixture.on('exit',r);setTimeout(r,2500)});fixture.kill();}
})().catch(e=>{console.error(e);process.exit(1)});
