const {chromium}=require('playwright');
const fs=require('fs'), path=require('path');
const root=path.resolve(__dirname,'..');
(async()=>{
 const context=await chromium.launchPersistentContext('/tmp/wg-chrome-test', {headless:true, channel:'chromium',args:[`--disable-extensions-except=${root}/extension`,`--load-extension=${root}/extension`,'--no-sandbox']});
 let sw=context.serviceWorkers()[0]||await context.waitForEvent('serviceworker');
 console.log('SW',sw.url());
 const id=sw.url().split('/')[2];
 const page=await context.newPage();await page.goto(`chrome-extension://${id}/popup.html`);
 await page.waitForTimeout(1200);
 console.log('BODY',await page.locator('body').innerText());
 await page.screenshot({path:'/downloads/wg-popup.png',fullPage:true});
 const st=await sw.evaluate(()=>chrome.runtime.id);console.log('ID',st);
 await context.close();
})().catch(e=>{console.error(e);process.exit(1)});
