// Isolated UI integration checks using invented API fixtures; no device access.
const fs=require('node:fs');
const path=require('node:path');
const assert=require('node:assert/strict');
const http=require('node:http');
const {chromium}=require('playwright');
const folder=path.resolve(process.argv[2]||'deliverables/验证记录/model-delivery/ui');
fs.mkdirSync(folder,{recursive:true});
const css=fs.readFileSync('portainer/app/edgestack/workspace.css','utf8');
const js=fs.readFileSync('portainer/app/edgestack/delivery-ui.js','utf8');
const fixture=`
window.calls=[];window.fixtureError=false;window.requests=[];
const devices=[{id:'one',name:'客厅设备',tasks:[{id:'old-task',name:'原有检测'}]},{id:'two',name:'书房设备',tasks:[]}];
window.fetch=async(url,options={})=>{
 const p=new URL(url).pathname.split('/api/edgestack/')[1],method=options.method||'GET',body=JSON.parse(options.body||'null');calls.push({p,method,body,headers:options.headers});
 let data={items:[]};
 if(window.fixtureError)return {ok:false,status:503,json:async()=>({errorCode:'delivery_not_configured'})};
 if(p==='model-requests'&&method==='POST'){data={id:'request-1',modelName:body.modelName,status:'submitted',messages:[{message:body.message,createdAt:new Date().toISOString()}]};requests.push(data);}
 if(p==='model-requests'&&method==='GET')data={items:requests};
 if(p==='model-requests/request-1/messages'){requests[0].messages.push({message:body.message,createdAt:new Date().toISOString()});data=requests[0];}
 if(p==='delivery/catalog')data={items:[{packageId:'pkg-1',recipeId:'recipe-1',name:'YOLOv8n',version:'1',image:'registry.invalid/yolo@sha256:'+ 'a'.repeat(64)}]};
 if(p==='deployment-plans')data={id:'plan-1',expiresAt:new Date(Date.now()+300000).toISOString(),targets:body.targets.map(t=>({...t,preview:{effects:['保留数据卷。']}}))};
 if(p==='deployment-plans/plan-1/confirm')data={id:'rollout-1',status:'running'};
 return {ok:true,status:200,json:async()=>JSON.parse(JSON.stringify(data))};
};
window.openUI=async()=>{window.delivery ||= await EdgeStackDeliveryUI.mount(document.querySelector('#host'),{devices:()=>devices,selectedDevice:()=> 'one'});delivery.open();};`;
const html=`<!doctype html><html lang="zh-CN"><head><meta charset="utf-8"><style>${css}</style></head><body><button id="open" onclick="openUI()">部署模型</button><div id="host"></div><script>${js}</script><script>${fixture}</script></body></html>`;
const server=http.createServer((req,res)=>{res.setHeader('Content-Type','text/html;charset=utf-8');res.end(html);});
(async()=>{
 await new Promise(resolve=>server.listen(0,'127.0.0.1',resolve));
 const browser=await chromium.launch({headless:true,channel:'chrome'});
 const page=await browser.newPage({viewport:{width:1440,height:1000}}),errors=[];
 page.on('pageerror',error=>errors.push(error.message));
 try{
  await page.goto('http://127.0.0.1:'+server.address().port);await page.locator('#open').click();
  const dialog=page.getByRole('dialog',{name:'模型部署助手'});await dialog.waitFor();
  await dialog.getByLabel('模型名称',{exact:true}).fill('<img src=x onerror=alert(1)>');
  await dialog.getByLabel('用途、模型来源和性能目标').fill('检测前台来访');
  await dialog.getByRole('button',{name:'提交模型申请'}).click();
  await dialog.getByRole('button',{name:'查看并补充说明'}).click();
  assert.equal(await dialog.locator('img').count(),0);
  await dialog.getByLabel('补充说明',{exact:true}).fill('希望保留原数据');await dialog.getByRole('button',{name:'发送补充说明'}).click();
  await dialog.getByText('希望保留原数据',{exact:true}).waitFor();
  await dialog.getByRole('button',{name:'部署模型',exact:true}).click();
  await dialog.getByLabel('业务任务').selectOption('old-task');
  await dialog.getByRole('button',{name:'加入部署目标'}).click();
  await dialog.getByLabel('目标设备').selectOption('two');
  await dialog.getByLabel('新任务名称').fill('书房检测');await dialog.getByRole('button',{name:'加入部署目标'}).click();
  await dialog.getByLabel('已发布模型版本').selectOption('pkg-1|recipe-1');
  await dialog.getByRole('button',{name:'生成部署预览'}).click();await dialog.getByRole('heading',{name:'部署影响预览'}).waitFor();
  let calls=await page.evaluate(()=>window.calls);const plan=calls.find(c=>c.p==='deployment-plans');assert.equal(plan.body.targets.length,2);assert.equal(plan.body.targets[0].taskId,'old-task');assert.equal(plan.body.targets[1].taskName,'书房检测');assert.equal(plan.body.packageId,'pkg-1');assert.equal(calls.filter(c=>c.p.endsWith('/confirm')).length,0);
  await page.screenshot({path:path.join(folder,'deployment-preview-desktop.png'),fullPage:true});
  await dialog.getByRole('button',{name:'确认部署',exact:true}).click();await dialog.getByRole('heading',{name:'端侧部署',exact:true}).waitFor();
  calls=await page.evaluate(()=>window.calls);assert.equal(calls.filter(c=>c.p.endsWith('/confirm')).length,1);assert(calls.find(c=>c.p==='model-requests'&&c.method==='POST').headers['Idempotency-Key']);
  await page.keyboard.press('Escape');await page.locator('#open').click();await dialog.getByRole('button',{name:'我的申请',exact:true}).click();await dialog.getByRole('button',{name:'查看并补充说明'}).waitFor();
  await page.setViewportSize({width:390,height:844});await page.screenshot({path:path.join(folder,'requests-mobile.png'),fullPage:true});assert(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth));
  await page.evaluate(()=>window.fixtureError=true);await dialog.getByRole('button',{name:'镜像版本',exact:true}).click();await dialog.getByRole('alert').getByText('管理员尚未配置模型交付服务。').waitFor();
  assert.deepEqual(errors,[]);
  const report={fixture:true,passed:['申请和补充说明','不可信文本转义','多设备预览与显式确认','请求幂等标识','重新打开恢复记录','移动端无横向溢出','服务未配置错误状态'],errors};
  fs.writeFileSync(path.join(folder,'report.json'),JSON.stringify(report,null,2)+'\n');console.log(JSON.stringify(report));
 }finally{await browser.close();server.close();}
})().catch(error=>{console.error(error);server.close();process.exitCode=1;});
