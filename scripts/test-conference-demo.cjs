// Exercises the self-contained file in a fresh, offline browser profile.
const { chromium } = require('playwright');
const fs = require('node:fs');
const path = require('node:path');
const { pathToFileURL } = require('node:url');
const assert = require('node:assert/strict');

(async () => {
  const file = path.resolve(process.argv[2]);
  const browser = await chromium.launch({ headless: true, channel: process.env.EDGESTACK_TEST_BROWSER || 'chrome' });
  const page = await browser.newPage({ viewport: { width: 1440, height: 1000 } });
  const errors = [], consoleErrors = [], external = [];
  page.on('pageerror', (error) => errors.push(error.message));
  page.on('console', (message) => { if (message.type() === 'error') consoleErrors.push(message.text().slice(0, 600)); });
  page.on('request', (r) => { if (/^https?:/.test(r.url())) external.push(r.url()); });
  const output = path.resolve('.local/conference');
  fs.mkdirSync(output, { recursive: true });
  try {
    await page.goto(pathToFileURL(file).href, { waitUntil: 'load', timeout: 30000 });
    await page.waitForTimeout(3000);
    const body = await page.locator('body').innerText();
    const requests = await page.evaluate(() => window.EdgeStackDemo?.requests || []);
    await page.screenshot({ path: path.join(output, 'initial.png'), fullPage: true });
    fs.writeFileSync(path.join(output, 'boot.json'), JSON.stringify({ errors, consoleErrors, external, body, requests }, null, 2));
    console.log(JSON.stringify({ boot: 'loaded', errors, consoleErrors, external, request_count: requests.length }, null, 2));
    assert(body.includes('我的空间') && body.includes('模拟数据'), 'Expected the explicitly labeled user workspace demo');
    assert(body.includes('客厅的时光盒'), 'Expected the invented business device fixture');
    assert.equal(errors.length, 0, 'No uncaught JavaScript errors');
    assert.equal(external.length, 0, 'No external HTTP requests');
    if(process.argv.includes('--release6-only')){
      const workspace=page.locator('[data-edgestack-workspace]');const initial=await page.evaluate(()=>window.EdgeStackDemo.getState());assert.equal(initial.demoBusiness.schema,2);assert(initial.demoBusiness.events.every(e=>['voice.user_utterance','voice.assistant_reply','voice.playback_state','camera.observation'].includes(e.type)));assert.equal(initial.demoBusiness.devices[0].tasks[0].state,'registered');assert.equal(initial.demoBusiness.devices[0].tasks[0].runtimeState,'running');assert(initial.demoBusiness.devices[0].tasks[0].runtimeObservedAt);assert.equal(initial.demoBusiness.devices[3].tasks[0].runtimeState,'unknown');assert(initial.demoBusiness.packages.every(m=>['llm','vision'].includes(m.slot)));assert.equal(initial.demoBusiness.devices[0].tasks[0].models[0].slot,'llm');
      await page.evaluate(()=>{const db=window.EdgeStackDemo.getState(),d=db.demoBusiness.devices[0],now=Date.now();for(let i=0;i<35;i++){const at=new Date(now-3600000-i*1000).toISOString();db.demoBusiness.events.push({eventId:'demo-page-'+i,deviceId:d.id,taskId:d.tasks[0].id,sourceId:d.sources[0].id,type:'voice.user_utterance',phase:'final',text:'模拟分页会话 '+i,sessionId:'demo-session-paginated',occurredAt:at,receivedAt:at,demo:true});}const older=new Date(now-48*3600000).toISOString();db.demoBusiness.events.push({eventId:'demo-camera-older',deviceId:'demo-device-2',taskId:'demo-task-2-1',type:'camera.observation',text:'两天前的模拟观察',sessionId:'demo-session-older',occurredAt:older,receivedAt:older,demo:true});localStorage.setItem('edgestack.conference.v1',JSON.stringify(db));});await page.reload();await workspace.locator('.ew-station').first().waitFor();await page.locator('[data-view=interactions]').click();await workspace.locator('.ew-records .ew-timeline').waitFor();assert.equal(await workspace.getByText('两天前的模拟观察',{exact:true}).count(),0);await workspace.locator('[name=eventPeriod]').selectOption('168');await workspace.getByText('两天前的模拟观察',{exact:true}).waitFor();await workspace.locator('[name=eventType]').selectOption('conversation');await workspace.getByText('模拟分页会话 0',{exact:true}).waitFor();await workspace.getByRole('button',{name:'加载更早记录',exact:true}).click();await workspace.getByText('模拟分页会话 34',{exact:true}).waitFor();await workspace.locator('[name=eventSession]').selectOption('demo-session-paginated');await workspace.getByText('模拟分页会话 0',{exact:true}).waitFor();await workspace.getByRole('button',{name:'加载更早记录',exact:true}).click();await workspace.getByText('模拟分页会话 34',{exact:true}).waitFor();assert.equal(await workspace.getByText('帮我记住，周末给阳台的植物浇水。',{exact:true}).count(),0);
      const filtered=await page.evaluate(async()=>{const q=new URLSearchParams({deviceId:'demo-device-1',type:'voice.user_utterance,voice.assistant_reply',sessionId:'demo-session-paginated',limit:'2',from:new Date(Date.now()-7*86400000).toISOString(),to:new Date().toISOString()});return (await fetch(new URL('api/edgestack/events?'+q,document.baseURI))).json();});assert.equal(filtered.filteredCount,35);assert.equal(filtered.coverage.eventCount,38);assert.equal(filtered.items.length,2);assert(filtered.nextCursor);await page.screenshot({path:path.join(output,'final-filtered-session.png'),fullPage:true});
      await workspace.locator('[name=eventSession]').selectOption('');await workspace.locator('[name=eventType]').selectOption('vision');await workspace.getByText('两天前的模拟观察',{exact:true}).waitFor();await workspace.locator('[name=eventPeriod]').selectOption('24');await workspace.getByText('前台区域已经恢复安静。',{exact:true}).waitFor();assert.equal(await workspace.getByText('两天前的模拟观察',{exact:true}).count(),0);await workspace.locator('[name=eventType]').selectOption('playback');await workspace.getByText('播放状态 · 播放结束',{exact:true}).waitFor();
      await page.locator('[data-view=space]').click();await workspace.locator('.ew-station').first().click();await workspace.locator('.ew-drawer').getByText('任务实例运行中',{exact:false}).waitFor();assert(!(await workspace.locator('.ew-drawer').innerText()).includes('服务正常'));await workspace.locator('.ew-drawer-tabs').getByRole('button',{name:'模型与数据',exact:true}).click();await workspace.getByRole('button',{name:'选择兼容模型'}).click();await workspace.locator('[data-action=preview-model][data-id=demo-voice-v2]').click();const preview=workspace.getByRole('region',{name:'模型替换预览'});await preview.getByText('变更前模型',{exact:true}).waitFor();assert((await preview.innerText()).includes('demo-voice-v1'));await page.screenshot({path:path.join(output,'final-runtime-previous-model.png'),fullPage:true});await workspace.getByRole('button',{name:'确认替换模型',exact:true}).click();await workspace.locator('.ew-operation').getByText('已完成',{exact:true}).waitFor();assert((await workspace.locator('.ew-operation').innerText()).includes('demo-voice-v1'));assert.equal(await page.evaluate(()=>Object.values(window.EdgeStackDemo.getState().demoBusiness.operations)[0].previousPackageId),'demo-voice-v1');
      await workspace.locator('.ew-drawer-tabs').getByRole('button',{name:'互动记录',exact:true}).click();await workspace.locator('[name=question]').fill('这次发生了什么');await workspace.locator('[data-action=summarize]').click();await workspace.getByText('关于你的问题',{exact:true}).waitFor();await workspace.locator('[name=question]').fill('');await workspace.locator('[data-action=summarize]').click();await workspace.getByText('离线演示小结 · 本地样例',{exact:true}).waitFor();assert.equal(await workspace.getByText('关于你的问题',{exact:true}).count(),0);assert.deepEqual(errors,[]);assert.deepEqual(consoleErrors,[]);assert.deepEqual(external,[]);const result={status:'passed',ui_version:await page.evaluate(()=>window.EdgeStackWorkspace.version),checks:['正式type/phase和llm/vision slot一致，runtime模拟事实与登记分开','最终离线时间/type/session过滤与loadMore及filteredCount正确','模型preview与operation保留previousPackageId并经模拟观察确认','空问题不复用之前的answer'],errors,consoleErrors,external};fs.writeFileSync(path.join(output,'release6-results.json'),JSON.stringify(result,null,2));console.log(JSON.stringify(result,null,2));return;
    }
    if(process.argv.includes('--preview-copy-only')){
      const workspace=page.locator('[data-edgestack-workspace]');await page.evaluate(()=>{const original=window.fetch;window.fetch=async(...args)=>{const response=await original(...args);if(String(args[0]).includes('/model-change-preview')){const data=await response.clone().json();data.effects=['Update only the registered service image and approved model environment values','Restart the task; retain all volumes, ports, networks, and unrelated environment values','Wait for a trusted runtime receipt before marking the model loaded'];return new Response(JSON.stringify(data),{status:response.status,headers:response.headers});}return response;};});await workspace.locator('.ew-station').first().click();await workspace.locator('.ew-drawer-task').first().waitFor();await workspace.locator('.ew-drawer-tabs').getByRole('button',{name:'模型与数据',exact:true}).click();await workspace.getByRole('button',{name:'选择兼容模型'}).click();await workspace.locator('[data-action=preview-model][data-id=demo-voice-v2]').click();const preview=workspace.getByRole('region',{name:'模型替换预览'});await preview.waitFor();const copy=await preview.innerText();for(const text of ['仅更新已登记服务的镜像与已批准的模型环境参数。','重启任务；保留全部数据卷、端口、网络及其他环境参数。','收到可信的运行时回执后，才将模型标记为已加载。'])assert(copy.includes(text),text);for(const text of ['Update only','Restart the task','Wait for a trusted'])assert(!copy.includes(text),text);assert.equal(await page.evaluate(()=>window.EdgeStackDemo.requests.filter(r=>r.method==='POST'&&r.path.includes('/model-changes')).length),0);assert.deepEqual(errors,[]);assert.deepEqual(consoleErrors,[]);assert.deepEqual(external,[]);await page.screenshot({path:path.join(output,'final-preview-effects-zh.png'),fullPage:true});const result={status:'passed',ui_version:await page.evaluate(()=>window.EdgeStackWorkspace.version),checks:['最终离线文件中的真实3条英文预览effects显示中文','仅预览未提交模型变更'],errors,consoleErrors,external};fs.writeFileSync(path.join(output,'final-preview-copy.json'),JSON.stringify(result,null,2));console.log(JSON.stringify(result,null,2));return;
    }
    const userFlows=[];
    const workspace=page.locator('[data-edgestack-workspace]');
    assert.equal(await page.locator('[data-edgestack-nav] [data-view]').count(),4);
    assert.equal(await workspace.locator('.ew-station').count(),8);
    assert.equal(await page.evaluate(()=>window.EdgeStackDemo.getState().demoPersona),'user');
    assert.equal(await page.evaluate(()=>window.EdgeStackDemo.requests.some(r=>r.path.includes('/summaries'))),false);
    userFlows.push('默认普通用户、完整导航、八设备且小结未自动调用');
    await workspace.locator('.ew-station').first().click();await workspace.locator('.ew-drawer-task').first().waitFor();
    await workspace.locator('.ew-drawer-tabs').getByRole('button',{name:'互动记录',exact:true}).click();
    await workspace.locator('.ew-timeline p').first().waitFor();await workspace.locator('[name=question]').fill('最近发生了什么？');
    await workspace.locator('[data-action=summarize]').click();await workspace.getByText('离线演示小结 · 本地样例',{exact:true}).waitFor();
    assert((await workspace.locator('.ew-summary-result').innerText()).includes('未调用 DeepSeek 云服务'));
    await workspace.getByRole('button',{name:'查看依据',exact:true}).first().click();assert(await workspace.locator('.ew-evidence').getAttribute('open')!==null);userFlows.push('业务互动、离线小结、提问与可追溯依据');
    await workspace.locator('.ew-drawer-tabs').getByRole('button',{name:'模型与数据',exact:true}).click();await workspace.getByRole('button',{name:'选择兼容模型'}).click();await workspace.locator('[data-action=preview-model][data-id=demo-voice-v2]').click();
    assert.equal(await page.evaluate(()=>window.EdgeStackDemo.getState().demoBusiness.devices[0].tasks[0].models[0].packageId),'demo-voice-v1');
    await workspace.getByRole('button',{name:'确认替换模型',exact:true}).click();await workspace.locator('.ew-operation').getByText('已完成',{exact:true}).waitFor();
    assert.equal(await page.evaluate(()=>window.EdgeStackDemo.getState().demoBusiness.devices[0].tasks[0].models[0].observedPackageId),'demo-voice-v2');userFlows.push('兼容模型预览、确认、模拟观察回执后更新');
    await page.keyboard.press('Escape');await page.reload();await workspace.locator('.ew-station').first().waitFor();assert.equal(await page.evaluate(()=>window.EdgeStackDemo.getState().demoBusiness.devices[0].tasks[0].models[0].packageId),'demo-voice-v2');userFlows.push('刷新保留会议模型状态');
    await page.waitForTimeout(500);await page.screenshot({path:path.join(output,'workspace-offline.png'),fullPage:true});
    if(process.argv.includes('--truthful')){
      const baseline=await page.evaluate(()=>localStorage.getItem('edgestack.conference.v1'));
      await page.evaluate(()=>{const db=window.EdgeStackDemo.getState(),d=db.demoBusiness.devices[0],t=d.tasks[0];db.demoPersona='user';db.demoBusiness.devices=[d];db.demoBusiness.events=[];t.state='registered';t.runtimeState='unknown';delete t.runtimeObservedAt;t.kind='voice-test';t.models[0].state='unobserved';t.models[0].slot='asr-encoder';t.dataLocations=[{name:'登记的数据',path:'/demo/data/voice',verification:'registered'},{name:'服务端记录',kind:'server',location:'本地事件记录',verification:'registered'}];d.sources=[{id:'demo-voice-unconfigured',deviceId:d.id,taskId:t.id,name:'语音来源（演示）',type:'voice.transcript',enabled:true,status:'unconfigured',demo:true},{id:'demo-model-observer',deviceId:d.id,taskId:t.id,name:'模型观察（演示）',type:'model.observer',enabled:true,status:'healthy',demo:true}];localStorage.setItem('edgestack.conference.v1',JSON.stringify(db));location.hash='#!/workspace?view=space';});
      await page.reload();await page.setViewportSize({width:1470,height:875});await workspace.getByText('1 项任务已登记',{exact:true}).waitFor();await workspace.getByText('尚未收到互动记录',{exact:true}).waitFor();assert(!(await workspace.innerText()).includes('一切安好'));assert(!(await workspace.locator('.ew-node-caption').textContent()).includes('正在提供'));
      const fullStation=async()=>{const b=await page.evaluate(()=>{const n=document.querySelector('.ew-station').getBoundingClientRect(),v=document.querySelector('.ew-scene-scroll').getBoundingClientRect();return {top:n.top,bottom:n.bottom,left:n.left,right:n.right,t:v.top,b:v.bottom,l:v.left,r:v.right};});assert(b.top>=b.t-1&&b.bottom<=b.b+1&&b.left>=b.l-1&&b.right<=b.r+1,JSON.stringify(b));};
      await fullStation();await page.screenshot({path:path.join(output,'release02-single-user.png'),fullPage:true});
      await workspace.locator('.ew-station').click();await workspace.locator('.ew-drawer').getByText('管理连接在线',{exact:true}).waitFor();await workspace.getByText('语音交互尚未接入',{exact:true}).first().waitFor();await workspace.getByText('模型确认来源',{exact:true}).waitFor();assert(!(await workspace.locator('.ew-drawer').innerText()).includes('unconfigured'));
      await workspace.locator('.ew-drawer-tabs').getByRole('button',{name:'模型与数据',exact:true}).click();await workspace.getByText('管理员登记，实际挂载待核验',{exact:true}).waitFor();assert.equal(await workspace.getByText('已核验实际挂载',{exact:true}).count(),0);await workspace.getByText('语音识别 · 尚未观察到',{exact:true}).waitFor();await page.screenshot({path:path.join(output,'release02-truthful-details.png'),fullPage:true});userFlows.push('已登记任务、管理连通、来源未接入和登记数据不冒充业务健康或挂载核验');
      await page.evaluate(()=>{const db=window.EdgeStackDemo.getState();db.demoPersona='admin';localStorage.setItem('edgestack.conference.v1',JSON.stringify(db));location.hash='#!/workspace?view=space';});await page.reload();await page.getByRole('heading',{name:'首页',exact:true}).waitFor();await page.waitForTimeout(500);await page.locator('[data-edgestack-preview]').click();await workspace.locator('.ew-station').waitFor();await page.waitForTimeout(200);await fullStation();assert.equal(await page.locator('[data-edgestack-nav] [data-view]').count(),4);assert.equal(await page.evaluate(()=>document.body.classList.contains('edgestack-admin-preview')),true);assert.equal(await page.locator('.sidebar').isVisible(),false);await page.screenshot({path:path.join(output,'release02-single-admin-300px.png'),fullPage:true});userFlows.push('1470×875普通用户与完整用户布局的管理员预览单工位完整可见');
      await page.evaluate(value=>{localStorage.setItem('edgestack.conference.v1',value);location.hash='#!/workspace?view=space';},baseline);await page.reload();await workspace.locator('.ew-station').first().waitFor();await page.setViewportSize({width:1440,height:1000});
      await page.waitForTimeout(150);await page.locator('#demo-toggle').click();await page.locator('#demo-voice-event').click();await workspace.locator('.ew-live-event').first().waitFor({timeout:8000});assert((await workspace.locator('.ew-live-event').first().textContent()).includes('用户说'));await page.screenshot({path:path.join(output,'final-offline-live-event.png'),fullPage:true});
      await page.evaluate(()=>{const db=window.EdgeStackDemo.getState(),d=db.demoBusiness.devices[0],t=d.tasks[0],stamp=new Date().toISOString();db.demoBusiness.events=[{eventId:'demo-user-check',deviceId:d.id,taskId:t.id,type:'voice.user_utterance',phase:'final',text:'测试用户输入',occurredAt:stamp,receivedAt:stamp},{eventId:'demo-assistant-check',deviceId:d.id,taskId:t.id,type:'voice.assistant_reply',phase:'generated',text:'回声测试回复：测试用户输入',provenance:{kind:'integration_test'},occurredAt:stamp,receivedAt:stamp},...['tts_requested','synthesis_complete','playing','played','interrupted'].map((phase,i)=>({eventId:'demo-phase-'+i,deviceId:d.id,taskId:t.id,type:'voice.playback_state',phase,text:'',provenance:{kind:'integration_test'},occurredAt:stamp,receivedAt:stamp}))];localStorage.setItem('edgestack.conference.v1',JSON.stringify(db));location.hash='#!/workspace?view=space';});await page.reload();await workspace.locator('.ew-station').first().waitFor();await workspace.locator('.ew-station').first().click();await workspace.locator('.ew-drawer-tabs').getByRole('button',{name:'互动记录',exact:true}).click();await workspace.locator('.ew-drawer .ew-timeline').waitFor();const timeline=await workspace.locator('.ew-drawer').innerText();for(const label of ['用户说','设备回复','生成','请求合成','合成完成','正在播放','播放结束','播放中断','接入测试'])assert(timeline.includes(label),label);await page.screenshot({path:path.join(output,'final-offline-event-phases.png'),fullPage:true});userFlows.push('模拟新事件触发有限时提示，真实事件类型与播放阶段中文、接入测试单独标识');
      await page.evaluate(value=>{localStorage.setItem('edgestack.conference.v1',value);location.hash='#!/workspace?view=space';},baseline);await page.reload();await workspace.locator('.ew-station').first().waitFor();
    }
    const switchPersona=async(persona)=>{await page.locator('#demo-toggle').click();await Promise.all([page.waitForEvent('load'),page.locator('[data-persona="'+persona+'"]').click()]);await page.waitForFunction(p=>window.EdgeStackDemo?.getState().demoPersona===p,persona);};
    await switchPersona('admin');await page.waitForFunction(()=>!!window.EdgeStackWorkspace);await page.getByRole('heading',{name:'首页',exact:true}).waitFor();assert.equal(await page.locator('[data-edgestack-nav]').count(),0);userFlows.push('明确切换至原管理员后台并保留菜单');
    await page.screenshot({path:path.join(output,'admin-offline.png'),fullPage:true});
    fs.writeFileSync(path.join(output,'workspace-flows.json'),JSON.stringify({status:'passed',passed:userFlows,errors,external},null,2));
    if (process.argv.includes('--flows')) {
      const passed = [];
      const go = async (route) => { await page.evaluate((r) => { location.hash = '#!/' + r; }, route); await page.waitForTimeout(400); };
      const state = await page.evaluate(() => window.EdgeStackDemo.getState());
      const cid = state.containers[0].Id;
      try {
        await go(`4/docker/containers/${cid}`);
        await page.locator('[data-cy="stop-container-button"]').click();
        await page.waitForFunction((id) => !window.EdgeStackDemo.getState().containers.find((c) => c.Id === id).State.Running, cid);
        await page.locator('[data-cy="start-container-button"]').click();
        await page.waitForFunction((id) => window.EdgeStackDemo.getState().containers.find((c) => c.Id === id).State.Running, cid);
        passed.push('原生容器停止和启动，状态联动');

        await go('4/docker/containers/new');
        await page.locator('[data-cy="container-name-input"]').fill('conference-web');
        await page.getByRole('combobox', { name: '镜像', exact: true }).fill('nginx:alpine');
        await page.keyboard.press('Tab');
        await page.locator('[data-cy="deploy-container-button"]').click();
        await page.waitForFunction(() => window.EdgeStackDemo.getState().containers.some((c) => c.Name === '/conference-web' && c.State.Running), null, { timeout: 8000 });
        await page.waitForFunction(() => location.hash === '#!/4/docker/containers', null, { timeout: 8000 });
        passed.push('原生创建容器表单、拉取和启动');

        await go('edge/groups/new');
        await page.locator('input[name="name"]').fill('conference-workers');
        await page.locator('[data-cy="edgeGroupCreate-availableEndpoints"] tbody tr').filter({ hasText: 'rk3588-demo-01' }).click();
        await page.locator('[data-cy="edgeGroupCreate-addGroupButton"]').click();
        await page.waitForFunction(() => window.EdgeStackDemo.getState().edge_groups.some((g) => g.Name === 'conference-workers'), null, { timeout: 5000 });
        await page.waitForFunction(() => location.hash === '#!/edge/groups', null, { timeout: 5000 });
        assert((await page.evaluate(() => window.EdgeStackDemo.getState().edge_groups.find((g) => g.Name === 'conference-workers').Endpoints)).includes(4));
        passed.push('原生边缘分组创建与成员选择');

        await go('4/docker/images');
        await page.getByRole('combobox', { name: '镜像', exact: true }).fill('redis:7-alpine');
        await page.keyboard.press('Tab');
        await page.getByRole('button', { name: '拉取镜像', exact: true }).click();
        await page.waitForFunction(() => window.EdgeStackDemo.getState().images.some((x) => x.RepoTags.some((t) => t.includes('redis'))), null, { timeout: 5000 });
        passed.push('原生镜像拉取表单与进度反馈');

        await go('4/docker/stacks/newstack');
        await page.locator('[data-cy="stack-name-input"]').fill('conference-stack');
        const editor = page.locator('.cm-content[contenteditable="true"]');
        await editor.click();
        await page.keyboard.insertText('services:\n  web:\n    image: nginx:alpine\n    ports:\n      - "18081:80"\n    restart: unless-stopped\n');
        await page.getByRole('button', { name: '部署应用栈', exact: true }).click();
        await page.waitForFunction(() => window.EdgeStackDemo.getState().stacks.some((s) => s.Name === 'conference-stack'), null, { timeout: 5000 });
        await page.waitForFunction(() => location.hash.startsWith('#!/4/docker/stacks/') && !location.hash.includes('newstack'), null, { timeout: 5000 });
        assert((await page.evaluate(() => window.EdgeStackDemo.getState().containers)).some((c) => c.Name === '/conference-stack-web-1' && c.Config.Image === 'nginx:alpine'));
        passed.push('原生 CodeMirror Compose 编辑器与应用栈部署');

        await go('edge/stacks/new');
        await page.locator('[data-cy="edgeStackCreate-nameInput"]').fill('conference-edge-app');
        await page.locator('[data-cy="edge-stacks-groups-selector"]').click();
        await page.getByRole('option', { name: 'home-workers', exact: true }).click();
        await page.keyboard.press('Escape');
        await page.locator('.cm-content[contenteditable="true"]').click();
        await page.keyboard.insertText('services:\n  web:\n    image: nginx:alpine\n    ports:\n      - "18082:80"\n');
        await page.locator('[data-cy="edgeStackCreate-createStackButton"]').click();
        await page.waitForFunction(() => window.EdgeStackDemo.getState().edge_stacks.some((s) => s.Name === 'conference-edge-app'), null, { timeout: 5000 });
        await page.waitForFunction(() => location.hash === '#!/edge/stacks', null, { timeout: 5000 });
        passed.push('原生边缘应用栈选组、Compose 编辑和下发');

        await page.reload();
        await page.waitForFunction(() => window.EdgeStackDemo && window.EdgeStackDemo.getState().stacks.some((s) => s.Name === 'conference-stack'));
        passed.push('刷新后会议数据保留');
        assert.equal(errors.length, 0);
        assert.equal(consoleErrors.length, 0);
        assert.equal(external.length, 0);
        console.log(JSON.stringify({ flow_status: 'passed', passed, errors, external }, null, 2));
        fs.writeFileSync(path.join(output, 'flows.json'), JSON.stringify({ status: 'passed', passed, errors, external }, null, 2));
      } catch (error) {
        await page.screenshot({ path: path.join(output, 'flow-failed.png'), fullPage: true });
        const report = { passed, error: error.message, errors, consoleErrors, body: await page.locator('body').innerText(), requests: await page.evaluate(() => window.EdgeStackDemo.requests) };
        fs.writeFileSync(path.join(output, 'flow-failed.json'), JSON.stringify(report, null, 2));
        console.error(JSON.stringify(report, null, 2));
        throw error;
      }
    }
    if (process.argv.includes('--audit')) {
      const state = await page.evaluate(() => window.EdgeStackDemo.getState());
      const cid = state.containers[0].Id, image = encodeURIComponent(state.images[0].Id), network = state.networks[3].Id;
      const routes = [
        'home', '4/docker/dashboard', '4/docker/containers', '4/docker/containers/new',
        `4/docker/containers/${cid}`, `4/docker/containers/${cid}/logs`, `4/docker/containers/${cid}/stats`, `4/docker/containers/${cid}/inspect`, `4/docker/containers/${cid}/exec`,
        '4/docker/images', '4/docker/images/build', '4/docker/images/import', `4/docker/images/${image}`,
        '4/docker/networks', '4/docker/networks/new', `4/docker/networks/${network}`,
        '4/docker/volumes', '4/docker/volumes/new', '4/docker/volumes/edgestack_agent_data', '4/docker/volumes/edgestack_agent_data/browse',
        '4/docker/stacks', '4/docker/stacks/newstack', '4/docker/stacks/1?type=2&external=false',
        '4/docker/templates', '4/docker/templates/custom', '4/docker/events', '4/docker/host/feat-config', '4/docker/host/registries',
        'edge/groups', 'edge/groups/1', 'edge/groups/new', 'edge/stacks', 'edge/stacks/1', 'edge/stacks/new', 'edge/jobs', 'edge/jobs/new', 'edge/templates',
        'users', 'users/1', 'teams', 'teams/1', 'endpoints', 'endpoints/4', 'groups', 'tags', 'registries', 'registries/new',
        'auth-logs', 'notifications', 'settings', 'settings/auth', 'settings/edge', 'account', 'gitops/workflows', 'gitops/sources',
      ];
      const audit = [];
      for (const route of routes) {
        const from = errors.length, cf = consoleErrors.length;
        await page.evaluate((r) => { location.hash = '#!/' + r; }, route);
        await page.waitForTimeout(650);
        audit.push({ route, hash: await page.evaluate(() => location.hash), errors: errors.slice(from), console: consoleErrors.slice(cf), body: (await page.locator('body').innerText()).slice(-11000) });
      }
      const req = await page.evaluate(() => window.EdgeStackDemo.requests);
      fs.writeFileSync(path.join(output, 'audit.json'), JSON.stringify({ audit, requests: req, external }, null, 2));
      console.log(JSON.stringify({ audited_routes: audit.length, errors: audit.filter((r) => r.errors.length || r.console.length).map((r) => ({ route: r.route, errors: r.errors, console: r.console })), unhandled: req.filter((r) => r.unhandled), external }, null, 2));
      assert.equal(audit.filter((r) => r.errors.length || r.console.length).length, 0, 'All audited native routes load without errors');
      assert.equal(req.filter((r) => r.unhandled).length, 0, 'All audited routes have matching local responses');
      assert.equal(external.length, 0);
    }
    if (process.argv.includes('--controls')) {
      const passed = [];
      const before = await page.evaluate(() => window.EdgeStackDemo.getState().containers.filter((c) => c.State.Running).map((c) => [c.Id, c.State.StartedAt]));
      await page.locator('#demo-toggle').click();
      await Promise.all([page.waitForEvent('load'), page.locator('#demo-board').selectOption('0')]);
      await page.waitForFunction(() => window.EdgeStackDemo && !window.EdgeStackDemo.getState().boardOnline);
      await page.getByText('管理通道离线', { exact: true }).or(page.locator('[data-cy="environment-card-rk3588-demo-01"]').getByText('离线', { exact: true })).waitFor();
      assert.deepEqual(await page.evaluate(() => window.EdgeStackDemo.getState().containers.filter((c) => c.State.Running).map((c) => [c.Id, c.State.StartedAt])), before);
      passed.push('模拟离线，业务容器身份和运行状态保持');
      await page.locator('#demo-toggle').click();
      await Promise.all([page.waitForEvent('load'), page.locator('#demo-board').selectOption('1')]);
      await page.waitForFunction(() => window.EdgeStackDemo?.getState().boardOnline);
      passed.push('模拟重新连接');
      await page.locator('#demo-toggle').click();
      await page.locator('#demo-cpu').press('End');
      assert.equal(await page.evaluate(() => window.EdgeStackDemo.getState().cpu), 95);
      passed.push('监控示例值调节');
      const [download] = await Promise.all([page.waitForEvent('download'), page.locator('#demo-export').click()]);
      const exported = JSON.parse(fs.readFileSync(await download.path(), 'utf8'));
      assert(exported.containers.length >= 11);
      passed.push('会议状态 JSON 下载');
      await page.locator('#demo-help').click();
      await page.locator('#demo-info').waitFor();
      await page.keyboard.press('Escape');
      assert.equal(await page.locator('#demo-info').count(), 0);
      passed.push('本地帮助与 Esc 关闭');
      await page.locator('#demo-toggle').click();
      page.once('dialog', (dialog) => dialog.accept());
      await Promise.all([page.waitForEvent('load'), page.locator('#demo-reset').click()]);
      await page.waitForFunction(() => window.EdgeStackDemo?.getState().containers.length === 11);
      await page.getByRole('heading', { name: '主机概览', exact: true }).or(page.getByRole('heading', { name: '我的空间.', exact: true })).waitFor();
      assert.equal(await page.evaluate(() => window.EdgeStackDemo.getState().edge_groups.length), 1);
      passed.push('一键恢复初始演示数据');
      if (await page.locator('[data-edgestack-nav]').count() && await page.locator('[data-edgestack-nav]').isVisible()) await page.locator('[data-edgestack-nav] a[href="#!/logout"]').click();
      else {await page.locator('[data-cy="userMenu-button"]').click();await page.locator('[data-cy="userMenu-logOut"]').click();}
      await page.locator('#username').waitFor();
      await page.locator('#username').fill('admin');
      await page.locator('#password').fill('demo');
      await page.getByRole('button', { name: '登录', exact: true }).click();
      await page.waitForFunction(() => window.EdgeStackDemo.getState().loggedIn);
      await page.getByRole('heading', { name: '首页', exact: true }).or(page.getByRole('heading', { name: '我的空间.', exact: true })).waitFor();
      passed.push('原生退出和演示账户重新登录');
      assert.equal(errors.length, 0);
      assert.equal(external.length, 0);
      fs.writeFileSync(path.join(output, 'controls.json'), JSON.stringify({ status: 'passed', passed, errors, external }, null, 2));
      console.log(JSON.stringify({ controls_status: 'passed', passed, errors, external }, null, 2));
    }
    if (process.argv.includes('--preview')) {
      if(await page.evaluate(()=>window.EdgeStackDemo.getState().demoPersona!=='admin'))await switchPersona('admin');
      await page.evaluate(() => { location.hash = '#!/4/docker/host'; });
      await page.getByRole('heading', { name: '主机概览', exact: true }).waitFor();
      await page.screenshot({ path: path.join(output, 'final-preview.png'), fullPage: true });
      await page.locator('#demo-toggle').click();
      await page.screenshot({ path: path.join(output, 'final-controls.png'), fullPage: true });
    }
  } catch(error) { await page.screenshot({path:path.join(output,'latest-failure.png'),fullPage:true});fs.writeFileSync(path.join(output,'latest-failure.json'),JSON.stringify({error:error.message,body:await page.locator('body').innerText(),state:await page.evaluate(()=>window.EdgeStackDemo?.getState()),requests:await page.evaluate(()=>window.EdgeStackDemo?.requests),errors,consoleErrors,external},null,2));throw error;} finally { await browser.close(); }
})().catch((error) => { console.error(error); process.exitCode = 1; });
