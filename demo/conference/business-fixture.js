/* Explicit offline conference fixtures. Never imported by the production UI or
 * backend. Everything in this module is invented demonstration data. */
(function () {
  'use strict';
  const copy = value => JSON.parse(JSON.stringify(value));
  function seed() {
    const ago = minutes => new Date(Date.now() - minutes * 60000).toISOString();
    const packages = [
      {id:'demo-voice-v1',name:'轻语 · 日常对话',version:'1.2',kind:'voice',slot:'llm',taskKinds:['voice'],published:true,immutable:true,requiresRestart:true,digest:'sha256:'+'a'.repeat(64),demo:true},
      {id:'demo-voice-v2',name:'轻语 · 新版日常对话',version:'1.3',kind:'voice',slot:'llm',taskKinds:['voice'],published:true,immutable:true,requiresRestart:true,digest:'sha256:'+'b'.repeat(64),demo:true},
      {id:'demo-camera-v1',name:'微光 · 视觉观察',version:'2.0',kind:'camera',slot:'vision',taskKinds:['camera'],published:true,immutable:true,requiresRestart:true,digest:'sha256:'+'c'.repeat(64),demo:true},
      {id:'demo-camera-v2',name:'微光 · 视觉观察',version:'2.1',kind:'camera',slot:'vision',taskKinds:['camera'],published:true,immutable:true,requiresRestart:true,digest:'sha256:'+'d'.repeat(64),demo:true},
    ];
    const devices = ['客厅的时光盒','前台小助手','书房的观察员','窗边的伙伴','工作室 · 一号','厨房小耳朵','走廊的守望者','阳台观察站'].map((name,i)=>({id:'demo-device-'+(i+1),name,online:![3,6].includes(i),lastSeenAt:ago([3,6].includes(i)?180:0),tasks:[],sources:[],recentEvents:[],lastInteractionAt:null,demo:true}));
    function task(deviceIndex,kind,name,number=1){
      const d=devices[deviceIndex],tid='demo-task-'+(deviceIndex+1)+'-'+number;
      const model=packages.find(p=>p.kind===kind);
      const t={id:tid,deviceId:d.id,name,kind,state:'registered',runtimeState:d.online?'running':'unknown',runtimeObservedAt:d.online?ago(0):undefined,revision:1,models:model?[{slot:model.slot,packageId:model.id,name:model.name,version:model.version,observedPackageId:d.online?model.id:undefined,state:d.online?'loaded':'unobserved',observedAt:d.online?ago(0):undefined}]:[],dataLocations:[{name:'任务数据（演示）',path:'/demo/data/'+tid,kind:'volume',purpose:'business',verification:'registered'},...(model?[{name:'模型文件（演示）',containerPath:'/demo/models/'+tid,kind:'bind',purpose:'model',verification:'registered'}]:[]),{name:'互动记录（演示）',kind:'server',location:'本地模拟事件记录',verification:'registered'}],capabilities:model?['replace-model']:[],demo:true};
      d.tasks.push(t);
      if(kind!=='web')d.sources.push({id:'demo-source-'+tid,deviceId:d.id,taskId:tid,name:kind==='voice'?'语音互动来源（演示）':'视觉观察来源（演示）',type:kind==='voice'?'voice.user_utterance':'camera.observation',enabled:true,status:d.online?'connected':'disconnected',lastSeenAt:d.lastSeenAt,lastEventAt:null,demo:true});
      return t;
    }
    task(0,'voice','日常语音助手');task(1,'camera','来访观察');task(2,'web','设备便笺');task(3,'voice','窗边的语音伙伴');task(5,'voice','厨房小助手');task(6,'camera','走廊观察');task(7,'camera','植物观察');task(7,'voice','阳台语音便笺',2);
    const events=[];
    function event(di,type,text,minutes,ti=0){const d=devices[di],t=d.tasks[ti],e={schemaVersion:1,eventId:'demo-event-'+(events.length+1),deviceId:d.id,taskId:t.id,sourceId:d.sources.find(x=>x.taskId===t.id)?.id,type,phase:type==='voice.user_utterance'?'final':type==='voice.assistant_reply'?'generated':type==='voice.playback_state'?'played':undefined,occurredAt:ago(minutes),receivedAt:ago(minutes),sessionId:'demo-session-'+di,sequence:events.length+1,text,provenance:{kind:'offline-fixture'},demo:true};events.push(e);if(type!=='voice.playback_state')d.lastInteractionAt=e.occurredAt;const source=d.sources.find(x=>x.id===e.sourceId);if(source)source.lastEventAt=e.occurredAt;}
    event(0,'voice.user_utterance','帮我记住，周末给阳台的植物浇水。',4);
    event(0,'voice.assistant_reply','已记好周末的小计划。希望植物们也有愉快的一天。',3);
    event(0,'voice.playback_state','语音回复已播放完成。（离线样例）',2);
    event(1,'camera.observation','今天的第一位访客到了，前台已准备就绪。',8);
    event(1,'camera.observation','前台区域已经恢复安静。',6);
    event(7,'camera.observation','阳台光线明亮，植物叶片状态已记录。',12);
    event(7,'voice.user_utterance','记一条便笺：明天把薄荷移到窗边。',10,1);
    events.sort((a,b)=>new Date(b.occurredAt)-new Date(a.occurredAt));
    return {schema:2,fixture:true,description:'本地生成的虚构会议数据；不对应实际设备、模型或互动。',devices,events,packages,summaries:{},previews:{},operations:{}};
  }
  function create({db,save,audit,jsonResponse}) {
    if(!db.demoBusiness||db.demoBusiness.schema!==2){db.demoBusiness=seed();save();}
    const b=db.demoBusiness;
    const fail=(status,errorCode)=>jsonResponse({errorCode,fixture:true},status);
    const tasks=()=>b.devices.flatMap(d=>d.tasks);
    const findTask=tid=>tasks().find(t=>t.id===tid);
    function device(d){const out=copy(d);if(d.id==='demo-device-1'){out.online=db.boardOnline;out.lastSeenAt=new Date(Date.now()-(db.boardOnline?0:180000)).toISOString();}out.tasks.forEach(t=>{if(out.online&&t.runtimeState==='running')t.runtimeObservedAt=new Date().toISOString();else{t.runtimeState='unknown';delete t.runtimeObservedAt;}});out.recentEvents=b.events.filter(e=>e.deviceId===d.id).slice(0,5);return out;}
    function sources(did){return b.devices.filter(d=>!did||d.id===did).flatMap(d=>device(d).sources);}
    function coverage(did,from,to,count){const rows=sources(did).filter(s=>/^(?:voice|camera)(?:\.|$)/.test(s.type));const gaps=!rows.length?['no_source_configured']:rows.some(s=>s.enabled===false||!['connected','healthy','streaming'].includes(s.status))?['source_disconnected']:[];return {from,to,retentionDays:7,eventCount:count,sourceCount:rows.length,availableSources:rows.filter(s=>s.enabled!==false&&['connected','healthy','streaming'].includes(s.status)).length,gaps,complete:!gaps.length};}
    function query(q){const did=q.get('deviceId')||'',from=q.get('from')||new Date(Date.now()-7*86400000).toISOString(),to=q.get('to')||new Date().toISOString();return {did,from,to,rows:b.events.filter(e=>(!did||e.deviceId===did)&&e.occurredAt>=from&&e.occurredAt<=to).sort((a,b)=>Date.parse(b.occurredAt)-Date.parse(a.occurredAt)||b.eventId.localeCompare(a.eventId))};}
    function route(method,path,q,body){
      if(!path.startsWith('/edgestack/'))return null;
      if(!db.loggedIn)return fail(401,'authentication_required');
      if(path==='/edgestack/devices'&&method==='GET')return jsonResponse({items:b.devices.map(device),fixture:true});
      let m=path.match(/^\/edgestack\/devices\/([^/]+)(?:\/(event-sources))?$/);
      if(m){const d=b.devices.find(x=>x.id===m[1]);if(!d)return fail(404,'not_found');return m[2]?jsonResponse({items:device(d).sources,coverage:coverage(d.id,null,null,b.events.filter(e=>e.deviceId===d.id).length),fixture:true}):jsonResponse(device(d));}
      if(path==='/edgestack/events'&&method==='GET'){if(!q.get('deviceId'))return fail(400,'device_required');if(!b.devices.some(d=>d.id===q.get('deviceId')))return fail(404,'not_found');const types=(q.get('type')||'').split(',').filter(Boolean),supported=['voice.user_utterance','voice.assistant_reply','voice.playback_state','camera.observation'];if(types.some(type=>!supported.includes(type)))return fail(400,'invalid_event_type');const x=query(q);if(!Number.isFinite(Date.parse(x.from))||!Number.isFinite(Date.parse(x.to))||Date.parse(x.to)<=Date.parse(x.from)||Date.parse(x.to)-Date.parse(x.from)>7*86400000)return fail(400,'invalid_time_range');const matching=x.rows.filter(e=>(!types.length||types.includes(e.type))&&(!q.get('sessionId')||e.sessionId===q.get('sessionId')));const filteredCount=matching.length;let cursor=0;if(q.get('cursor')){const at=matching.findIndex(e=>e.eventId===q.get('cursor'));if(at<0)return fail(400,'invalid_cursor');cursor=at+1;}const limit=Math.min(Number(q.get('limit')||30),100);const rows=matching.slice(cursor,cursor+limit);return jsonResponse({items:rows,nextCursor:cursor+limit<matching.length?rows.at(-1).eventId:null,coverage:coverage(x.did,x.from,x.to,x.rows.length),filteredCount,fixture:true});}
      m=path.match(/^\/edgestack\/tasks\/([^/]+)\/(models|model-change-preview|model-changes)$/);
      if(m){const t=findTask(m[1]);if(!t)return fail(404,'not_found');const compatible=b.packages.filter(p=>p.taskKinds.includes(t.kind)&&t.models.some(binding=>binding.slot===p.slot));
        if(m[2]==='models'&&method==='GET')return jsonResponse({items:device(b.devices.find(d=>d.id===t.deviceId)).online?compatible:[],taskRevision:t.revision,canReplaceModel:device(b.devices.find(d=>d.id===t.deviceId)).online&&!!compatible.length&&t.capabilities.includes('replace-model'),reason:!device(b.devices.find(d=>d.id===t.deviceId)).online?'no_compatible_packages_or_target_unavailable':compatible.length?'':'task_not_configured_for_replacement',fixture:true});
        if(m[2]==='model-change-preview'&&method==='POST'){const p=compatible.find(p=>p.id===body.packageId);if(!device(b.devices.find(d=>d.id===t.deviceId)).online)return fail(409,'target_unavailable');if(!p||!t.capabilities.includes('replace-model'))return fail(409,'incompatible_package');if(body.revision!==t.revision)return fail(409,'stale_revision');if(body.slot!==p.slot)return fail(409,'incompatible_slot');const preview={previousPackageId:t.models.find(binding=>binding.slot===p.slot)?.packageId||'',id:'demo-preview-'+Date.now()+'-'+Object.keys(b.previews).length,taskId:t.id,packageId:p.id,slot:p.slot,revision:t.revision,target:{endpointId:4,stackId:1,serviceId:'demo'},requiresRestart:p.requiresRestart,effects:['这是本地演示变更，不会操作任何实际设备。','示例任务会短暂重启，并保留原有数据位置。'],expiresAt:new Date(Date.now()+300000).toISOString(),fixture:true};b.previews[preview.id]=preview;save();return jsonResponse(preview,201);}
        if(m[2]==='model-changes'&&method==='POST'){const p=b.previews[body.previewId];if(!p||p.taskId!==t.id||body.confirm!==true)return fail(400,'invalid_confirmation');if(p.operationId)return jsonResponse(b.operations[p.operationId],202);if(p.revision!==t.revision||p.expiresAt<new Date().toISOString())return fail(409,'stale_revision');const op={previousPackageId:p.previousPackageId,id:'demo-operation-'+Date.now()+'-'+Object.keys(b.operations).length,taskId:t.id,deviceId:t.deviceId,packageId:p.packageId,slot:p.slot,revision:t.revision+1,status:'awaiting_observation',createdAt:new Date().toISOString(),readyAt:Date.now()+1000,fixture:true};p.operationId=op.id;b.operations[op.id]=op;t.runtimeState='unknown';delete t.runtimeObservedAt;audit('离线模拟：确认模型替换',t.name);return jsonResponse(op,202);}
      }
      m=path.match(/^\/edgestack\/operations\/([^/]+)$/);
      if(m&&method==='GET'){const op=b.operations[m[1]];if(!op)return fail(404,'not_found');if(op.status==='awaiting_observation'&&Date.now()>=op.readyAt&&device(b.devices.find(d=>d.id===op.deviceId)).online){const t=findTask(op.taskId),p=b.packages.find(p=>p.id===op.packageId);op.status='succeeded';op.updatedAt=new Date().toISOString();op.message='离线演示：已模拟设备确认模型更新。';t.revision=op.revision;t.runtimeState='running';t.runtimeObservedAt=new Date().toISOString();t.models=t.models.map(binding=>binding.slot===op.slot?{...binding,packageId:p.id,observedPackageId:p.id,name:p.name,version:p.version,state:'loaded',observedAt:new Date().toISOString()}:binding);save();}return jsonResponse(op);}
      if(path==='/edgestack/summaries'&&method==='POST'){if(!b.devices.some(d=>d.id===body.deviceId)||!body.from||!body.to)return fail(400,'invalid_summary_request');const x=query(new URLSearchParams({deviceId:body.deviceId,from:body.from,to:body.to}));const evidence=x.rows.filter(e=>e.type!=='voice.playback_state').map(e=>({ref:'evt:'+e.eventId,eventId:e.eventId,text:e.text,occurredAt:e.occurredAt,type:e.type}));const summary={id:'demo-summary-'+Date.now()+'-'+Object.keys(b.summaries).length,status:evidence.length?'completed':'no_data',title:'离线演示小结 · 本地样例',summary:evidence.length?'这个时间段记录了 '+evidence.length+' 条业务互动。该小结由离线样例生成，未调用 DeepSeek 云服务。':'这个时间段还没有可整理的业务互动。',topics:evidence.slice(0,4).map(e=>({text:e.text,eventRefs:[e.ref]})),answer:body.question?'离线演示回答：关于“'+String(body.question).slice(0,200)+'”，可以依据下方事件记录继续了解。此回答为本地预设示例。':'',uncertainties:['离线演示内容来自虚构样例，不代表实际设备观察。'],coverage:coverage(x.did,x.from,x.to,x.rows.length),evidence,fixture:true};b.summaries[summary.id]=summary;audit('离线模拟：生成互动小结',body.deviceId);return jsonResponse(summary,201);}
      m=path.match(/^\/edgestack\/summaries\/([^/]+)$/);if(m&&method==='GET')return b.summaries[m[1]]?jsonResponse(b.summaries[m[1]]):fail(404,'not_found');
      return fail(404,'demo_business_route_not_supported');
    }
    function emitDemoEvent(kind){const d=b.devices.find(d=>d.tasks.some(t=>t.kind===kind)),t=d?.tasks.find(t=>t.kind===kind);if(!d||!t)return;const stamp=new Date().toISOString(),source=d.sources.find(s=>s.taskId===t.id);const event={schemaVersion:1,eventId:'demo-live-'+Date.now()+'-'+b.events.length,deviceId:d.id,taskId:t.id,sourceId:source?.id,type:kind==='camera'?'camera.observation':'voice.user_utterance',phase:kind==='camera'?'observed':'final',occurredAt:stamp,receivedAt:stamp,sessionId:'demo-live-session',sequence:b.events.length+1,text:kind==='camera'?'离线模拟：前台收到一条新的观察记录。':'离线模拟：刚刚说了一句“今天的计划是什么？”',provenance:{kind:'offline_fixture'},demo:true};b.events.unshift(event);d.lastInteractionAt=stamp;if(source)source.lastEventAt=stamp;audit('离线模拟：产生新的互动样例',d.name);return event;}
    return {route,emitDemoEvent};
  }
  window.EdgeStackDemoBusiness={create,seed};
})();
