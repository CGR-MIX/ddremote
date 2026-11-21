// Regression checks for the renderer's asynchronous logout behavior.
// The bridge is a fixture; this does not test real login, remote control or cloud deletion.
const assert = require('node:assert/strict');
const path = require('node:path');
const {pathToFileURL} = require('node:url');
const {chromium} = require('playwright');

(async () => {
  const browser = await chromium.launch({headless:true, ...(process.env.DD_TEST_BROWSER ? {executablePath:process.env.DD_TEST_BROWSER} : {})});
  try {
    const page = await browser.newPage({viewport:{width:1050,height:800}});
    page.setDefaultTimeout(5000);
    await page.addInitScript(() => {
      window.qa = {loggedIn:true, pending:null, failLogout:false};
      window.ddInvoke = raw => {
        const req = JSON.parse(raw);
        const network = {running:qa.loggedIn, state:qa.loggedIn?'Running':'Stopped', account:qa.loggedIn?{id:'fixture',name:'界面验证账号'}:{}, peers:[]};
        let data = {};
        if (req.action === 'status') data = {version:'0.2.0-preview',deviceName:'界面验证电脑',hostname:'TEST',addresses:[],host:{running:false,ready:false},network,transfer:{running:false},pairings:[],logTail:[]};
        if (req.action === 'accountDevices') data = {account:network.account,state:network.state,devices:qa.loggedIn?[{id:'peer',name:'界面验证设备',platform:'windows',ready:true,online:true}]:[]};
        if (req.action === 'accountConnect') { qa.pending=req.id; return; }
        if (req.action === 'accountLogout') {
          qa.loggedIn=false;
          if (qa.failLogout) {
            queueMicrotask(()=>ddResolve({id:req.id,ok:false,error:'已断开；未确认云端移除'}));
            return;
          }
          data = {message:'已退出账号并移除此设备'};
        }
        queueMicrotask(()=>ddResolve({id:req.id,ok:true,data}));
      };
    });
    await page.goto(pathToFileURL(path.join(__dirname,'../apps/windows/web/index.html')).href);
    await page.locator('#accountDeviceList button').click();
    assert.equal(await page.locator('#accountLogout').isEnabled(),true);
    await page.locator('#accountLogout').click();
    await page.evaluate(()=>ddResolve({id:qa.pending,ok:true,data:{message:'迟到的连接成功'}}));
    assert.equal(await page.locator('#accountResult').innerText(),'已退出账号并移除此设备');
    assert.equal(await page.locator('#accountDeviceList').isVisible(),false);
    assert.equal(await page.locator('#accountLogin').isEnabled(),true);
    console.log('PASS logout remains available during connect; delayed result cannot restore account UI');

    await page.reload();
    await page.locator('#accountLogout').waitFor({state:'visible'});
    await page.locator('[data-page="files"]').click();
    await page.locator('#clipboardText').fill('旧会话测试文字');
    await page.locator('[data-page="devices"]').click();
    await page.evaluate(()=>{qa.failLogout=true;});
    await page.locator('#accountLogout').click();
    assert.equal(await page.locator('#accountResult').innerText(),'已断开；未确认云端移除');
    assert.equal(await page.locator('#accountDeviceList').isVisible(),false);
    assert.equal(await page.locator('#clipboardText').inputValue(),'');
    assert.equal(await page.locator('#accountLogin').isEnabled(),true);
    console.log('PASS failed cloud confirmation preserves the error while clearing prior UI session');

    await page.setViewportSize({width:760,height:650});
    assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),true);
    console.log('PASS 760px account view has no horizontal overflow');
  } finally { await browser.close(); }
})().catch(error=>{console.error(error);process.exitCode=1;});
