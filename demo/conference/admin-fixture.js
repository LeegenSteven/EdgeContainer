/* Persistent, invented offline administrator data. Loaded only by the standalone
 * conference builder. No network, microphone, deployment or cloud integration.
 * Candidate keys and one-time credential values are never persisted. */
(function () {
  'use strict';
  const copy = x => JSON.parse(JSON.stringify(x));
  const stamp = () => new Date().toISOString();
  const meta = () => ({metadataVersion:1,description:'本地虚构演示登记，不对应实际部署。',archived:false});
  const acl = () => ({userIds:[10001],teamIds:[]});
  function create({db,save,audit,jsonResponse,business}) {
    const b = db.demoBusiness;
    const tasks = business.tasks;
    const findTask = business.findTask;
    const sources = () => b.devices.flatMap(d => d.sources);
    const findDevice = id => b.devices.find(d => d.id === id);
    const defaults = (item, fields) => { for (const [key,value] of Object.entries(fields)) if (item[key] === undefined) item[key] = copy(value); };
    // Migrate old user-only fixture saves without replacing user changes.
    b.devices.forEach(d => defaults(d,{...meta(),...acl(),endpointId:4,architecture:'arm64'}));
    tasks().forEach((t,i) => {
      defaults(t,{...meta(),...acl(),testTask:i===0,runtimeBackend:'demo-runtime',templateId:t.kind==='camera'?'demo-camera-v1':'demo-voice-v1',modelEnvKeys:[],target:{endpointId:4,stackId:101+i,serviceId:'app'}});
      // Each task has a separate synthetic single-device Stack; the original
      // native conference Stack remains intact.
      if (db.edge_stacks && !db.edge_stacks.some(s => s.Id === t.target.stackId)) {
        const base=copy(db.edge_stacks[0]||{});
        db.edge_stacks.push({...base,Id:t.target.stackId,Name:'演示业务 · '+t.name,EdgeGroups:[1],StackFileContent:JSON.stringify({services:{[t.target.serviceId]:{image:'example.invalid/demo-task:offline',environment:{DEMO_MODE:'true'}}}},null,2),demo:true});
      }
    });
    sources().forEach(s => defaults(s,meta()));
    b.packages.forEach(p => defaults(p,{...meta(),architectures:['arm64'],runtimeBackend:'demo-runtime',templateIds:[p.kind==='camera'?'demo-camera-v1':'demo-voice-v1'],image:'example.invalid/demo-model@'+p.digest,modelId:p.id+'-weights',modelHash:p.digest,environment:{},validationStatus:'passed',validatedScopes:[{taskKind:p.kind,architecture:'arm64',runtimeBackend:'demo-runtime',templateId:p.kind==='camera'?'demo-camera-v1':'demo-voice-v1'}]}));
    if (!db.demoAdmin || db.demoAdmin.schema !== 1) {
      const template=b.packages.find(p=>p.id==='demo-voice-v2');
      if (!b.packages.some(p=>p.id==='demo-voice-draft')) b.packages.unshift({...copy(template),...meta(),id:'demo-voice-draft',name:'轻语 · 待验证草稿（演示）',version:'1.4-demo',published:false,validationStatus:'draft',validatedScopes:[],image:'example.invalid/demo-model@sha256:'+'e'.repeat(64),digest:'sha256:'+'e'.repeat(64),modelId:'demo-draft-weights',modelHash:'sha256:'+'e'.repeat(64)});
      db.demoAdmin={schema:1,fixture:true,credentials:[],validations:[],audit:[],imports:{},cloud:{metadataVersion:1,provider:'deepseek',model:'demo-summary',source:'hostfile',hasKey:true,availableModels:['demo-summary','demo-reasoning'],fixture:true}};
      // Explicit simulated native permission, separate from business ACL.
      const endpoint=db.environments?.find(e=>e.Id===4);
      if(endpoint){endpoint.UserAccessPolicies=endpoint.UserAccessPolicies||{};endpoint.UserAccessPolicies[10001]={RoleId:1};}
    }
    const a=db.demoAdmin;
    const fail=(code,status=400)=>jsonResponse({errorCode:code,fixture:true},status);
    const output=(value,status=200)=>jsonResponse(copy(value),status);
    const id=prefix=>prefix+'-'+Date.now()+'-'+Math.random().toString(36).slice(2,8);
    function record(action,kind,resource,fields=[],result='success') {
      a.audit.unshift({id:id('demo-audit'),actorUserId:1,action,resourceType:kind,resourceId:resource.id,deviceId:kind==='devices'?resource.id:resource.deviceId,taskId:kind==='tasks'?resource.id:resource.taskId,occurredAt:stamp(),result,fields:[...fields],fixture:true});
      a.audit=a.audit.slice(0,500);audit('离线业务模拟：'+action,resource.name||resource.id);save();
    }
    function nativeAccess(endpointId,userId=10001) {
      const user=db.users?.find(u=>u.Id===userId);
      if(user?.Role===1)return true;
      const e=db.environments?.find(e=>e.Id===endpointId);if(!e)return null;
      const teamIds=(db.memberships||[]).filter(m=>m.UserID===userId).map(m=>m.TeamID);
      const group=db.endpoint_groups?.find(g=>g.Id===e.GroupId);
      return [e,group].filter(Boolean).some(x=>Object.prototype.hasOwnProperty.call(x.UserAccessPolicies||{},userId)||teamIds.some(t=>Object.prototype.hasOwnProperty.call(x.TeamAccessPolicies||{},t)));
    }
    business.setNativeAccess(endpointId=>nativeAccess(endpointId));
    function businessAccess(x,userId) {const teamIds=(db.memberships||[]).filter(m=>m.UserID===userId).map(m=>m.TeamID);return !!x&&!x.archived&&((x.userIds||[]).includes(userId)||(x.teamIds||[]).some(t=>teamIds.includes(t)));}
    function table(kind){return ({devices:b.devices,tasks:tasks(),sources:sources(),models:b.packages,'ingest-credentials':a.credentials,operations:Object.values(b.operations),audit:a.audit})[kind];}
    function settle(){Object.values(b.operations).forEach(business.settleOperation);}
    function list(items,q) {
      let rows=items.slice();
      for(const key of ['deviceId','taskId','actorUserId','result'])if(q.get(key))rows=rows.filter(x=>String(x[key])===q.get(key)||Array.isArray(x[key+'s'])&&x[key+'s'].includes(q.get(key)));
      for(const key of ['published','archived'])if(q.has(key)&&q.get(key)!=='')rows=rows.filter(x=>!!x[key]===(q.get(key)==='true'));
      if(q.get('status'))rows=rows.filter(x=>[x.status,x.state,x.validationStatus].includes(q.get('status')));
      if(q.get('q')){const term=q.get('q').toLocaleLowerCase();rows=rows.filter(x=>[x.name,x.id,x.description].some(v=>String(v||'').toLocaleLowerCase().includes(term)));}
      if(q.get('from'))rows=rows.filter(x=>(x.occurredAt||x.createdAt||'')>=q.get('from'));
      if(q.get('to'))rows=rows.filter(x=>(x.occurredAt||x.createdAt||'')<=q.get('to'));
      const page=Math.max(1,Number(q.get('page'))||1),pageSize=Math.min(100,Math.max(1,Number(q.get('pageSize'))||25));
      return output({items:rows.slice((page-1)*pageSize,page*pageSize),totalCount:rows.length,page,pageSize,fixture:true});
    }
    const fields={devices:['name','description','userIds','teamIds','archived'],tasks:['name','description','userIds','teamIds','archived','dataLocations','capabilities','modelEnvKeys','voiceOrigin','voiceTokenFile','testTask','runtimeBackend','templateId'],sources:['name','description','enabled','archived']};
    const configFields=['capabilities','modelEnvKeys','voiceOrigin','voiceTokenFile','testTask','runtimeBackend','templateId'];
    function validModel(p){return !!(p.id&&p.name&&p.version&&p.kind&&p.slot&&p.modelId&&p.runtimeBackend&&p.taskKinds?.length&&p.architectures?.length&&p.templateIds?.length&&/^\S+@sha256:[a-f0-9]{64}$/i.test(p.image||'')&&/^sha256:[a-f0-9]{64}$/i.test(p.modelHash||'')&&p.published!==true);}
    function validateCreate(kind,value,pending={}) {
      if(!value.id||!value.name)return 'invalid_registration';
      if(table(kind)?.some(x=>x.id===value.id))return 'object_exists';
      if(kind==='devices'&&(!db.environments?.some(e=>e.Id===Number(value.endpointId))||!value.architecture))return 'invalid_device';
      if(kind==='tasks'){
        const d=findDevice(value.deviceId)||(pending.devices||[]).find(d=>d.id===value.deviceId);if(!d||d.archived||!value.kind||!value.target?.stackId||!value.target.serviceId||value.target.endpointId!==d.endpointId)return 'invalid_task';
        const stack=db.edge_stacks?.find(s=>s.Id===Number(value.target.stackId));if(!stack)return 'invalid_target';
        for(const binding of value.models||[])if(![...b.packages,...(pending.models||[])].some(p=>p.id===binding.packageId&&p.slot===binding.slot))return 'incompatible_package';
      }
      const sourceTask=findTask(value.taskId)||(pending.tasks||[]).find(t=>t.id===value.taskId);
      if(kind==='sources'&&(!sourceTask||sourceTask.deviceId!==value.deviceId||!['voice.events','voice.user_utterance','camera.observation','model.observer'].includes(value.type)))return 'invalid_source';
      if(kind==='models'&&!validModel(value))return 'invalid_model_package';
      return '';
    }
    function add(kind,value) {
      const item={...copy(value),...meta(),fixture:true,demo:true};
      if(kind==='devices')Object.assign(item,{...acl(),...copy(value),endpointId:Number(value.endpointId),tasks:[],sources:[],recentEvents:[],online:db.boardOnline,lastSeenAt:stamp()});
      if(kind==='tasks')Object.assign(item,{...acl(),...copy(value),revision:1,state:'registered',runtimeState:'unknown',models:(value.models||[]).map(binding=>{const p=b.packages.find(x=>x.id===binding.packageId);return {...binding,name:p?.name,version:p?.version,state:'unobserved'};}),dataLocations:(value.dataLocations||[]).map(loc=>({...loc,verification:'registered'})),capabilities:value.capabilities||[],modelEnvKeys:value.modelEnvKeys||[]});
      if(kind==='sources')Object.assign(item,{enabled:true,...copy(value),status:'unconfigured'});
      if(kind==='models')Object.assign(item,{published:false,immutable:true,validationStatus:'draft',validatedScopes:[],environment:value.environment||{},legacyScoped:false,digest:value.modelHash});
      if(kind==='tasks')findDevice(item.deviceId).tasks.push(item);
      else if(kind==='sources')findDevice(item.deviceId).sources.push(item);
      else table(kind).push(item);
      return item;
    }
    function patch(kind,item,body) {
      const changed=fields[kind].filter(key=>body[key]!==undefined&&JSON.stringify(body[key])!==JSON.stringify(item[key]));
      if(kind==='tasks'&&changed.some(k=>configFields.includes(k))&&Object.values(b.operations).some(op=>op.taskId===item.id&&!['succeeded','failed'].includes(op.status)))return 'operation_in_progress';
      changed.forEach(key=>item[key]=copy(body[key]));
      if(changed.includes('dataLocations'))item.dataLocations=item.dataLocations.map(loc=>({...loc,verification:'registered'}));
      if(kind==='tasks'&&changed.some(k=>configFields.includes(k)))item.revision++;
      if(changed.length)item.metadataVersion++;
      return changed;
    }
    function credential(body,old) {
      const scope=old||body;
      if(!(scope.sourceIds||[]).length||!(scope.capabilities||[]).length||scope.sourceIds.some(sid=>!sources().some(s=>s.id===sid)))return fail('invalid_ingest_credential');
      const c={id:id('demo-credential'),name:body.name||old?.name||'演示上报凭据',sourceIds:copy(scope.sourceIds),taskIds:copy(scope.taskIds||[]),deviceIds:copy(scope.deviceIds||[]),capabilities:copy(scope.capabilities),revoked:false,createdAt:stamp(),createdByUserId:1,fixture:true};
      if(old)c.rotatedFromId=old.id;
      a.credentials.unshift(c);record(old?'换发演示凭据':'创建演示凭据','ingest-credentials',c,['name','sourceIds','taskIds','deviceIds','capabilities']);
      // This is a visibly invalid local sample, not a usable ingest secret.
      return output({id:c.id,token:'DEMO_OFFLINE_ONLY_'+c.id,credential:c,fixture:true},201);
    }
    function preview(t,p,purpose) {
      if(!t||!p||t.archived||!findDevice(t.deviceId)||findDevice(t.deviceId).archived)return fail('target_unavailable',409);
      if(purpose==='model-validation'&&(!t.testTask||!t.runtimeBackend||!t.templateId))return fail('validation_requires_test_task',409);
      if(!business.device(findDevice(t.deviceId)).online)return fail('target_unavailable',409);
      if(!business.compatible(t,p,true))return fail('incompatible_package',409);
      if(Object.values(b.operations).some(op=>op.taskId===t.id&&!['succeeded','failed'].includes(op.status)))return fail('operation_in_progress',409);
      const plan={id:id('demo-preview'),taskId:t.id,packageId:p.id,previousPackageId:t.models.find(m=>m.slot===p.slot)?.packageId||'',slot:p.slot,revision:t.revision,target:copy(t.target),requiresRestart:!!p.requiresRestart,purpose,expiresAt:new Date(Date.now()+300000).toISOString(),effects:['离线模拟：只更新示例任务的模型登记。','离线模拟：保留数据位置、端口与网络；不部署到任何实际设备。','离线模拟：轮询后生成虚构运行回执，不执行真实推理。'],fixture:true};
      b.previews[plan.id]=plan;save();return output(plan,201);
    }
    function cloud(method,sub,body) {
      const c=a.cloud;if(method==='GET'&&!sub)return output(c);
      if(!['test','restore-hostfile',''].includes(sub)||!['POST','PATCH','PUT'].includes(method))return fail('demo_admin_route_not_supported',404);
      if(body.metadataVersion!==c.metadataVersion)return fail('cloud_settings_conflict',409);
      const restore=sub==='restore-hostfile',model=restore?'demo-summary':body.model;
      if(!restore&&(body.provider!=='deepseek'||!model||Object.prototype.hasOwnProperty.call(body,'apiKey')&&!String(body.apiKey).trim()))return fail('invalid_cloud_settings');
      if(!c.availableModels.includes(model))return fail('provider_model_unavailable',502);
      if(body.apiKey==='DEMO_INVALID_KEY')return fail('provider_authentication_failed',502);
      if(sub==='test')return output({ok:true,metadataVersion:c.metadataVersion,provider:'deepseek',model,availableModels:c.availableModels,checkedAt:stamp(),fixture:true});
      // Never assign the request body, apiKey, or a key fragment to persistent data.
      Object.assign(c,{metadataVersion:c.metadataVersion+1,model,source:restore?'hostfile':body.apiKey?'managed':c.source,hasKey:true,updatedAt:stamp(),lastTestAt:stamp(),lastTestStatus:'success'});
      record(restore?'恢复演示主机配置':'验证并保存演示云端配置','settings',{id:'cloud'},['provider','model','source']);return output(c);
    }
    function importPreview(body) {
      const config=body.config;if(!config||typeof config!=='object')return fail('invalid_import');
      if(['devices','tasks','sources','models','credentials'].some(kind=>config[kind]!==undefined&&!Array.isArray(config[kind])))return fail('invalid_import');
      if((config.credentials||[]).length)return fail('demo_credentials_import_not_supported');
      const changes=[];
      for(const kind of ['devices','tasks','sources','models']){
        if(config[kind]!==undefined&&!Array.isArray(config[kind]))return fail('invalid_import');
        const seen=new Set();
        for(const value of config[kind]||[]){if(seen.has(value.id))return fail('object_exists',409);seen.add(value.id);const previous=table(kind).find(x=>x.id===value.id);if(!previous){const error=validateCreate(kind,value,config);if(error)return fail(error,error==='object_exists'?409:400);}else if(!fields[kind])return fail('immutable_model_package',409);
          changes.push({kind,id:value.id,action:previous?'update':'create',fields:previous?fields[kind].filter(k=>value[k]!==undefined&&JSON.stringify(value[k])!==JSON.stringify(previous[k])):Object.keys(value).filter(k=>!['apiKey','token','voiceToken'].includes(k)),metadataVersion:previous?.metadataVersion||0});}
      }
      const plan={id:id('demo-import'),expiresAt:new Date(Date.now()+300000).toISOString(),changes,fixture:true};
      // Only whitelisted public registration fields enter a persistent import plan.
      const clean={};for(const kind of ['devices','tasks','sources','models'])clean[kind]=(config[kind]||[]).map(v=>{const allowed=kind==='models'?['id','name','description','version','kind','slot','taskKinds','architectures','runtimeBackend','templateIds','image','modelId','modelHash','environment','requiresRestart','published','immutable']:['id',...(fields[kind]||[]),...(kind==='devices'?['endpointId','architecture']:kind==='tasks'?['deviceId','kind','target','models']:['deviceId','taskId','type'])];return Object.fromEntries(allowed.filter(k=>v[k]!==undefined).map(k=>[k,copy(v[k])]));});
      a.imports[plan.id]={...plan,config:clean};save();return output(plan,201);
    }
    function route(method,path,q,body) {
      if(!path.startsWith('/edgestack/admin/'))return null;
      if(!db.loggedIn)return fail('authentication_required',401);
      if(db.demoPersona!=='admin')return fail('administrator_required',403);
      settle();const relative=path.slice('/edgestack/admin/'.length),parts=relative.split('/'),kind=parts[0],key=parts[1],action=parts[2];
      if(relative.startsWith('settings/cloud'))return cloud(method,parts[2]||'',body);
      if(relative==='access-preview'&&method==='GET'){
        const uid=Number(q.get('userId')),d=findDevice(q.get('deviceId')),t=findTask(q.get('taskId')),da=businessAccess(d,uid),ta=!q.get('taskId')||!!t&&t.deviceId===d?.id&&businessAccess(t,uid),native=nativeAccess(d?.endpointId,uid),missing=[];
        if(!da)missing.push('device_business_permission');if(!ta)missing.push('task_business_permission');if(native!==true)missing.push(native===null?'native_endpoint_permission_unknown':'native_endpoint_permission');
        return output({userId:uid,deviceId:d?.id||q.get('deviceId'),taskId:q.get('taskId')||undefined,deviceBusinessAccess:da,taskBusinessAccess:ta,nativeEndpointAccess:native,effectiveAccess:da&&ta&&native===true,missing,fixture:true});
      }
      if(relative==='imports/preview'&&method==='POST')return importPreview(body);
      if(relative==='imports/confirm'&&method==='POST'){
        const plan=a.imports[body.previewId];if(!plan||body.confirm!==true)return fail('invalid_confirmation');if(plan.applied)return output({applied:true,changes:plan.changes,fixture:true});if(plan.expiresAt<stamp())return fail('import_preview_expired',409);
        for(const change of plan.changes){const item=table(change.kind).find(x=>x.id===change.id);if((item?.metadataVersion||0)!==change.metadataVersion)return fail('metadata_version_conflict',409);if(change.kind==='tasks'&&change.fields.some(k=>configFields.includes(k))&&Object.values(b.operations).some(op=>op.taskId===change.id&&!['succeeded','failed'].includes(op.status)))return fail('operation_in_progress',409);if(!item){const value=plan.config[change.kind].find(x=>x.id===change.id),error=validateCreate(change.kind,value,plan.config);if(error)return fail(error,409);}}
        for(const kind of ['devices','models','tasks','sources'])for(const value of plan.config[kind]){const item=table(kind).find(x=>x.id===value.id);if(item)patch(kind,item,value);else add(kind,value);record('导入演示登记',kind,value,Object.keys(value));}
        plan.applied=true;save();return output({applied:true,changes:plan.changes,fixture:true});
      }
      if(kind==='ingest-credentials'){
        if(!key&&method==='GET')return list(a.credentials,q);
        if(!key&&method==='POST')return credential(body);
        const c=a.credentials.find(c=>c.id===key);if(!c)return fail('not_found',404);
        if(action==='rotate'&&method==='POST'){if(c.revoked)return fail('credential_revoked',409);return credential(body,c);}
        if(method==='DELETE'){c.revoked=true;c.revokedAt=stamp();record('撤销演示凭据',kind,c,['revoked']);return output(c);}
      }
      if(kind==='models'&&key){
        const p=b.packages.find(p=>p.id===key);if(!p)return fail('not_found',404);
        if(action==='validation-preview'&&method==='POST'){const t=findTask(body.taskId);if(!t||body.revision!==t.revision)return fail('stale_revision',409);if(body.slot!==p.slot)return fail('incompatible_slot',409);return preview(t,p,'model-validation');}
        if(action==='validations'&&method==='GET')return list(a.validations.filter(v=>v.packageId===p.id),q);
        if(action==='validations'&&method==='POST'){
          const plan=b.previews[body.previewId],t=findTask(plan?.taskId);if(!plan||plan.packageId!==p.id||plan.purpose!=='model-validation'||body.confirm!==true)return fail('invalid_confirmation');
          if(plan.operationId)return output(b.operations[plan.operationId],202);
          if(!t||plan.revision!==t.revision||plan.expiresAt<stamp())return fail('stale_revision',409);
          const op={id:id('demo-operation'),taskId:t.id,deviceId:t.deviceId,packageId:p.id,previousPackageId:plan.previousPackageId,slot:p.slot,revision:t.revision+1,status:'awaiting_observation',actorUserId:1,target:copy(t.target),purpose:'model-validation',createdAt:stamp(),readyAt:Date.now()+1000,fixture:true};op.validationId=op.id;
          plan.operationId=op.id;b.operations[op.id]=op;p.validationStatus='validating';p.metadataVersion++;p.lastValidationId=op.id;t.runtimeState='unknown';delete t.runtimeObservedAt;
          a.validations.unshift({id:op.id,operationId:op.id,packageId:p.id,taskId:t.id,deviceId:t.deviceId,status:op.status,actorUserId:1,createdAt:op.createdAt,updatedAt:op.createdAt,fixture:true});record('确认离线模型验证',kind,p,['validationStatus']);return output(op,202);
        }
        if(action==='publication'&&method==='POST'){if(body.metadataVersion!==p.metadataVersion)return fail('metadata_version_conflict',409);if(typeof body.published!=='boolean')return fail('invalid_publication');if(body.published&&p.validationStatus!=='passed'&&!p.legacyScoped)return fail('model_validation_required',409);p.published=body.published;p.metadataVersion++;record(p.published?'发布演示模型候选':'撤回演示模型候选',kind,p,['published']);return output({id:p.id,published:p.published,metadataVersion:p.metadataVersion,fixture:true});}
      }
      const rows=table(kind);if(!rows)return fail('demo_admin_route_not_supported',404);
      if(!key&&method==='GET')return list(rows,q);
      const item=rows.find(x=>x.id===key);
      if(key&&method==='GET')return item?output(item):fail('not_found',404);
      if(!key&&method==='POST'&&['devices','tasks','sources','models'].includes(kind)){
        const error=validateCreate(kind,body);if(error)return fail(error,error==='object_exists'?409:400);
        const added=add(kind,body);record('新增演示登记',kind,added,Object.keys(body).filter(k=>!['apiKey','token'].includes(k)));return output(added,201);
      }
      if(key&&method==='PATCH'&&fields[kind]){if(!item)return fail('not_found',404);if(body.metadataVersion!==item.metadataVersion)return fail('metadata_version_conflict',409);const result=patch(kind,item,body);if(typeof result==='string')return fail(result,409);record('编辑演示登记',kind,item,result);return output(item);}
      return fail('demo_admin_route_not_supported',404);
    }
    save();return {route};
  }
  window.EdgeStackDemoAdmin={create};
})();
