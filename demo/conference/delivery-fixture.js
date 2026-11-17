/* Invented offline delivery records. No build, registry, device or inference is
 * contacted. This simulator is included only in explicitly marked demo HTML. */
(function () {
  'use strict';
  function create({db,save,jsonResponse,business}) {
    const copy=value=>JSON.parse(JSON.stringify(value));
    const stamp=()=>new Date().toISOString();
    const id=kind=>'demo-'+kind+'-'+Date.now()+'-'+Math.random().toString(36).slice(2,7);
    const b=db.demoBusiness;
    db.demoDelivery ||= {requests:[],jobs:[],plans:[],rollouts:[],recipes:[],policies:[]};
    const d=db.demoDelivery;
    const output=(value,status=200)=>jsonResponse({...value,fixture:true},status);
    const fail=(errorCode,status=400)=>output({errorCode},status);
    const admin=()=>db.demoPersona==='admin';
    function route(method,path,q,body) {
      if(!/^\/edgestack\/(?:model-requests|delivery\/|deployment-plans|rollouts|admin\/model-requests|admin\/delivery\/|admin\/execution-leases)/.test(path))return null;
      if(!db.loggedIn)return fail('authentication_required',401);
      if(path.includes('/admin/')&&!admin())return fail('admin_required',403);
      if(path==='/edgestack/admin/execution-leases')return output({items:[]});
      if(path.endsWith('/delivery/settings'))return output({enabled:true,workerTokenConfigured:true,newTaskProviderConfigured:true,healthObserverConfigured:true,description:'离线模拟配置，不代表真实服务已接入'});
      if(path==='/edgestack/delivery/catalog')return output({items:b.packages.filter(p=>p.published&&!p.archived).map(p=>({packageId:p.id,name:p.name,version:p.version,image:p.image,architecture:'arm64',runtimeBackend:'demo-runtime',deviceIds:b.devices.map(x=>x.id),recipeId:'demo-offline-recipe'}))});
      if(path==='/edgestack/delivery/artifacts')return output({items:[]});
      if(path.endsWith('/delivery/jobs'))return output({items:copy(d.jobs)});
      let match=path.match(/^\/edgestack\/(?:admin\/)?delivery\/(recipes|policies)(?:\/([^/]+))?$/);
      if(match){const rows=d[match[1]];if(method==='GET')return output({items:copy(rows)});if(method==='PUT'){const old=rows.find(x=>x.id===match[2]);if(old&&old.metadataVersion!==body.metadataVersion)return fail('metadata_version_conflict',409);const row={...body,id:match[2],metadataVersion:(old?.metadataVersion||0)+1};if(old)Object.assign(old,row);else rows.push(row);save();return output(row);}}
      match=path.match(/^\/edgestack\/(?:admin\/)?model-requests(?:\/([^/]+)(?:\/(messages|resolve|retry))?)?$/);
      if(match){
        const item=d.requests.find(x=>x.id===match[1]);
        if(method==='GET')return match[1]?item?output(copy(item)):fail('not_found',404):output({items:copy(d.requests)});
        if(method==='POST'&&!match[1]){
          if(!body.deviceId||!body.modelName)return fail('invalid_model_request');
          const request={id:id('request'),deviceId:body.deviceId,taskId:body.taskId,modelName:body.modelName,status:'submitted',messages:[{id:id('message'),message:body.message,createdAt:stamp()}],createdAt:stamp(),updatedAt:stamp()};d.requests.push(request);save();return output(request,201);
        }
        if(!item)return fail('not_found',404);
        if(match[2]==='messages'){item.messages.push({id:id('message'),message:body.message,createdAt:stamp()});save();return output(item,201);}
        if(match[2]==='resolve'||match[2]==='retry'){
          if(!admin())return fail('admin_required',403);
          const job={id:id('job'),requestId:item.id,recipeId:body.recipeId,status:'queued',errorCode:'',createdAt:stamp(),fixture:true,description:'本地模拟任务，没有执行真实模型构建。'};d.jobs.push(job);item.status='resolved';item.jobId=job.id;save();return output(job,202);
        }
      }
      if(path==='/edgestack/deployment-plans'&&method==='POST'){
        const targets=(body.targets||[]).map(target=>({...target,newTask:!target.taskId,preview:{effects:['离线模拟：不会操作真实设备。','模拟保留数据并等待逐台结果。']}}));
        if(!targets.length||!body.packageId)return fail('invalid_deployment_plan');
        const plan={id:id('plan'),packageId:body.packageId,targets,expiresAt:new Date(Date.now()+300000).toISOString(),fixture:true};d.plans.push(plan);save();return output(plan);
      }
      match=path.match(/^\/edgestack\/deployment-plans\/([^/]+)\/confirm$/);
      if(match&&method==='POST'){
        const plan=d.plans.find(x=>x.id===match[1]);if(!plan||!body.confirm)return fail('confirmation_required');
        if(plan.rolloutId)return output(d.rollouts.find(x=>x.id===plan.rolloutId),202);
        const rollout={id:id('rollout'),planId:plan.id,packageId:plan.packageId,status:'running',createdAt:stamp(),targets:plan.targets.map((t,index)=>({...t,status:index===0?'observing':'pending'})),description:'本地模拟健康观察，未执行真实部署。'};plan.rolloutId=rollout.id;d.rollouts.push(rollout);save();return output(rollout,202);
      }
      if(path==='/edgestack/rollouts')return output({items:copy(d.rollouts)});
      return fail('demo_delivery_route_not_supported',404);
    }
    return {route};
  }
  window.EdgeStackDemoDelivery={create};
})();
