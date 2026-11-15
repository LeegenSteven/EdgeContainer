/* EdgeStack user workspace. No frontend runtime dependencies. The authenticated
 * Angular router owns mount/unmount; each instance owns its requests, listeners
 * and polling timers. The native administrator experience remains the default. */
(function () {
  'use strict';
  // This standalone resource owns one set of router hooks and observers per
  // document, even if a host accidentally includes the script twice.
  if(window.__EDGESTACK_WORKSPACE_RUNTIME__)return;
  window.__EDGESTACK_WORKSPACE_RUNTIME__=true;
  const buildVersion = '__EDGESTACK_VERSION__';
  const startupHash = location.hash;
  const esc = value => String(value ?? '').replace(/[&<>"']/g, c => ({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c]));
  const items = value => Array.isArray(value) ? value : value?.items || [];
  const id = value => String(value?.id ?? '');
  const views = ['space', 'tasks', 'interactions', 'library'];
  const titles = {space:'我的空间', tasks:'我的任务', interactions:'互动记录', library:'模型与数据'};
  const aliases = {devices:'space', apps:'tasks', deploy:'tasks', models:'library', events:'interactions'};
  const iconPaths = {
    space:'<path d="m3 7 9-5 9 5-9 5-9-5Zm0 5 9 5 9-5M3 17l9 5 9-5"/>',
    tasks:'<rect x="4" y="4" width="16" height="16" rx="4"/><path d="m8 9 2 2 5-4M8 16h8"/>',
    interactions:'<path d="M21 11.5a8.4 8.4 0 0 1-.9 3.8 8.5 8.5 0 0 1-7.6 4.7 8.4 8.4 0 0 1-3.8-.9L3 21l1.9-5.7a8.4 8.4 0 0 1-.9-3.8 8.5 8.5 0 0 1 4.7-7.6 8.4 8.4 0 0 1 3.8-.9h.5a8.5 8.5 0 0 1 8 8v.5Z"/><path d="M8 10h8M8 14h5"/>',
    library:'<path d="m12 2 9 5v10l-9 5-9-5V7l9-5Zm0 10 9-5M3 7l9 5v10M7 4.8l9 5v5"/>',
    arrow:'<path d="m8 5 7 7-7 7"/>', close:'<path d="m6 6 12 12M6 18 18 6"/>',
    refresh:'<path d="M20 7v5h-5M4 17v-5h5"/><path d="M6.1 7a7 7 0 0 1 11.6-2L20 8M4 16l2.3 3A7 7 0 0 0 18 17"/>',
    spark:'<path d="m12 3 2.5 6.5L21 12l-6.5 2.5L12 21l-2.5-6.5L3 12l6.5-2.5L12 3ZM20 2v4M18 4h4"/>',
    search:'<circle cx="10.5" cy="10.5" r="6.5"/><path d="m16 16 5 5"/>',
    data:'<ellipse cx="12" cy="5" rx="8" ry="3"/><path d="M4 5v14c0 4 16 4 16 0V5M4 12c0 4 16 4 16 0"/>',
    user:'<circle cx="12" cy="8" r="4"/><path d="M4 21v-2a8 8 0 0 1 16 0v2"/>',
    logout:'<path d="M9 4H4v16h5M14 8l4 4-4 4M9 12h12"/>',
    audio:'<rect x="9" y="2" width="6" height="13" rx="3"/><path d="M5 10v2a7 7 0 0 0 14 0v-2M12 19v3M8 22h8"/>',
    camera:'<path d="M3 7h4l2-3h6l2 3h4v13H3V7Z"/><circle cx="12" cy="13" r="4"/>',
    check:'<path d="m5 12 4 4L19 6"/>', info:'<circle cx="12" cy="12" r="9"/><path d="M12 11v6M12 7h.01"/>',
    list:'<path d="M8 6h13M8 12h13M8 18h13M3 6h.01M3 12h.01M3 18h.01"/>',
  };
  const icon = (name, cls = '') => `<svg class="ew-icon ${cls}" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">${iconPaths[name] || iconPaths.info}</svg>`;
  const time = value => {const d=new Date(typeof value==='number'&&value<1e12?value*1000:value);return !value||!Number.isFinite(d.getTime())||d.getUTCFullYear()<2000?'尚无记录':d.toLocaleString('zh-CN',{month:'2-digit',day:'2-digit',hour:'2-digit',minute:'2-digit'});};
  const stateLabel = value => ({running:'运行中',ready:'已就绪',stopped:'已停止',exited:'已停止',paused:'已暂停',unknown:'待确认',unobserved:'尚未观察到',loaded:'已加载',registered:'已登记',configured:'已配置',verified:'已核验',pending:'等待中',degraded:'需关注',failed:'未完成',succeeded:'已完成',completed:'已完成',applying:'正在应用',dispatching:'正在下发',awaiting_observation:'等待设备确认',applied:'已应用',queued:'等待设备接收'})[value] || '待确认';
  const taskRuntime=t=>t.runtimeState==='running'?'任务实例运行中':'运行状态待确认';
  const eventTypeGroups={all:'',conversation:'voice.user_utterance,voice.assistant_reply',vision:'camera.observation',playback:'voice.playback_state'};
  const taskKind = value => ({voice:'语音助手',speech:'语音交互',audio:'语音交互','voice-test':'语音接入测试',camera:'视觉观察',vision:'视觉观察',web:'网页应用',workflow:'业务流程',application:'业务应用',app:'业务应用',inference:'本地推理'})[value] || '业务任务';
  const sourceState = value => ({connected:'已连接',online:'已连接',healthy:'运行正常',streaming:'正在接收',backpressure:'接收暂缓',unconfigured:'尚未接入',unsupported:'暂不支持',ready:'已就绪',active:'已连接',disconnected:'未连接',error:'连接异常',pending:'等待连接',registered:'已登记',unknown:'待确认'})[value] || '待确认';
  const interactionSources = d => (d?.sources||[]).filter(source=>source.type!=='model.observer');
  const taskList = d => (d?.tasks || []).filter(t => t.kind !== 'system' && t.kind !== 'agent' && !t.system);
  const eventPhase=value=>({final:'已识别',generated:'生成',tts_requested:'请求合成',synthesis_started:'开始合成',synthesis_complete:'合成完成',synthesis_completed:'合成完成',playing:'正在播放',played:'播放结束',interrupted:'播放中断',synthesis_aborted:'合成中止',failed:'未完成'})[value]||'';
  const eventRole=e=>/voice\.(?:user_utterance|transcript)$/.test(e.type)?'用户说':/voice\.(?:assistant_reply|response)$/.test(e.type)?'设备回复':e.type==='voice.playback_state'?'播放状态':/^camera/.test(e.type)?'观察记录':'业务互动';
  const integrationTest=e=>e.provenance?.kind==='integration_test';
  const eventText = e => e.text || e.summary || e.message || e.title || eventPhase(e.phase) || '一条新的业务互动';
  const businessEvent=e=>['voice.user_utterance','voice.assistant_reply','camera.observation','voice.transcript','voice.response'].includes(e.type);
  const eventIcon = e => /voice|audio|speech/.test(e.type) ? 'audio' : /camera|vision|image/.test(e.type) ? 'camera' : 'interactions';
  const workspaceMemory=new Map(),activityMemory=new Map();
  const modelPurpose=value=>({'asr-encoder':'语音识别',asr:'语音识别',llm:'对话',tts:'语音合成',vision:'视觉理解'})[value]||'用途待登记';
  const liveLabel=e=>eventRole(e)+(eventPhase(e.phase)?' · '+eventPhase(e.phase):'')+(integrationTest(e)?' · 接入测试':'');
  let injector, router, auth, automaticNavigation;
  const apiRoot = new URL('api/edgestack/', document.baseURI);
  function normalView(v) { return views.includes(v) ? v : aliases[v] || 'space'; }
  function isAdmin() { return !!auth?.isPureAdmin(); }
  function routeParams() { const q = new URLSearchParams(location.hash.split('?')[1] || ''); return {view:normalView(q.get('view')),device:q.get('device') || null}; }
  function workspaceRoute() { return /portainer\.(?:workspace|edgestack)$/.test(router?.current?.name || ''); }
  // Startup deep links and the native home success hook can run in the same
  // turn. Keep their automatic redirect single-flight, and respect an already
  // active workspace transition (including one initiated by the native router).
  function openWorkspaceAutomatically(params) {
    if(workspaceRoute()||automaticNavigation)return;
    if(/portainer\.(?:workspace|edgestack)$/.test(router.transition?.to?.().name||''))return;
    const request={};automaticNavigation=request;
    const finish=()=>{if(automaticNavigation===request)automaticNavigation=null;};
    try{Promise.resolve(router.go('portainer.workspace',params)).then(finish,error=>{finish();
      // UI-Router's SUPERSEDED rejection means a newer navigation won. Only
      // this expected cancellation is consumed; actual routing errors remain
      // visible with their original diagnostic information.
      if(error?.type!==2)console.error('EdgeStack workspace automatic navigation:',error);
    });}catch(error){finish();throw error;}
  }
  function setMode() {
    const authenticated=!!auth?.isAuthenticated(),preview=authenticated&&isAdmin()&&workspaceRoute();
    document.body.classList.toggle('edgestack-user',authenticated&&(!isAdmin()||preview));
    document.body.classList.toggle('edgestack-admin-preview',preview);
    document.body.classList.remove('edgestack-simple');
  }
  function mascot(standby = false) {
    return `<g class="ew-companion ${standby?'ew-resting':''}" transform="translate(0 0)"><ellipse cx="0" cy="31" rx="24" ry="8" fill="#bbc8bd" opacity=".2"/><path d="M-18 7Q-23 23-12 31h24Q23 23 18 7Z" fill="${standby?'#e1e4df':'#eef0e7'}" stroke="#c9d0c2"/><path d="M-16 26v9M16 26v9" stroke="#9ba79c" stroke-width="7" stroke-linecap="round"/><path d="M-23 9q-10 6-7 14M23 9q10 6 7 14" fill="none" stroke="#cbd4c5" stroke-width="6" stroke-linecap="round"/><rect x="-29" y="-32" width="58" height="43" rx="18" fill="${standby?'#d8ddd5':'#edf0e9'}" stroke="#bcc9b7"/><path d="M-24-24q5-9 19-5h17q12 0 12 12v7q0 11-13 12H-11q-13 0-13-12Z" fill="${standby?'#9fa9a3':'#283d37'}"/><g class="ew-eyes" fill="#edfae5">${standby?'<path d="M-14-11h7M7-11h7" stroke="#e5eae2" stroke-width="2"/>':'<ellipse cx="-10" cy="-13" rx="3" ry="4"/><ellipse cx="10" cy="-13" rx="3" ry="4"/>'}</g><path d="M-4-4q4 3 8 0" stroke="#a7d7ab" stroke-width="1.7" fill="none"/><path d="M-6 13h12v8H-6Z" rx="3" fill="${standby?'#bdc5ba':'#ed9a59'}"/><path d="M0-32v-8" stroke="#adbea5" stroke-width="3"/><circle cx="0" cy="-42" r="4" fill="${standby?'#c2c9be':'#f1ad6e'}"/></g>`;
  }
  function station(d, index, total, columns=null, visible=true) {
    const cols = total===1?1:columns||(total<=4?2:4);
    const x = total === 1 ? 310 : cols === 2 ? 320 + (index % cols) * 340 : 135 + (index % cols) * 235;
    const y = (total === 1 ? 200 : 155) + Math.floor(index / cols) * 235 + (index % cols % 2 ? 22 : 0);
    const count = taskList(d).length, active=taskList(d).filter(t=>t.runtimeState==='running').length;
    const caption=!d.online?'管理连接离线 · 状态待确认':active?`${active} 项任务实例运行中${count>active?' · '+(count-active)+' 项运行待确认':''}`:count?`${count} 项任务已登记`:'管理连接在线 · 暂无任务';
    return `<g class="ew-station ${d.online?'':'ew-offline'}" ${visible?'':'style="visibility:hidden;pointer-events:none" aria-hidden="true"'} transform="translate(${x} ${y}) ${total===1?'scale(1.5)':''}" data-action="device" data-id="${esc(id(d))}" tabindex="${visible?0:-1}" role="button" aria-label="${esc(d.name)}，${d.online?'管理连接在线':'管理连接离线'}，${count} 项任务"><title>${esc(d.name)} · ${count} 项任务</title><ellipse class="ew-station-halo" cx="0" cy="70" rx="105" ry="61" fill="#e9eddf" opacity="0"/><ellipse cx="15" cy="104" rx="108" ry="28" fill="url(#ew-shadow)"/><g class="ew-furniture"><path d="m-87 33 112-28 79 39-113 31Z" fill="#fff" stroke="#dce1d9"/><path d="m-87 33 78 37v9l-78-37Z" fill="#e4e9df"/><path d="m-9 70 113-26v9L-9 79Z" fill="#f2f4ec"/><path d="m-77 44 7 3v58l-7-4Zm168 10 6-2v50l-6 3ZM-6 78l6-2v57l-6 2Z" fill="#d3dccf"/><path d="m-75 101 70 30 99-27" fill="none" stroke="#e6eae1" stroke-width="3"/><path d="m-43-18 60-14v48l-60 14Z" fill="#d6ded2" stroke="#c6d0c1"/><path d="m-39-14 52-12v38l-52 12Z" fill="${d.online?'#dce6d3':'#e4e8e0'}"/><path d="m-35-9 44-10v29l-44 10Z" fill="${d.online?'#334c40':'#aab6a9'}"/><path d="m-29-2 25-6M-29 4-7-1M-29 10-13 6" stroke="${d.online?'#b9d2a5':'#d1d9cd'}" stroke-width="2"/><path d="m-9 24 7-2v10l-7 2Z" fill="#b8c6af"/><path d="m-21 35 21-5 14 6-20 5Z" fill="#d8e0d1"/><path d="m-38 47 39-9 16 7-39 9Z" fill="#e5eade"/><path d="m47 2 21-5 17 9-20 5Z" fill="#f4f5eb"/><path d="m47 2 18 9v34l-18-9Z" fill="#dfe6d6"/><path d="m65 11 20-5v34l-20 5Z" fill="#edf0e4"/><path d="m71 17 8-2M71 22l8-2" stroke="#bac9ae"/><circle cx="73" cy="33" r="2" fill="${d.online?'#79ac75':'#a8b3a0'}"/><path d="m-54 67 37-8 23 12-37 9Z" fill="#b9c8ad"/><path d="m-54 67 2 29 26 11-5-27Z" fill="#cbd6c1"/><path d="m-29 103-2 22m0-3-18-1m18 1 15-10" stroke="#bdcbb2" stroke-width="4" stroke-linecap="round"/></g><g transform="translate(-26 51)">${mascot(!d.online)}</g><g transform="translate(0 -76)" class="ew-node-label"><text text-anchor="middle" class="ew-node-name">${esc(d.name?.length>19?d.name.slice(0,18)+'…':d.name)}</text><text y="21" text-anchor="middle" class="ew-node-caption">${esc(caption)}</text><circle cx="${Math.min((d.name||'').length*6+12,110)}" cy="-5" r="3" fill="${d.online?'#68a277':'#b7bdb2'}"/></g><g class="ew-event-layer" data-device-activity="${esc(id(d))}"></g></g>`;
  }
  function plant(x,y) { return `<g transform="translate(${x} ${y})"><ellipse cy="39" rx="24" ry="8" fill="#cbd5c2" opacity=".22"/><path d="M-16 3h32l-5 33q-11 6-22 0Z" fill="#e4ddd0"/><ellipse cy="3" rx="16" ry="6" fill="#d7cdbb"/><path d="M0 3v-39M0-19q-29 0-24-21Q0-39 0-19ZM0-6q26 0 23-21Q0-25 0-6Z" fill="#9db48a" stroke="#8da67b"/><path d="M0-30q17-2 12-23Q-6-47 0-30Z" fill="#b3c69d"/></g>`; }
  function scene(devices,options={}) {
    const capacity=options.slots?Math.max(devices.length,...devices.map(d=>(options.slots.get(id(d))||0)+1)):devices.length;
    const rows = devices.length===1?1:Math.ceil(capacity/(options.columns||(devices.length<=4?2:4)));
    const h = Math.max(510, rows * 235 + 115), w=devices.length===1?620:1020;
    return `<svg class="ew-scene ${devices.length===1?'ew-scene-single':''}" viewBox="0 0 ${w} ${h}" xmlns="http://www.w3.org/2000/svg" aria-label="设备工作空间，共 ${devices.length} 台已授权设备"><defs><radialGradient id="ew-shadow"><stop stop-color="#a7b29d" stop-opacity=".25"/><stop offset="1" stop-color="#a7b29d" stop-opacity="0"/></radialGradient><pattern id="ew-floor" width="100" height="56" patternUnits="userSpaceOnUse"><path d="M0 28 50 0l50 28-50 28Z" stroke="#e7e9e1" stroke-width=".65" fill="none"/></pattern><linearGradient id="ew-floorfade" x2="0" y2="1"><stop stop-color="#f5f6f0"/><stop offset="1" stop-color="#fafbf6"/></linearGradient></defs><rect x="5" y="1" width="${w-10}" height="${h-2}" rx="28" fill="url(#ew-floorfade)"/><rect x="8" y="10" width="${w-16}" height="${h-20}" rx="28" fill="url(#ew-floor)" opacity=".6"/><path d="M40 72 156 8h54L78 84Z" fill="#fff" opacity=".7"/><path d="m680 8 280 0-240 135-280 0Z" fill="#fff" opacity=".55"/>${plant(w-70,74)}${plant(60,h-84)}<g class="ew-room-sign" transform="translate(54 37)"><circle r="4" fill="#91ab7c"/><text x="13" y="4">EVERY DEVICE, A LITTLE WORLD</text></g>${devices.map((d,i)=>station(d,options.slots?.get(id(d))??i,devices.length,options.columns,options.visible?options.visible.has(id(d)):true)).join('')}<text x="${w-60}" y="${h-24}" text-anchor="end" class="ew-floor-caption">EDGE / SPACE</text></svg>`;
  }
  function Workspace(element, params) {
    const root = element.querySelector('[data-edgestack-workspace]') || element;
    let alive = true, sequence = 0,eventBaseline=false,polling=null,eventGeneration=0;const baselineDevices=new Set();
    const ownerKey=String(auth?.getUserDetails()?.ID||'session')+':'+(isAdmin()?'admin':'user'),memoryKey=ownerKey+':'+normalView(params.view);
    const remembered=workspaceMemory.get(memoryKey)||{query:'',filter:'all',layout:'space',libraryLayout:'details',scene:{x:0,y:0},drawer:{}};
    if(!activityMemory.has(ownerKey))activityMemory.set(ownerKey,{seen:new Set(),seenAt:new Map(),active:new Map(),slots:new Map(),columns:null});
    const activity=activityMemory.get(ownerKey),expiryScheduled=new Set();
    const requests = new Set(), timers = new Set(),sessionHints=new Map();
    const s = {view:normalView(params.view), deviceId:String(params.device || ''), devices:[],loading:true,error:'',notice:'',tab:'overview',query:remembered.query,filter:remembered.filter,layout:remembered.layout,libraryLayout:remembered.libraryLayout,relationsOpen:false,events:[],eventPeriod:remembered.eventPeriod||'24',eventType:remembered.eventType||'all',eventSession:'',eventWindow:null,eventCursors:{},eventCoverage:null,eventCursor:'',eventLoading:false,drawerLoading:false,summary:null,summaryQuestion:'',summaryBusy:false,question:'',period:'24',models:null,modelTaskId:'',preview:null,previousModelLabel:'',operation:null,modelBusy:false,evidence:null};
    const selected = () => s.devices.find(d => id(d) === s.deviceId);
    const allTasks = () => s.devices.flatMap(d => taskList(d).map(t=>({...t,deviceId:id(d),deviceName:d.name,online:d.online})));
    const filteredDevices = () => s.devices.filter(d=>(!s.query || (d.name||'').toLowerCase().includes(s.query.toLowerCase())) && (s.filter==='all'||(s.filter==='online'?d.online:!d.online)));
    function schedule(fn, ms) { const t=setTimeout(()=>{timers.delete(t);if(alive)fn();},ms);timers.add(t); }
    async function api(method,path,body) {
      const controller=new AbortController();requests.add(controller);
      const timeout=setTimeout(()=>controller.abort(),method==='POST'&&path==='summaries'?75000:25000);
      try {
        const response=await fetch(new URL(path.replace(/^\//,''),apiRoot),{method,credentials:'same-origin',signal:controller.signal,headers:body===undefined?{}:{'Content-Type':'application/json'},...(body===undefined?{}:{body:JSON.stringify(body)})});
        if(!response.ok){let data={};try{data=await response.json();}catch{}if(response.status===401)router?.go('portainer.auth');const e=new Error(({401:'登录已过期，请重新登录。',403:'你没有这项操作的权限。',409:'任务已发生变化，请重新选择并预览。',503:'服务暂未连接。请稍后重试。'})[response.status]||data.message||({summary_capacity_reached:'当前生成小结的人较多，请稍后再试。',not_found:'这项资源已移除，或当前账号无权访问。',replacement_unavailable:'模型更换服务暂不可用。',invalid_summary_request:'请选择最近 7 天内的时间范围。',summary_range_too_large:'这个时间范围的记录较多，请缩小范围后重试。',summary_already_in_progress:'你已有一份小结正在生成，请稍后查看。'})[data.errorCode]||data.error||`暂时无法读取（${response.status}）`);e.status=response.status;throw e;}
        return response.status===204?null:response.json();
      } catch(e) {if(e.name==='AbortError')throw new Error('请求超时，请重试。');throw e;}
      finally {requests.delete(controller);clearTimeout(timeout);}
    }
    function go(view,device='') { saveView();if(router)router.go('portainer.workspace',{view,device:device||null});else location.hash=`#!/workspace?view=${view}${device?'&device='+encodeURIComponent(device):''}`; }
    function empty(title,description,small=false) {return `<div class="ew-empty ${small?'ew-empty-small':''}"><svg viewBox="-60 -55 120 110" aria-hidden="true">${mascot(true)}</svg><h3>${esc(title)}</h3><p>${esc(description)}</p></div>`;}
    function error() {return s.error?`<div class="ew-error" role="alert">${icon('info')}<span>${esc(s.error)}</span><button data-action="dismiss" aria-label="关闭提示">${icon('close')}</button></div>`:'';}
    function loading(text='正在走进你的空间…') {return `<div class="ew-loading" role="status"><span></span>${text}</div>`;}
    function coverage(c) {
      if(!c)return '';
      const status=typeof c==='string'?c:c.status||c.state;
      const incomplete=c.complete===false||c.incomplete===true||['partial','incomplete','unavailable','disconnected'].includes(status);
      const gaps={no_source_configured:'尚未连接业务事件来源。',source_disabled:'部分互动来源未启用。',source_disconnected:'部分互动来源暂未连接。',source_error:'部分互动来源连接异常。',summary_input_truncated:'本次仅整理了部分记录。',source_history_unknown:'来源尚未提供完整的历史覆盖信息。',source_history_incomplete:'部分时段尚未收到记录。',source_window_incomplete:'所选时间段包含尚未收到记录的时段。',outside_retention:'所选时间超出当前保留范围。'};
      return incomplete?`<p class="ew-coverage">${icon('info')}${esc(c.message||c.reason||(c.gaps||[]).map(g=>gaps[g]).filter(Boolean).join(' ')||'部分时段尚未收到记录，以下记录可能不完整。')}</p>`:'';
    }
    function emptyInteractions(d=selected(),c=null) {
      const sources=d?interactionSources(d):s.devices.flatMap(interactionSources);
      const unconfigured=sources.length>0&&sources.every(source=>source.enabled===false||['unconfigured','unsupported'].includes(source.status));
      const noSources=c?.sourceCount===0||(!sources.length&&!c?.sourceCount)||unconfigured;
      if(noSources){const kinds=d?taskList(d).map(t=>t.kind):[];const voice=kinds.some(k=>/voice|speech|audio/.test(k)),vision=kinds.some(k=>/camera|vision/.test(k));return empty(voice&&!vision?'语音交互尚未接入':vision&&!voice?'视觉交互尚未接入':'互动来源尚未接入','接入业务事件来源后，这里会显示互动记录。',true);}
      if(c?.complete===false||sources.some(source=>source.enabled===false||['disconnected','error','backpressure'].includes(source.status)))return empty('部分时段尚未收到记录','来源覆盖仍不完整。可以调整时间范围，或稍后刷新查看。',true);
      return empty('这个时间段还很安静','新的业务互动记录会出现在这里。',true);
    }
    function timeline(events,compact=false) {
      if(!events.length)return emptyInteractions();
      return `<ol class="ew-timeline ${compact?'ew-timeline-compact':''}">${events.map(e=>`<li><span class="ew-event-icon">${icon(eventIcon(e))}</span><div><div class="ew-event-meta"><span>${esc(eventRole(e))}${eventPhase(e.phase)?' · '+esc(eventPhase(e.phase)):''}${integrationTest(e)?' <em class="ew-integration-label">接入测试</em>':''}</span><time>${esc(time(e.occurredAt||e.timestamp))}</time></div><p>${esc(eventText(e))}</p>${!compact&&e.deviceName?`<span class="ew-muted">${esc(e.deviceName)}</span>`:''}</div></li>`).join('')}</ol>`;
    }
    function header() {
      const online=s.devices.filter(d=>d.online).length;
      return `<header class="ew-header"><div><div class="ew-eyebrow">YOUR EDGE, TOGETHER${window.EdgeStackDemo?' <span class="ew-demo-label">演示空间 · 模拟数据</span>':''}${isAdmin()?'<span class="ew-demo-label">管理员预览</span>':''}</div><h1>${titles[s.view]}<span class="ew-heading-dot">.</span></h1><p>${({space:'每台设备都有自己的小天地。看看它们今天在忙什么。',tasks:'业务在设备上协作，进展在这里汇聚。',interactions:'留住与你的设备发生的每一次有意义的互动。',library:'了解任务使用的模型，以及数据安放的位置。'})[s.view]}</p></div><div class="ew-header-actions">${isAdmin()?'<a class="ew-native-return" href="#!/home" data-action="native">返回管理后台 '+icon('arrow')+'</a>':''}<button class="ew-icon-button" data-action="refresh" aria-label="刷新空间" title="刷新空间">${icon('refresh')}</button><span class="ew-avatar" aria-label="当前用户">${esc((auth?.getUserDetails()?.Username||auth?.getUserDetails()?.username||'我').slice(0,1).toUpperCase())}</span></div></header>${error()}${s.notice?`<div class="ew-notice" role="status">${icon('check')}${esc(s.notice)}</div>`:''}${s.loading?'':`<div class="ew-space-meta"><span><i class="ew-status-dot"></i>${online} 台管理在线</span><span>${s.devices.length} 台设备</span><span>${allTasks().length} 项业务任务</span><span class="ew-meta-tail">在空间里，了解每一台设备</span></div>`}`;
    }
    function space() {
      if(!s.devices.length)return empty('你的空间，等待第一位伙伴','请联系管理员接入设备并授予访问权限。接入后的设备会在这里拥有自己的工作台。');
      const devices=filteredDevices();
      const allRecent=s.devices.flatMap(d=>(d.recentEvents||[]).map(e=>({...e,deviceId:id(d),deviceName:d.name})));const hasPlayback=allRecent.some(e=>e.type==='voice.playback_state');const recent=allRecent.filter(businessEvent).sort((a,b)=>new Date(b.occurredAt)-new Date(a.occurredAt)).slice(0,4);
      return `<div class="ew-space-layout"><section class="ew-scene-panel"><div class="ew-scene-toolbar"><div class="ew-segment" aria-label="设备筛选">${[['all','全部'],['online','在线'],['offline','离线']].map(([key,label])=>`<button data-action="filter" data-value="${key}" aria-pressed="${s.filter===key}">${label}</button>`).join('')}</div><div class="ew-toolbar-right"><label class="ew-search">${icon('search')}<input name="search" placeholder="找一台设备" value="${esc(s.query)}" aria-label="查找设备"></label><button class="ew-icon-button" data-action="layout" aria-label="${s.layout==='space'?'切换列表视图':'切换空间视图'}" aria-pressed="${s.layout==='list'}">${icon(s.layout==='space'?'list':'space')}</button></div></div><div class="ew-scene-scroll">${!devices.length?empty('没有找到这台设备','试试其他名称或查看全部设备。',true):s.layout==='list'?`<div class="ew-device-list">${devices.map(d=>`<button data-action="device" data-id="${esc(id(d))}"><span class="ew-list-device">${icon('space')}</span><span><strong>${esc(d.name)}</strong><small>${taskList(d).length} 项任务 · 最近互动 ${time(d.lastInteractionAt)}</small></span><span class="ew-status ${d.online?'':'ew-status-off'}">${d.online?'管理连接在线':'管理连接离线'}</span>${icon('arrow')}</button>`).join('')}</div>`:scene(s.devices,{visible:new Set(devices.map(id)),slots:activity.slots,columns:activity.columns})}</div><div class="ew-scene-footer"><span><i class="ew-status-dot"></i>点击工作台，看看设备的小世界</span><span>${devices.length} 台设备 · ${s.layout==='space'?'空间视图':'列表视图'}</span></div></section><aside class="ew-activity"><div class="ew-section-heading"><h2>空间动态</h2><span class="ew-tiny-dot"></span></div><p class="ew-muted ew-small">来自你的设备，而不只是运行状态</p>${recent.length?timeline(recent,true):`<div class="ew-quiet"><svg viewBox="-60 -55 120 110" aria-hidden="true">${mascot(true)}</svg><h3>${hasPlayback?'尚未收到对话或观察内容':'尚未收到互动记录'}</h3><p>${hasPlayback?'播放阶段记录可在互动记录中查看。':'接入设备的业务事件来源后，互动会出现在这里。'}</p></div>`}<button class="ew-text-button" data-action="view" data-value="interactions">查看全部互动 ${icon('arrow')}</button><div class="ew-space-note">${icon('spark')}<p>想知道最近发生了什么？<br>进入设备，按需生成一份小结。</p></div></aside></div>`;
    }
    function tasks() {
      const tasks=allTasks();
      if(!tasks.length)return empty('还没有业务任务','设备已接入后，已识别的业务应用会出现在这里。系统 Agent 不计入业务任务。');
      return `<div class="ew-page-intro"><h2>设备上的业务任务 <span>${tasks.length}</span></h2><p>一个任务可以由一个或多个容器协作完成。</p></div><div class="ew-task-list">${tasks.map((t,i)=>`<button class="ew-task-row" data-action="task" data-id="${esc(t.id)}" data-device="${esc(t.deviceId)}"><span class="ew-task-art tone-${i%3}">${icon(/voice|speech/.test(t.kind)?'audio':/camera|vision/.test(t.kind)?'camera':'tasks')}</span><span class="ew-task-main"><strong>${esc(t.name)}</strong><small>${esc(t.deviceName)} <b>·</b> ${esc(taskKind(t.kind))}</small></span><span class="ew-task-detail">${(t.models||[]).length?esc(t.models.map(m=>m.name||m.packageId).join('、')):'尚未声明模型'}<small>${(t.dataLocations||[]).length} 处登记的数据位置</small></span><span class="ew-task-state"><i class="ew-status-dot ${t.runtimeState==='running'?'':'ew-dot-muted'}"></i>${esc(taskRuntime(t))}</span>${icon('arrow')}</button>`).join('')}</div>`;
    }
    function rememberSessions(events){for(const event of events){if(!event.sessionId)continue;const at=Date.parse(event.occurredAt),hint=sessionHints.get(event.sessionId),user=/voice\.(user_utterance|transcript)$/.test(event.type);if(!hint)sessionHints.set(event.sessionId,{at,text:eventText(event).slice(0,24),user,deviceId:String(event.deviceId)});else{hint.at=Math.min(hint.at,at);if(user&&(!hint.user||at<=hint.at)){hint.text=eventText(event).slice(0,24);hint.user=true;}}}}
    function eventMatches(event){const types=eventTypeGroups[s.eventType];return (!types||types.split(',').includes(event.type))&&(!s.eventSession||event.sessionId===s.eventSession)&&Date.parse(event.occurredAt)>=Date.now()-Number(s.eventPeriod)*3600000;}
    function eventFilters(){const sessions=[...sessionHints.entries()].filter(([,hint])=>(!s.deviceId||hint.deviceId===s.deviceId)&&s.devices.some(d=>id(d)===hint.deviceId)).sort((a,b)=>b[1].at-a[1].at);return `<div class="ew-event-filters" aria-label="互动记录筛选"><label>时间范围<select name="eventPeriod"><option value="24" ${s.eventPeriod==='24'?'selected':''}>最近 24 小时</option><option value="168" ${s.eventPeriod==='168'?'selected':''}>最近 7 天</option></select></label><label>类型<select name="eventType">${[['all','全部'],['conversation','语音对话'],['vision','视觉观察'],['playback','播放状态']].map(([v,l])=>`<option value="${v}" ${s.eventType===v?'selected':''}>${l}</option>`).join('')}</select></label><label>一次会话<select name="eventSession"><option value="">全部会话</option>${s.eventSession&&!sessionHints.has(s.eventSession)?`<option selected value="${esc(s.eventSession)}">已选择的会话</option>`:''}${sessions.map(([key,hint])=>`<option value="${esc(key)}" ${s.eventSession===key?'selected':''}>${esc(time(hint.at)+' · '+hint.text)}</option>`).join('')}</select></label></div>`;}
    function resetEventFilters(){eventGeneration++;s.eventWindow=null;s.eventCursors={};s.eventCursor='';s.events=[];s.eventLoading=false;return loadEvents();}
    function recentTimeline(d){const events=(d.recentEvents||[]).filter(businessEvent);return events.length?timeline(events,true):(d.recentEvents||[]).some(e=>e.type==='voice.playback_state')?empty('尚未收到对话或观察内容','播放阶段记录可在互动记录中查看。',true):emptyInteractions(d);}
    function eventTimeline() {return !s.events.length?emptyInteractions(selected(),s.eventCoverage):timeline(s.events);}
    function interactions() {
      return `<div class="ew-records-top"><div><h2>每一次互动，都有迹可循</h2><p class="ew-muted">仅呈现业务事件中的文本记录。</p></div><label class="ew-select-label">设备<select name="eventsDevice"><option value="">全部设备</option>${s.devices.map(d=>`<option value="${esc(id(d))}" ${id(d)===s.deviceId?'selected':''}>${esc(d.name)}</option>`).join('')}</select></label></div>${eventFilters()}${coverage(s.eventCoverage)}<section class="ew-records">${s.eventLoading?loading('正在整理互动记录…'):eventTimeline()}${s.eventCursor?'<button class="ew-button ew-button-quiet" data-action="more-events">加载更早记录</button>':''}</section>`;
    }
    function modelName(m) {return `${m.name||m.packageId||'未命名模型'}${m.version?' · '+m.version:''}`;}
    function dataLocation(location) {
      const l=location&&typeof location==='object'?location:{path:location||'',verification:'registered'};
      const central=l.kind==='server'||l.type==='server';
      const verified=!central&&l.verification==='verified';
      const kind=({volume:'数据卷',bind:'目录挂载',database:'数据库',file:'文件',local:'本地数据',server:'管理中枢存储'})[l.kind||l.type]||'任务数据';
      const verification=central?'由管理中枢保存 · 按服务端保留设置管理':verified?'已核验实际挂载':'管理员登记，实际挂载待核验';
      return `<div class="ew-data-location" data-verification="${central?'server':verified?'verified':'registered'}">${icon('data')}<span><strong>${esc(l.name||kind)}</strong><code>${esc(l.containerPath||l.path||l.location||l.destination||l.volume||'路径尚未登记')}</code><small class="ew-data-verification">${esc(verification)}</small>${l.description?`<small>${esc(l.description)}</small>`:''}</span></div>`;
    }
    function relationships(devices,compact=false) {
      function locationNodes(rows,fallback){return rows.length?rows.map(l=>dataLocation(l)).join(''):`<p class="ew-relation-unknown">${esc(fallback)}</p>`;}
      return `<section class="ew-relations ${compact?'ew-relations-compact':''}" aria-label="设备任务与数据关系"><header><h3>设备、任务与数据的联系</h3><p>连线表示当前授权范围内的登记关系，位置状态另行标明。</p></header>${devices.map(d=>`<article class="ew-relation-device"><div class="ew-relation-node ew-relation-origin">${icon('space')}<span><strong>${esc(d.name)}</strong><small>${d.online?'管理连接在线':'管理连接离线'}</small></span></div><div class="ew-relation-tasks">${taskList(d).length?taskList(d).map(t=>{const locations=(t.dataLocations||[]).map(l=>typeof l==='object'&&l?l:{path:l});const server=locations.filter(l=>l.kind==='server'||l.type==='server');const model=locations.filter(l=>!server.includes(l)&&['model','model-file','model_files'].includes(l.purpose||l.usage));const business=locations.filter(l=>!server.includes(l)&&['business','business-data','conversation','interaction','events','data'].includes(l.purpose||l.usage));const unknown=locations.filter(l=>!server.includes(l)&&!model.includes(l)&&!business.includes(l));return `<section class="ew-relation-task"><div class="ew-relation-node">${icon('tasks')}<span><strong>${esc(t.name)}</strong><small>${esc(taskKind(t.kind))} · ${esc(taskRuntime(t))}</small></span></div><div class="ew-relation-branches"><div><h4>${icon('data')}业务数据</h4>${locationNodes(business,'业务数据位置待登记')}${unknown.length?`<div class="ew-relation-unclassified"><strong>用途待登记</strong>${locationNodes(unknown,'')}</div>`:''}</div><div><h4>${icon('library')}模型文件</h4>${(t.models||[]).map(m=>`<p class="ew-relation-model"><strong>${esc(modelName(m))}</strong><small>${esc(modelPurpose(m.slot))} · ${esc(stateLabel(m.state))}</small></p>`).join('')}${locationNodes(model,'模型文件位置待登记')}</div><div><h4>${icon('data')}管理中枢记录</h4>${locationNodes(server,'尚未登记管理中枢记录')}</div></div></section>`;}).join(''):'<p class="ew-relation-unknown">尚未登记业务任务</p>'}</div></article>`).join('')}</section>`;
    }
    function libraryMode(){return `<div class="ew-library-mode ew-segment" aria-label="模型与数据视图"><button data-action="library-layout" data-value="details" aria-pressed="${s.libraryLayout!=='relations'}">详情列表</button><button data-action="library-layout" data-value="relations" aria-pressed="${s.libraryLayout==='relations'}">关系视图</button></div>`;}
    function drawerModels(d){return `<button class="ew-text-button ew-relations-toggle" data-action="relations">${icon('library')}${s.relationsOpen?'返回模型与位置详情':'查看设备、任务与数据关系'}</button>${s.relationsOpen?relationships([d],true):taskList(d).length?taskList(d).map(taskModels).join(''):empty('尚无模型与数据声明','这台设备还没有已识别的业务任务。',true)}`;}
    function library() {
      const tasks=allTasks();
      if(s.libraryLayout==='relations')return libraryMode()+relationships(s.devices);
      return `${libraryMode()}<div class="ew-page-intro"><h2>模型有来处，数据有归处</h2><p>模型状态来自任务记录；数据位置显示管理员登记及核验情况。</p></div>${!tasks.length?empty('尚无模型与数据声明','业务任务接入后，这里会显示它使用的模型和登记的数据位置。'):tasks.map(t=>`<section class="ew-library-section"><header><span class="ew-task-art">${icon('library')}</span><div><h3>${esc(t.name)}</h3><p>${esc(t.deviceName)}</p></div><button class="ew-text-button" data-action="task" data-id="${esc(t.id)}" data-device="${esc(t.deviceId)}">查看与管理 ${icon('arrow')}</button></header><div class="ew-library-columns"><div><h4>任务模型</h4>${t.models?.length?t.models.map(m=>`<div class="ew-model-line">${icon('library')}<span><strong>${esc(modelName(m))}</strong><small>${esc(modelPurpose(m.slot))} · ${esc(stateLabel(m.state||'unknown'))}</small>${m.observedAt&&time(m.observedAt)!=='尚无记录'?`<small>上次确认 ${esc(time(m.observedAt))}</small>`:''}</span></div>`).join(''):'<p class="ew-muted">此任务尚未声明模型信息。</p>'}</div><div><h4>登记的数据位置</h4>${t.dataLocations?.length?t.dataLocations.map(dataLocation).join(''):'<p class="ew-muted">此任务尚未登记数据位置。</p>'}</div></div></section>`).join('')}`;
    }
    function sourceCoverage(){const c=s.sourceCoverage;if(!Array.isArray(c?.gaps)||!c.gaps.includes('outside_retention'))return coverage(c);const gaps=c.gaps.filter(g=>g!=='outside_retention');return coverage({...c,gaps,complete:gaps.length?c.complete:true});}
    function drawerSources(d) {
      const sources=interactionSources(d),observers=(d.sources||[]).filter(source=>source.type==='model.observer');
      const row=(source,observer=false)=>`<div class="ew-source"><span class="ew-source-icon">${icon(observer?'library':eventIcon(source))}</span><span><strong>${esc(source.name||(observer?'模型状态观察':'业务事件来源'))}</strong><small>${observer?(time(source.lastSeenAt)!=='尚无记录'?'最近联系 '+time(source.lastSeenAt):'尚未收到来源状态'):(time(source.lastEventAt)!=='尚无记录'?'最近记录 '+time(source.lastEventAt):'尚无互动记录')}</small></span><span class="ew-status ${source.enabled!==false&&['connected','online','healthy','streaming','ready','active'].includes(source.status)?'':'ew-status-off'}">${source.enabled===false?'未启用':sourceState(source.status)}</span></div>`;
      return `<section class="ew-drawer-section"><h3>互动来源 <span>${sources.length}</span></h3>${sources.length?sources.map(source=>row(source)).join(''):emptyInteractions(d)}${sourceCoverage()}${observers.length?`<div class="ew-model-observers"><h4>模型确认来源</h4><p class="ew-muted ew-small">用于确认模型更新结果。</p>${observers.map(source=>row(source,true)).join('')}</div>`:''}</section>`;
    }
    function voiceTest(t) {
      if(!t.capabilities?.includes('voice-test')||!t.voiceTestUrl)return '';
      let url;try{url=new URL(t.voiceTestUrl,document.baseURI);if(url.origin!==location.origin||!['http:','https:'].includes(url.protocol))return '';}catch{return '';}
      return `<div class="ew-voice-test"><a class="ew-button ew-button-quiet" href="${esc(url.href)}" target="_blank" rel="noopener">${icon('audio')}语音接入测试</a><small>录音转文字 · 回声播放</small></div>`;
    }
    function taskModels(t) {
      const active=s.modelTaskId===String(t.id);
      return `<section class="ew-drawer-section"><div class="ew-section-heading"><h3>${esc(t.name)}</h3><span class="ew-task-kind">${esc(taskKind(t.kind))}</span></div><p class="ew-runtime-fact">${esc(taskRuntime(t))}${t.runtimeObservedAt?'<small>观测 '+esc(time(t.runtimeObservedAt))+'</small>':''}</p><h4>当前模型</h4>${t.models?.length?t.models.map(m=>`<div class="ew-model-line">${icon('library')}<span><strong>${esc(modelName(m))}</strong><small>${esc(modelPurpose(m.slot))} · ${esc(stateLabel(m.state||'unknown'))}</small>${m.observedAt&&time(m.observedAt)!=='尚无记录'?`<small>上次确认 ${esc(time(m.observedAt))}</small>`:''}</span></div>`).join(''):'<p class="ew-muted">任务尚未声明模型。</p>'}${voiceTest(t)}${t.models?.length?`<button class="ew-button ew-button-quiet" data-action="models" data-id="${esc(t.id)}" ${s.modelBusy?'disabled':''}>选择兼容模型 ${icon('arrow')}</button>`:''}${active&&s.models?`<div class="ew-model-picker"><h4>兼容的已发布模型</h4>${!s.models.canReplaceModel?`<p class="ew-muted">${esc(({task_not_configured_for_replacement:'此任务尚未配置模型更换能力。',no_compatible_packages_or_target_unavailable:'暂时没有兼容模型，或目标任务暂不可用。'})[s.models.reason]||s.models.reason||'此任务暂不支持更换模型。')}</p>`:items(s.models).length?items(s.models).map(m=>`<button class="ew-package" data-action="preview-model" data-id="${esc(m.id)}" data-slot="${esc(m.slot||t.models?.[0]?.slot||'default')}" ${s.modelBusy?'disabled':''}><span>${icon('library')}<strong>${esc(modelName(m))}</strong><small>${m.requiresRestart?'切换时任务将短暂重启':'按任务兼容策略切换'}</small></span>${icon('arrow')}</button>`).join(''):'<p class="ew-muted">暂时没有可用的兼容模型。</p>'}</div>`:''}<h4>登记的数据位置</h4>${t.dataLocations?.length?t.dataLocations.map(dataLocation).join(''):'<p class="ew-muted">任务尚未登记数据位置。</p>'}</section>`;
    }
    function summaryResult() {
      const r=s.summary;if(!r)return '';
      if(['pending','queued','running','processing'].includes(r.status))return loading('DeepSeek 正在阅读这个时间段的互动…');
      if(r.status==='no_data')return `<div class="ew-summary-result"><h4>这个时间段还没有可整理的互动</h4><p>连接业务事件来源后，记录会出现在这里。可以换个时间范围再看看。</p>${coverage(r.coverage)}</div>`;
      if(r.status==='failed')return `<div class="ew-summary-result"><h4>这次小结未能完成</h4><p>${esc(r.message||({provider_not_configured:'管理员尚未配置 DeepSeek 服务。',provider_authentication_failed:'DeepSeek 服务认证未通过，请联系管理员检查配置。',provider_timeout:'DeepSeek 响应超时，请稍后重试。',provider_rate_limited:'DeepSeek 当前请求较多，请稍后重试。',provider_invalid_evidence:'这次小结没有通过事件依据校验，请重新生成。',provider_unavailable:'DeepSeek 暂时无法连接，请稍后重试。',provider_output_truncated:'这次生成的内容达到长度上限，请缩小时间范围后重试。',provider_invalid_response:'这次小结的格式无效，请重新生成。',insufficient_evidence:'这个时间段没有足够的互动记录。'})[r.errorCode]||r.errorCode||'请稍后重试。')}</p></div>`;
      return `<div class="ew-summary-result"><div class="ew-summary-result-label">${icon('spark')} DEEPSEEK · 互动小结</div><h4>${esc(r.title||'这段时间，发生了这些事')}</h4>${r.summary?`<p>${esc(r.summary)}</p>`:''}${r.answer&&s.summaryQuestion?`<div class="ew-answer"><strong>关于你的问题</strong><p>${esc(r.answer)}</p></div>`:''}${r.topics?.length?`<ul>${r.topics.map(t=>`<li>${esc(typeof t==='string'?t:t.text)}${t.eventRefs?.length?` <button class="ew-evidence-link" data-action="evidence" data-refs="${esc(t.eventRefs.join(','))}">查看依据</button>`:''}</li>`).join('')}</ul>`:''}${r.uncertainties?.length?`<p class="ew-coverage">${icon('info')}${esc(Array.isArray(r.uncertainties)?r.uncertainties.join('；'):r.uncertainties)}</p>`:''}${coverage(r.coverage)}${r.evidence?.length?`<details class="ew-evidence"><summary>查看 ${r.evidence.length} 条事件依据</summary>${r.evidence.map(e=>`<blockquote id="ew-evidence-${esc(e.ref)}"><p>${esc(e.text)}</p><small>${esc(e.ref)} · ${esc(time(e.occurredAt))}</small></blockquote>`).join('')}</details>`:''}</div>`;
    }
    function summaryPanel() {return `<section class="ew-summary-panel"><div class="ew-section-heading"><h3>${icon('spark')} 为这台设备写一份小结</h3><span>DeepSeek</span></div><p>只在你点击后，整理所选时间内的业务互动文本。</p><div class="ew-summary-form"><label>时间范围<select name="period">${[['24','最近 24 小时'],['168','最近 7 天']].map(([v,l])=>`<option value="${v}" ${s.period===v?'selected':''}>${l}</option>`).join('')}</select></label><label>想了解什么 <span>选填</span><textarea name="question" rows="2" maxlength="2000" placeholder="例如：今天大家最关心什么？">${esc(s.question)}</textarea></label><button class="ew-button" data-action="summarize" ${s.summaryBusy?'disabled':''}>${icon('spark')}${s.summaryBusy?'正在生成…':s.question?'提问并生成小结':'生成互动小结'}</button></div>${summaryResult()}</section>`;}
    function drawer() {
      if(!s.deviceId||s.view==='interactions')return '';
      const d=selected();
      return `<div class="ew-drawer-backdrop" data-action="close-drawer"></div><aside class="ew-drawer" role="dialog" aria-modal="true" aria-label="${esc(d?.name||'设备详情')}" tabindex="-1"><header class="ew-drawer-header"><div class="ew-drawer-device-art"><svg viewBox="-60 -55 120 110" aria-hidden="true">${mascot(!d?.online)}</svg></div><div><p class="ew-eyebrow">A LITTLE WORLD</p><h2>${esc(d?.name||'正在读取设备')}</h2><p><span class="ew-status ${d?.online?'':'ew-status-off'}">${d?.online?'管理连接在线':'管理连接离线'}</span> <span class="ew-muted">${taskList(d).length} 项业务任务</span></p></div><button class="ew-icon-button ew-drawer-close" data-action="close-drawer" aria-label="关闭设备详情">${icon('close')}</button></header><nav class="ew-drawer-tabs" aria-label="设备内容">${[['overview','概览'],['interactions','互动记录'],['models','模型与数据']].map(([key,label])=>`<button data-action="tab" data-value="${key}" aria-current="${s.tab===key?'page':'false'}">${label}</button>`).join('')}</nav><div class="ew-drawer-body" data-scroll-key="${esc(s.deviceId+':'+s.tab)}">${error()}${s.drawerLoading?loading('正在读取设备的小世界…'):!d?empty('没有找到这台设备','它可能已被移除，或当前账号没有访问权限。',true):s.tab==='overview'?`<div class="ew-device-note"><i class="ew-status-dot ${d.online?'':'ew-dot-muted'}"></i><span>${d.online?'管理连接在线，业务状态请查看任务记录':'管理连接离线，业务状态待确认'}<small>最近联系 ${esc(time(d.lastSeenAt))}</small></span></div><section class="ew-drawer-section"><h3>设备任务 <span>${taskList(d).length}</span></h3>${taskList(d).length?taskList(d).map(t=>`<button class="ew-drawer-task" data-action="task-tab" data-id="${esc(t.id)}"><span class="ew-task-art">${icon(/voice|speech/.test(t.kind)?'audio':/camera|vision/.test(t.kind)?'camera':'tasks')}</span><span><strong>${esc(t.name)}</strong><small>${esc(taskRuntime(t))} · ${(t.models||[]).length} 个模型</small><small>登记状态：${esc(stateLabel(t.state))}${t.runtimeObservedAt?' · 观测 '+esc(time(t.runtimeObservedAt)):''}</small></span>${icon('arrow')}</button>${voiceTest(t)}`).join(''):empty('暂无业务任务','设备在线接入后，已识别的业务应用会在这里出现。',true)}</section>${drawerSources(d)}<section class="ew-drawer-section"><div class="ew-section-heading"><h3>最近互动</h3><button class="ew-text-button" data-action="tab" data-value="interactions">全部 ${icon('arrow')}</button></div>${recentTimeline(d)}</section>`:s.tab==='interactions'?`${summaryPanel()}${eventFilters()}${coverage(s.eventCoverage)}<section class="ew-drawer-section"><h3>互动时间线</h3>${s.eventLoading?loading('正在读取互动记录…'):eventTimeline()}${s.eventCursor?'<button class="ew-button ew-button-quiet" data-action="more-events">加载更早记录</button>':''}</section>`:drawerModels(d)}${s.preview?previewPanel():''}${s.operation?operationPanel():''}</div></aside>`;
    }
    function previousModelName(record){const task=taskList(selected()).find(t=>String(t.id)===String(record.taskId||s.modelTaskId)),binding=task?.models?.find(m=>m.slot===record.slot&&m.packageId===record.previousPackageId);return binding?modelName(binding):items(s.models).find(m=>m.id===record.previousPackageId)?.name||record.previousPackageId;}
    function modelEffect(value){const text=typeof value==='string'?value:value.text||value.description;return ({
      'Update only the registered service image and approved model environment values':'仅更新已登记服务的镜像与已批准的模型环境参数。',
      'Restart the task; retain all volumes, ports, networks, and unrelated environment values':'重启任务；保留全部数据卷、端口、网络及其他环境参数。',
      'Wait for a trusted runtime receipt before marking the model loaded':'收到可信的运行时回执后，才将模型标记为已加载。',
    })[text]||text;}
    function previewPanel() {const p=s.preview;return `<div class="ew-model-confirm" role="region" aria-label="模型替换预览"><span class="ew-summary-result-label">变更预览</span><h3>确认后，再交给设备</h3><dl><dt>目标任务</dt><dd>${esc(taskList(selected()).find(t=>String(t.id)===s.modelTaskId)?.name||p.taskId)}</dd>${p.previousPackageId?`<dt>变更前模型</dt><dd>${esc(s.previousModelLabel||previousModelName(p))}<small class="ew-package-id">${esc(p.previousPackageId)}</small></dd>`:''}<dt>新模型</dt><dd>${esc(p.packageName||items(s.models).find(m=>m.id===p.packageId)?.name||p.packageId)}</dd><dt>任务影响</dt><dd>${p.requiresRestart?'任务会短暂重启':'按任务声明的兼容策略应用'}</dd></dl>${p.effects?.length?`<ul>${p.effects.map(e=>`<li>${esc(modelEffect(e))}</li>`).join('')}</ul>`:''}<p class="ew-muted">只更新已声明的模型位置。任务配置在提交时再次校验。</p><div class="ew-inline-actions"><button class="ew-button ew-button-quiet" data-action="cancel-preview" ${s.modelBusy?'disabled':''}>取消</button><button class="ew-button" data-action="confirm-model" ${s.modelBusy?'disabled':''}>${s.modelBusy?'正在提交…':'确认替换模型'}</button></div></div>`;}
    function operationPanel() {const o=s.operation,success=['succeeded','completed','applied'].includes(o.status),stage=success?3:o.status==='awaiting_observation'?2:1;return `<div class="ew-operation" role="status">${icon(success?'check':'info')}<span><strong>${esc(stateLabel(o.status))}</strong><small>${esc(o.message||(success?'模型已按设备报告更新。':o.status==='failed'?'变更没有完成，请重新检查任务状态。':'变更已提交，等待设备报告结果。'))}</small>${o.previousPackageId?`<small>变更前模型：${esc(s.previousModelLabel||o.previousPackageId)} · ${esc(o.previousPackageId)}</small>`:''}<ol class="ew-operation-stages" aria-label="模型更新阶段">${['提交方案','等待设备确认','确认模型状态'].map((label,i)=>`<li class="${o.status==='failed'?'ew-stage-failed':i+1<stage||success?'ew-stage-done':i+1===stage?'ew-stage-current':''}"><i></i>${label}</li>`).join('')}</ol></span></div>`;}
    function saveView(){
      const scene=root.querySelector('.ew-scene-scroll');if(scene)remembered.scene={x:scene.scrollLeft,y:scene.scrollTop};
      const drawer=root.querySelector('.ew-drawer-body');if(drawer)remembered.drawer[drawer.dataset.scrollKey]=drawer.scrollTop;
      Object.assign(remembered,{query:s.query,filter:s.filter,layout:s.layout,libraryLayout:s.libraryLayout,eventPeriod:s.eventPeriod,eventType:s.eventType});workspaceMemory.set(memoryKey,remembered);
    }
    function updatePart(name,html){const slot=root.querySelector(`[data-ew-part="${name}"]`);if(slot.__ewHtml===html)return;
      if(name==='content'&&slot.querySelector('.ew-space-layout')&&html.includes('class="ew-space-layout"')){const temp=document.createElement('div');temp.innerHTML=html;for(const selector of ['.ew-scene-panel','.ew-activity']){const old=slot.querySelector(selector),next=temp.querySelector(selector);const cacheKey=selector==='.ew-scene-panel'?'__ewPanelHtml':'__ewActivityHtml';if(old&&next&&slot[cacheKey]!==next.outerHTML)old.replaceWith(next);if(next)slot[cacheKey]=next.outerHTML;}}else{slot.innerHTML=html;if(name==='content'){slot.__ewPanelHtml=slot.querySelector('.ew-scene-panel')?.outerHTML;slot.__ewActivityHtml=slot.querySelector('.ew-activity')?.outerHTML;}}
      slot.__ewHtml=html;
    }
    function syncActivities(){
      const at=Date.now();for(const [deviceId,a] of activity.active){if(a.expires<=at)activity.active.delete(deviceId);}
      root.querySelectorAll('[data-device-activity]').forEach(layer=>{const a=activity.active.get(layer.dataset.deviceActivity);if(!a){layer.replaceChildren();return;}if(layer.firstElementChild?.dataset.eventId===a.eventId)return;const node=document.createElementNS('http://www.w3.org/2000/svg','g');node.dataset.eventId=a.eventId;node.classList.add('ew-live-event',a.kind==='camera'?'ew-live-camera':'ew-live-voice');node.style.setProperty('--ew-activity-elapsed',`-${at-a.started}ms`);node.setAttribute('role','status');node.setAttribute('aria-label',a.label+'：'+a.text);node.innerHTML=`<path class="ew-sync-path" d="M65 32Q20 33-22 0"/><circle class="ew-sync-light" cx="65" cy="32" r="6"/><g class="ew-event-bubble"><rect x="-96" y="-140" width="192" height="42" rx="10"/><path d="m-9-98 8 8 8-8"/><text x="-84" y="-124" class="ew-event-bubble-label">${esc(a.label)}</text><text x="-84" y="-110" class="ew-event-bubble-text">${esc(a.text.length>21?a.text.slice(0,20)+'…':a.text)}</text></g>`;layer.replaceChildren(node);});
      for(const [deviceId,a] of activity.active){const key=deviceId+':'+a.eventId;if(!expiryScheduled.has(key)){expiryScheduled.add(key);schedule(()=>{expiryScheduled.delete(key);if(activity.active.get(deviceId)?.eventId===a.eventId){activity.active.delete(deviceId);syncActivities();}},Math.max(1,a.expires-at));}}
    }
    async function readEventPages(devices,limit=100,recent=false,more=false){
      const filters={type:eventTypeGroups[s.eventType],session:s.eventSession,window:s.eventWindow};const responses=[];for(let offset=0;offset<devices.length;offset+=4){const batch=devices.slice(offset,offset+4);const settled=await Promise.allSettled(batch.map(d=>{const q=new URLSearchParams({deviceId:id(d),limit:String(limit)});if(recent){const at=Date.now();q.set('from',new Date(at-90000).toISOString());q.set('to',new Date(at+5000).toISOString());}else{if(filters.window){q.set('from',filters.window.from);q.set('to',filters.window.to);}if(filters.type)q.set('type',filters.type);if(filters.session)q.set('sessionId',filters.session);}if(more&&s.eventCursors[id(d)])q.set('cursor',s.eventCursors[id(d)]);return api('GET','events?'+q);}));settled.forEach((r,i)=>responses.push({device:batch[i],result:r}));}return responses;
    }
    async function pollEvents(){
      if(polling||!alive||s.loading)return polling;polling=(async()=>{try{const pages=await readEventPages(s.devices,100,true);if(!alive)return;let changed=false;for(const page of pages){if(page.result.status!=='fulfilled')continue;const did=id(page.device),wasBaseline=baselineDevices.has(did);for(const event of items(page.result.value)){const key=event.eventId||event.id;if(!key||activity.seen.has(key))continue;activity.seen.add(key);activity.seenAt.set(key,Date.now());if(!wasBaseline)continue;const d=s.devices.find(d=>id(d)===String(event.deviceId));if(!d)continue;d.recentEvents=[event,...(d.recentEvents||[]).filter(e=>(e.eventId||e.id)!==key)].sort((a,b)=>new Date(b.occurredAt)-new Date(a.occurredAt)).slice(0,10);rememberSessions([event]);if((s.view==='interactions'||s.tab==='interactions')&&(!s.deviceId||s.deviceId===String(event.deviceId))&&eventMatches(event))s.events=[event,...s.events.filter(e=>(e.eventId||e.id)!==key)].sort((a,b)=>new Date(b.occurredAt)-new Date(a.occurredAt));changed=true;const at=Date.now(),occurred=Date.parse(event.occurredAt),received=Date.parse(event.receivedAt);if(/^(voice|camera)\./.test(event.type)&&Number.isFinite(occurred)&&Number.isFinite(received)&&at-occurred>=-5000&&at-occurred<45000&&at-received>=-5000&&at-received<45000)activity.active.set(String(event.deviceId),{eventId:key,started:at,expires:at+6500,label:liveLabel(event),text:eventText(event),kind:event.type.startsWith('camera')?'camera':'voice'});}baselineDevices.add(did);}
      eventBaseline=true;for(const [key,at] of activity.seenAt)if(Date.now()-at>120000){activity.seen.delete(key);activity.seenAt.delete(key);}if(changed)render();syncActivities();}catch{/* Keep existing evidence during a background read failure. */}})();try{await polling;}finally{polling=null;}
    }
    async function pollLoop(){await pollEvents();if(alive)schedule(pollLoop,5000);}
    function render() {
      if(!alive)return;
      saveView();
      if(!root.querySelector('[data-ew-part]'))root.innerHTML='<div data-ew-part="header"></div><div data-ew-part="content"></div><div data-ew-part="footer"></div><div data-ew-part="drawer"></div>';
      const focused=document.activeElement;const focusName=root.contains(focused)?focused?.name:null;const selection=focusName&&typeof focused.selectionStart==='number'?focused.selectionStart:null;
      updatePart('header',header());updatePart('content',s.loading?loading():s.error&&!s.devices.length?empty('暂时还看不到这个空间','读取设备时遇到了问题。点击右上角刷新，再试一次。'):({space,tasks,interactions,library})[s.view]());
      updatePart('footer',`<footer class="ew-footer"><span>EdgeStack<span class="ew-brand-dot">.</span> <span class="ew-muted">让边缘设备成为日常的伙伴</span></span><span class="ew-build">${window.EdgeStackDemo?'离线演示 · 所有设备与互动均为模拟数据':'状态来自已授权设备与业务事件'}</span></footer>`);updatePart('drawer',drawer());
      const scene=root.querySelector('.ew-scene-scroll');if(scene){scene.scrollLeft=remembered.scene.x;scene.scrollTop=remembered.scene.y;}
      const drawerBody=root.querySelector('.ew-drawer-body');if(drawerBody)drawerBody.scrollTop=remembered.drawer[drawerBody.dataset.scrollKey]||0;
      syncActivities();
      document.body.classList.toggle('edgestack-drawer-open',!!s.deviceId&&s.view!=='interactions');
      if(focusName){const next=root.querySelector(`[name="${focusName}"]`);next?.focus({preventScroll:true});if(selection!==null&&next?.setSelectionRange)next.setSelectionRange(selection,selection);}
    }
    async function refresh() {
      const run=++sequence;s.error='';
      try { const data=await api('GET','devices');if(!alive||run!==sequence)return;s.devices=items(data);for(const d of s.devices)if(!activity.slots.has(id(d)))activity.slots.set(id(d),activity.slots.size);if(!activity.columns&&s.devices.length>1)activity.columns=s.devices.length<=4?2:4;s.loading=false;render();if(s.deviceId&&s.view!=='interactions')await loadDevice();if(s.view==='interactions')await loadEvents();if(eventBaseline)await pollEvents(); }
      catch(e){if(alive&&run===sequence){s.error=e.message;s.loading=false;render();}}
    }
    async function loadDevice() {
      const current=s.deviceId;if(!current)return;s.drawerLoading=true;render();
      try {const results=await Promise.allSettled([api('GET','devices/'+encodeURIComponent(current)),api('GET','devices/'+encodeURIComponent(current)+'/event-sources')]);if(!alive||s.deviceId!==current)return;if(results[0].status==='rejected')throw results[0].reason;const d=results[0].value;const at=s.devices.findIndex(x=>id(x)===current);if(at<0)s.devices.push(d);else s.devices[at]=d;if(results[1].status==='fulfilled'){d.sources=items(results[1].value);s.sourceCoverage=results[1].value.coverage;}else{s.sourceCoverage={complete:false,message:'互动来源暂时无法读取，请稍后刷新。'};}}
      catch(e){s.error=e.message;}finally{if(alive&&s.deviceId===current){s.drawerLoading=false;render();if(s.tab==='interactions')loadEvents();}}
    }
    async function loadEvents(more=false) {
      if(s.eventLoading)return;const current=s.deviceId,generation=eventGeneration;if(!s.eventWindow){const at=Date.now();s.eventWindow={from:new Date(at-Number(s.eventPeriod)*3600000).toISOString(),to:new Date(at).toISOString()};}s.eventLoading=true;render();
      try{const devices=s.devices.filter(d=>(!current||id(d)===current)&&(!more||s.eventCursors[id(d)]));const pages=await readEventPages(devices,30,false,more);if(!alive||s.deviceId!==current||generation!==eventGeneration)return;const successful=pages.filter(p=>p.result.status==='fulfilled');if(pages.length&&!successful.length)throw pages[0].result.reason;const records=successful.flatMap(p=>items(p.result.value));rememberSessions(records);s.events=(more?[...s.events,...records]:[...records,...s.events.filter(e=>eventMatches(e)&&Date.parse(e.occurredAt)>Date.parse(s.eventWindow.to))]).filter((e,i,a)=>a.findIndex(x=>(x.eventId||x.id)===(e.eventId||e.id))===i).sort((a,b)=>new Date(b.occurredAt)-new Date(a.occurredAt));if(!more)s.eventCursors={};successful.forEach(p=>s.eventCursors[id(p.device)]=p.result.value.nextCursor||'');s.eventCursor=Object.values(s.eventCursors).some(Boolean)?'more':'';const coverage=successful.map(p=>p.result.value.coverage).filter(Boolean);s.eventCoverage=coverage.length?{sourceCount:coverage.reduce((n,c)=>n+(c.sourceCount||0),0),availableSources:coverage.reduce((n,c)=>n+(c.availableSources||0),0),complete:successful.length===pages.length&&coverage.every(c=>c.complete!==false),gaps:[...new Set(coverage.flatMap(c=>c.gaps||[]))]}:{sourceCount:0,complete:false,gaps:['no_source_configured']};}
      catch(e){if(generation===eventGeneration)s.error=e.message;}finally{if(alive&&generation===eventGeneration){s.eventLoading=false;render();}}
    }
    async function pollSummary(summaryId) {
      try{const r=await api('GET','summaries/'+encodeURIComponent(summaryId));if(!alive)return;s.summary=r;s.summaryBusy=['queued','pending','running','processing'].includes(r.status);render();if(s.summaryBusy)schedule(()=>pollSummary(summaryId),1500);}
      catch(e){if(alive){s.summaryBusy=false;s.error=e.message;render();}}
    }
    async function summarize() {
      if(s.summaryBusy)return;const question=s.question.trim();s.summaryQuestion=question;s.summaryBusy=true;s.summary=null;render();
      try{const to=new Date();const from=new Date(to.getTime()-Number(s.period)*3600000);let timeZone='Asia/Shanghai';try{timeZone=Intl.DateTimeFormat().resolvedOptions().timeZone||timeZone;}catch(_){}const r=await api('POST','summaries',{deviceId:s.deviceId,from:from.toISOString(),to:to.toISOString(),question,timeZone});if(!alive)return;s.summary=r;s.summaryBusy=['queued','pending','running','processing'].includes(r.status);if(s.summaryBusy)schedule(()=>pollSummary(r.id),1000);}
      catch(e){s.error=e.message;s.summaryBusy=false;}finally{render();}
    }
    async function pollOperation(operationId) {
      try{const r=await api('GET','operations/'+encodeURIComponent(operationId));if(!alive)return;s.operation=r;render();if(!['succeeded','completed','applied','failed','cancelled','rejected'].includes(r.status))schedule(()=>pollOperation(operationId),1800);else if(['succeeded','completed','applied'].includes(r.status))loadDevice();}
      catch(e){if(alive){s.error=e.message;render();}}
    }
    async function modelAction(action,b) {
      if(s.modelBusy)return;s.modelBusy=true;s.error='';render();
      try{
        if(action==='models'){s.modelTaskId=b.dataset.id;s.models=await api('GET','tasks/'+encodeURIComponent(s.modelTaskId)+'/models');s.preview=null;}
        if(action==='preview-model'){const task=taskList(selected()).find(t=>String(t.id)===s.modelTaskId);s.preview=await api('POST','tasks/'+encodeURIComponent(s.modelTaskId)+'/model-change-preview',{packageId:b.dataset.id,slot:b.dataset.slot,revision:s.models.taskRevision||task?.revision});s.previousModelLabel=s.preview.previousPackageId?previousModelName(s.preview):'';}
        if(action==='confirm-model'&&s.preview){s.operation=await api('POST','tasks/'+encodeURIComponent(s.modelTaskId)+'/model-changes',{previewId:s.preview.id,confirm:true});s.preview=null;s.models=null;schedule(()=>pollOperation(s.operation.id),1200);}
      }catch(e){s.error=e.message;if(e.status===409){s.preview=null;s.models=null;}}finally{s.modelBusy=false;render();}
    }
    async function click(event) {
      const b=event.target.closest('[data-action]');if(!b||!root.contains(b))return;const action=b.dataset.action;
      if(action==='device')return go(s.view,b.dataset.id);
      if(action==='close-drawer')return go(s.view);
      if(action==='view')return go(b.dataset.value);
      if(action==='native'){setMode(false);return;}
      if(action==='dismiss')s.error='';
      if(action==='filter')s.filter=b.dataset.value;
      if(action==='library-layout')s.libraryLayout=b.dataset.value;
      if(action==='relations')s.relationsOpen=!s.relationsOpen;
      if(action==='layout')s.layout=s.layout==='space'?'list':'space';
      if(action==='refresh')return refresh();
      if(action==='task') {if(s.deviceId===b.dataset.device){s.tab='models';s.modelTaskId=b.dataset.id;}else{return go('library',b.dataset.device);}}
      if(action==='task-tab'){s.tab='models';s.modelTaskId=b.dataset.id;}
      if(action==='tab'){s.tab=b.dataset.value;if(s.tab==='interactions'){render();return loadEvents();}}
      if(action==='more-events')return loadEvents(true);
      if(action==='summarize')return summarize();
      if(['models','preview-model','confirm-model'].includes(action))return modelAction(action,b);
      if(action==='cancel-preview')s.preview=null;
      if(action==='evidence'){const details=root.querySelector('.ew-evidence');if(details){details.open=true;details.scrollIntoView({behavior:matchMedia('(prefers-reduced-motion: reduce)').matches?'auto':'smooth',block:'nearest'});}return;}
      render();
    }
    function input(event) {if(event.target.name==='question')s.question=event.target.value;if(event.target.name==='search'){s.query=event.target.value;render();}}
    function change(event){if(['eventPeriod','eventType','eventSession'].includes(event.target.name)){s[event.target.name]=event.target.value;resetEventFilters();return;}if(event.target.name==='period')s.period=event.target.value;if(event.target.name==='eventsDevice'){s.deviceId=event.target.value;s.eventSession='';go('interactions',s.deviceId);}}
    function keydown(event) {
      if(event.key==='Escape'&&s.deviceId&&s.view!=='interactions'){event.preventDefault();go(s.view);}
      if(['Enter',' '].includes(event.key)&&event.target.matches('g[data-action]')){event.preventDefault();event.target.dispatchEvent(new MouseEvent('click',{bubbles:true}));}
      if(event.key==='Tab'&&s.deviceId&&s.view!=='interactions'){const drawer=root.querySelector('.ew-drawer');const nodes=[...drawer.querySelectorAll('button:not(:disabled),a,input,select,textarea,summary,[tabindex="0"]')];if(!nodes.length)return;if(!drawer.contains(document.activeElement)){event.preventDefault();nodes[0].focus();return;}if(event.shiftKey&&document.activeElement===nodes[0]){event.preventDefault();nodes.at(-1).focus();}else if(!event.shiftKey&&document.activeElement===nodes.at(-1)){event.preventDefault();nodes[0].focus();}}
    }
    root.addEventListener('click',click);root.addEventListener('input',input);root.addEventListener('change',change);document.addEventListener('keydown',keydown);
    if(s.view==='library'&&s.deviceId)s.tab='models';
    render();refresh().then(()=>{if(s.deviceId&&s.view!=='interactions')root.querySelector('.ew-drawer-close')?.focus();pollLoop();});
    return ()=>{saveView();alive=false;sequence++;requests.forEach(c=>c.abort());timers.forEach(clearTimeout);root.removeEventListener('click',click);root.removeEventListener('input',input);root.removeEventListener('change',change);document.removeEventListener('keydown',keydown);document.body.classList.remove('edgestack-drawer-open');root.replaceChildren();};
  }
  function nav() {
    const side=document.querySelector('#sideview');if(!auth.isAuthenticated()){document.body.classList.remove('edgestack-user','edgestack-admin-preview');workspaceMemory.clear();activityMemory.clear();side?.querySelector('[data-edgestack-nav]')?.remove();return;}if(!side)return;
    let menu=side.querySelector('[data-edgestack-nav]');
    if(isAdmin()&&!workspaceRoute()){
      menu?.remove();let preview=side.querySelector('[data-edgestack-preview]');
      if(!preview){preview=document.createElement('a');preview.dataset.edgestackPreview='';preview.className='ew-admin-preview';preview.href='#!/workspace?view=space';preview.title='用户视图预览';preview.setAttribute('aria-label','用户视图预览');preview.innerHTML=icon('space')+'<span>用户视图预览</span>';}
      const host=side.querySelector('.sidebar')||side;if(preview.parentElement!==host)host.append(preview);
      setMode(workspaceRoute());return;
    }
    side.querySelector('[data-edgestack-preview]')?.remove();
    if(!menu){menu=document.createElement('nav');menu.dataset.edgestackNav='';menu.setAttribute('aria-label','EdgeStack 工作空间');menu.innerHTML=`<a class="ew-nav-brand" aria-label="EdgeStack 我的空间" href="#!/workspace?view=space"><span class="ew-brand-mark">${icon('space')}</span><span>EdgeStack<span class="ew-brand-dot">.</span><small>你的边缘工作空间</small></span></a><div class="ew-nav-caption">${isAdmin()?'管理员预览':'工作空间'}</div>${views.map(v=>`<a class="ew-nav-item" aria-label="${titles[v]}" title="${titles[v]}" data-view="${v}" href="#!/workspace?view=${v}">${icon(v==='library'?'library':v)}<span>${titles[v]}</span>${v==='space'?'<span class="ew-nav-live"></span>':''}</a>`).join('')}<div class="ew-nav-garden"><svg viewBox="-60 -55 120 110" aria-hidden="true">${mascot()}</svg><strong>小小设备，大有作为</strong><p>你的每一份工作，<br>都在自己的空间里成长。</p></div><div class="ew-nav-bottom">${isAdmin()?'<a class="ew-nav-item" href="#!/home">'+icon('arrow')+'<span>返回管理后台</span></a>':''}<a class="ew-nav-item" aria-label="我的账号" title="我的账号" href="#!/account">${icon('user')}<span>我的账号</span></a><a class="ew-nav-item" aria-label="退出登录" title="退出登录" href="#!/logout">${icon('logout')}<span>退出登录</span></a><div class="ew-nav-footnote">CONNECTED TO YOUR EVERYDAY</div></div>`;side.prepend(menu);}
    const current=normalView(router.params?.view||routeParams().view);menu.querySelectorAll('[data-view]').forEach(a=>{const active=workspaceRoute()&&a.dataset.view===current;if(active)a.setAttribute('aria-current','page');else a.removeAttribute('aria-current');});setMode(workspaceRoute());
  }
  function register() {
    if(!window.angular)return false;
    injector=window.angular.element(document.querySelector('[ng-app]')||document.documentElement).injector();if(!injector)return false;
    router=injector.get('$state');auth=injector.get('Authentication');const registry=injector.get('$stateRegistry');
    function registerRoute(name,url){if(!registry.get(name))registry.register({name,url,data:{access:'restricted'},views:{'content@':{template:'<main data-edgestack-workspace class="ew-workspace"></main>',controller:['$element','$scope','$stateParams',function($element,$scope,$stateParams){setMode(true);nav();const destroy=Workspace($element[0],$stateParams);$scope.$on('$destroy',destroy);}]}}});}
    registerRoute('portainer.workspace','/workspace?view&device');registerRoute('portainer.edgestack','/edgestack?view&device&app');
    injector.get('$transitions').onSuccess({},transition=>{nav();if(transition.to().name==='portainer.home'&&!isAdmin())openWorkspaceAutomatically({view:'space',device:null});});
    let queued=false;new MutationObserver(()=>{if(!queued){queued=true;requestAnimationFrame(()=>{queued=false;nav();});}}).observe(document.querySelector('#sideview')||document.body,{childList:true,subtree:true});
    nav();
    if(/^#!\/(?:workspace|edgestack)(?:\?|$)/.test(startupHash)&&auth.isAuthenticated()){const q=new URLSearchParams(startupHash.split('?')[1]||'');openWorkspaceAutomatically({view:normalView(q.get('view')),device:q.get('device')||null});}
    else if(router.current.name==='portainer.home'&&!isAdmin())openWorkspaceAutomatically({view:'space',device:null});
    window.EdgeStackWorkspace={version:buildVersion,mount:Workspace};return true;
  }
  // The mount hook also powers isolated offline tests; no authentication bypass is
  // installed. Production instances are mounted only by the restricted router.
  window.EdgeStackWorkspaceUI={mount:Workspace,version:buildVersion};
  const start=setInterval(()=>{try{if(register())clearInterval(start);}catch(error){console.error('EdgeStack workspace initialization:',error);clearInterval(start);}},50);
  setTimeout(()=>clearInterval(start),30000);
})();
