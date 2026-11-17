/* Model delivery UI uses the authenticated EdgeContainer API. */
(function () {
  'use strict';
  const escape = value => String(value ?? '').replace(/[&<>"']/g, char => ({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[char]));
  const labels = {
    waiting_for_device:'等待设备空闲', submitted:'等待管理员适配', needs_information:'需要补充信息', resolved:'已配置运行方案',
    queued:'等待执行', claimed:'正在执行', native_validation:'正在验证模型',
    building:'正在构建镜像', artifact_ready:'镜像已保存', image_validation:'正在验证镜像',
    policy_evaluation:'正在核对发布策略', published:'已发布可用', succeeded:'已完成',
    awaiting_observation:'等待设备确认', observing:'健康观察中', paused:'已暂停',
    failed:'未完成', needs_review:'结果待核对', cancelled:'已取消', rolled_back:'已恢复原版本',
    rollback_failed:'恢复需要处理', deploying:'正在部署', running:'进行中',
  };
  const errors = {
    worker_lease_expired:'执行连接已中断，请联系管理员核对已完成阶段后重试。',
    delivery_claim_lost:'执行授权已失效，请联系管理员核对任务状态。',
    delivery_authorization_changed:'设备权限或发布配置已变化，请联系管理员重新核对。',
    delivery_validation_health_failed:'最终镜像的健康检查未通过，请联系管理员查看验证任务。',
    delivery_rollback_unconfirmed:'系统尚未确认原版本已恢复，请联系管理员核对设备。',
    source_format_unsupported:'当前运行配方不支持这个模型格式，需要管理员补充适配。',
    native_model_mismatch:'模型文件与已登记的验证方案不一致，请管理员核对来源。',
    waiting_for_device_execution_lease:'设备正被其他任务使用，请等待设备空闲。',
    delivery_artifact_root_unconfigured:'离线镜像存储尚未配置，请联系管理员。',
    runtime_health_unconfirmed:'设备尚未通过运行健康检查，请稍后再试。',

    new_task_provider_unconfigured:'管理员尚未安装新建业务任务的运行模板。',
    target_not_provisionable:'这台设备尚未满足新建任务条件，请联系管理员检查运行模板和独立设备组。',
    package_incompatible:'所选模型与目标设备或业务任务不兼容，请重新选择。',
    delivery_recipe_unavailable:'这个模型的运行配方暂不可用，请联系管理员。',
    delivery_disabled:'模型交付服务尚未启用，请联系管理员。',
    delivery_not_configured:'管理员尚未配置模型交付服务。',
    delivery_worker_not_configured:'管理员尚未配置模型构建服务。',
    delivery_permission_required:'当前账号没有这项部署权限，请联系管理员。',
    deployment_permission_required:'当前账号没有这项部署权限，请联系管理员。',
    metadata_version_conflict:'资料已更新，请重新读取后保存。',
    delivery_source_required:'需要管理员登记模型来源和固定文件摘要。',
    delivery_recipe_required:'请选择运行配方。',
    delivery_policy_required:'请选择发布策略。',
    delivery_no_compatible_recipe:'当前设备还没有兼容运行配方。',
    delivery_policy_changed:'发布策略已变化，任务需要重新评估。',
    delivery_provisioner_unavailable:'该设备尚未配置新建任务能力。',
    delivery_managed_execution_required:'设备执行组件需要完成升级和租约配置。',
  };
  async function mount(host, options) {
    const dialog = document.createElement('dialog');
    dialog.id = 'ew-delivery';
    dialog.className = 'ew-delivery-dialog';
    dialog.setAttribute('aria-label','模型部署助手');
    host.append(dialog);
    let alive = true, busy = false, refreshing = false, refreshError = '', message = '', tab = 'assistant', timer, plan, requestKey, selectedRequest = '';
    let targets = [], catalogModels = [];
    let devices = [], requests = [], jobs = [], artifacts = [], rollouts = [];
    const controller = new AbortController();
    const values = {deviceId:'',taskId:'',modelName:'',message:'',packageId:'',taskName:'',reply:''};
    const requestId = () => crypto.randomUUID?.() || 'delivery-'+Date.now()+'-'+Math.random().toString(36).slice(2);
    async function api(method, path, body, key) {
      const response = await fetch(new URL('api/edgestack/'+path,document.baseURI),{
        method, credentials:'same-origin',signal:controller.signal,
        headers:{...(body ? {'Content-Type':'application/json'}:{}),...(key?{'Idempotency-Key':key}:{})},
        ...(body?{body:JSON.stringify(body)}:{}),
      });
      const data = response.status===204?null:await response.json();
      if(!response.ok)throw Error(errors[data?.errorCode]||data?.message||data?.errorCode||'服务暂时无法读取，请稍后重试。');
      return data;
    }
    const list = data => Array.isArray(data)?data:data?.items||[];
    function button(action,text,quiet=false) {
      return `<button type="button" class="ew-button ${quiet?'ew-button-quiet':''}" data-delivery="${action}" ${busy?'disabled':''}>${text}</button>`;
    }
    function field(label,name,control) {
      return `<label>${label}${control||`<input name="${name}" value="${escape(values[name])}">`}</label>`;
    }
    function deviceFields() {
      const device=devices.find(d=>String(d.id)===values.deviceId);
      const tasks=device?.tasks||[];
      return field('目标设备','deviceId',`<select name="deviceId"><option value="">请选择设备</option>${devices.map(d=>`<option value="${escape(d.id)}" ${String(d.id)===values.deviceId?'selected':''}>${escape(d.name)}</option>`).join('')}</select>`)+
        field('业务任务','taskId',`<select name="taskId"><option value="">新增独立业务任务</option>${tasks.map(t=>`<option value="${escape(t.id)}" ${String(t.id)===values.taskId?'selected':''}>${escape(t.name)}</option>`).join('')}</select>`)+
        (!values.taskId?field('新任务名称','taskName'):'');
    }
    function status(value) { return escape(labels[value]||value||'等待更新'); }
    function records(items,kind) {
      if(!items.length)return '<p class="ew-muted">当前还没有记录。</p>';
      return `<ul class="ew-delivery-records">${items.map(item=>`<li><strong>${escape(item.modelName||item.name||requests.find(r=>r.id===item.requestId)?.modelName||catalogModels.find(model=>model.packageId===item.packageId)?.name||item.id)}</strong><span>${status(item.status==='claimed'?(item.phase||item.status):item.status)}</span>${item.errorCode?`<p>${escape(errors[item.errorCode]||item.errorCode)}</p>`:''}${item.targets?.length?`<ul>${item.targets.map(t=>`<li>${escape(devices.find(d=>String(d.id)===String(t.deviceId))?.name||t.deviceId)} · ${status(t.status)}${t.rollbackStatus?' · '+status(t.rollbackStatus):''}</li>`).join('')}</ul>`:''}${kind==='request'?`<button type="button" class="ew-button ew-button-quiet" data-request="${escape(item.id)}">查看并补充说明</button>`:''}</li>`).join('')}</ul>`;
    }
    function targetName(target) {
      const device=devices.find(d=>String(d.id)===String(target.deviceId));
      const task=device?.tasks?.find(t=>String(t.id)===String(target.taskId));
      return `${device?.name||target.deviceId} · ${task?.name||target.taskName||'新增业务任务'}`;
    }
    const catalogKey = model => (model.packageId||model.id)+'|'+(model.recipeId||'');
    function packageField() {
      return field('已发布模型版本','packageId',`<select name="packageId"><option value="">请选择可用模型</option>${catalogModels.map(model=>`<option value="${escape(catalogKey(model))}" ${catalogKey(model)===values.packageId?'selected':''}>${escape(model.name||model.modelId)} · ${escape(model.version||'')} · ${model.recipeId?'可新建任务':'用于现有任务'}</option>`).join('')}</select>`);
    }
    function requestDetail() {
      const item=requests.find(r=>r.id===selectedRequest);
      if(!item)return '';
      return `<section class="ew-delivery-preview"><h3>${escape(item.modelName)}的沟通记录</h3><ol class="ew-delivery-messages">${(item.messages||[]).map(m=>`<li><p>${escape(m.message)}</p><small>${escape(new Date(m.createdAt).toLocaleString('zh-CN'))}</small></li>`).join('')}</ol>${field('补充说明','reply',`<textarea name="reply" rows="3" maxlength="4000">${escape(values.reply)}</textarea>`)}${button('reply','发送补充说明')}</section>`;
    }
    function catalog() {
      return catalogModels.length?`<ul class="ew-delivery-records">${catalogModels.map(a=>`<li><strong>${escape(a.name||a.modelId)} · ${escape(a.version||'')}</strong><p>${escape(a.runtimeBackend||a.engine||'')} · ${escape(a.architecture||'')}</p><details><summary>镜像版本信息</summary><code>${escape(a.image||'')}</code></details>${a.artifactId?`<a class="ew-button ew-button-quiet" href="${escape(new URL('api/edgestack/delivery/artifacts/'+encodeURIComponent(a.artifactId)+'/export',document.baseURI).href)}" download>下载签名离线镜像包</a>`:''}<button class="ew-button ew-button-quiet" data-package="${escape(catalogKey(a))}">选择此版本部署</button></li>`).join('')}</ul>`:'<p>当前没有已授权的发布模型。可以先申请模型，由管理员登记配方并验证发布。</p>';

    }
    function render() {
      if(!alive)return;
      const active=document.activeElement;
      const focus=dialog.contains(active)?active?.name:'';
      const selection=focus&&typeof active.selectionStart==='number'?active.selectionStart:null;
      dialog.innerHTML=`<header><h2>模型部署助手</h2><button type="button" class="ew-icon-button" data-delivery="close" aria-label="关闭部署助手"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true"><path d="m6 6 12 12M6 18 18 6"/></svg></button></header>
        <nav aria-label="部署助手内容">${[['assistant','申请模型'],['deploy','部署模型'],['requests','我的申请'],['jobs','构建与部署'],['catalog','镜像版本']].map(([id,title])=>`<button type="button" data-delivery-tab="${id}" aria-current="${id===tab?'page':'false'}">${title}</button>`).join('')}</nav>
        <div class="ew-delivery-body">${message||refreshError?`<p role="alert" class="ew-delivery-error">${escape(message||refreshError)}</p>`:''}
        ${tab==='assistant'?`<p>填写希望部署的模型和用途，管理员登记兼容的运行配方后，你可以继续查看处理进度。</p><form data-delivery-form="request">${deviceFields()}${field('模型名称','modelName')}${field('用途、模型来源和性能目标','message',`<textarea name="message" rows="4" maxlength="4000" placeholder="例如：在这台设备上部署目标检测，优先降低推理延迟。">${escape(values.message)}</textarea>`)}${button('request','提交模型申请')}</form>`:''}
        ${tab==='deploy'?`<p>选择已发布模型。预览将核对每台设备的权限、兼容性、资源和原版本。</p><form data-delivery-form="deploy">${deviceFields()}${button('add-target','加入部署目标',true)}${targets.length?`<ol class="ew-delivery-targets">${targets.map((target,index)=>`<li>${escape(targetName(target))}<button type="button" class="ew-button ew-button-quiet" data-remove-target="${index}">移除</button></li>`).join('')}</ol>`:''}${packageField()}${button('preview','生成部署预览')}</form>${plan?`<section class="ew-delivery-preview" tabindex="-1"><h3>部署影响预览</h3><p>${escape(plan.effects?.join(' ')||plan.preview?.effects?.join(' ')||'提交后将在所选设备更新模型；系统保留原版本并核验设备结果。')}</p><p>预览有效期 ${escape(plan.expiresAt?new Date(plan.expiresAt).toLocaleString('zh-CN'):'以提交时重新核验为准')}</p>${plan.targets?`<ol>${plan.targets.map((target,index)=>`<li><strong>${escape(targetName(target))}</strong>${index===0?' · 首台验证':' · 前一台健康后继续'}<p>${escape((target.provision?.effects||target.preview?.effects||[]).join(' '))}</p></li>`).join('')}</ol>`:''}${button('confirm','确认部署')}${button('discard','取消预览',true)}</section>`:''}`:''}
        ${tab==='requests'?records(requests,'request')+requestDetail():''}
        ${tab==='jobs'?`<h3>镜像处理</h3>${records(jobs,'job')}<h3>端侧部署</h3>${records(rollouts,'rollout')}`:''}
        ${tab==='catalog'?catalog():''}
        </div>`;
      if(focus){const el=dialog.querySelector(`[name="${focus}"]`);el?.focus({preventScroll:true});if(selection!==null&&el?.setSelectionRange&&el.type!=='number')el.setSelectionRange(selection,selection);}
    }
    async function refresh() {
      if(!alive||!dialog.open||refreshing)return;
      refreshing=true;
      try {
        const responses=await Promise.all(['model-requests','delivery/jobs','delivery/artifacts','rollouts','delivery/catalog'].map(path=>api('GET',path)));
        [requests,jobs,artifacts,rollouts,catalogModels]=responses.map(list);
        for(const rows of [requests,jobs,rollouts])rows.sort((a,b)=>Date.parse(b.updatedAt||b.createdAt||0)-Date.parse(a.updatedAt||a.createdAt||0));
        refreshError='';
      } catch(error) {if(error.name!=='AbortError')refreshError=error.message;}
      finally {refreshing=false;}
      render();
    }
    async function action(name) {
      if(name==='close'){dialog.close();return;}
      if(busy)return;
      busy=true;message='';render();
      try {
        if(name==='add-target'){
          if(!values.deviceId)throw Error('请先选择设备。');
          if(targets.some(target=>target.deviceId===values.deviceId&&target.taskId===(values.taskId||undefined)))throw Error('此部署目标已加入。');
          targets.push({deviceId:values.deviceId,taskId:values.taskId||undefined,taskName:values.taskName});plan=undefined;
        }
        if(name==='reply'){
          if(!values.reply.trim())throw Error('请填写补充说明。');
          await api('POST','model-requests/'+encodeURIComponent(selectedRequest)+'/messages',{message:values.reply},requestId());
          values.reply='';await refresh();
        }
        if(name==='request'){
          if(!values.deviceId||!values.modelName.trim())throw Error('请选择目标设备并填写模型名称。');
          requestKey ||= requestId();
          await api('POST','model-requests',{deviceId:values.deviceId,taskId:values.taskId||undefined,modelName:values.modelName,message:values.message},requestKey);
          requestKey=undefined;tab='requests';await refresh();
        }
        if(name==='preview'){
          if((!values.deviceId&&!targets.length)||!values.packageId.trim())throw Error('请选择设备和已发布模型版本。');
          const model=catalogModels.find(model=>catalogKey(model)===values.packageId);
          plan=await api('POST','deployment-plans',{targets:targets.length?targets:[{deviceId:values.deviceId,taskId:values.taskId||undefined,taskName:values.taskName}],packageId:model?.packageId||model?.id,recipeId:model?.recipeId});
        }
        if(name==='confirm'&&plan){await api('POST','deployment-plans/'+encodeURIComponent(plan.id)+'/confirm',{confirm:true},plan.id);plan=undefined;targets=[];tab='jobs';await refresh();}
        if(name==='discard')plan=undefined;
      } catch(error){message=error.message;}
      finally{busy=false;render();if(name==='preview'&&plan)dialog.querySelector('.ew-delivery-preview')?.focus();}
    }
    dialog.addEventListener('click',event=>{
      if(event.target===dialog){dialog.close();return;}
      const tabButton=event.target.closest('[data-delivery-tab]');
      if(tabButton){tab=tabButton.dataset.deliveryTab;message='';render();refresh();return;}
      const request=event.target.closest('[data-request]');
      if(request){selectedRequest=request.dataset.request;render();return;}
      const remove=event.target.closest('[data-remove-target]');
      if(remove&&!busy){targets.splice(Number(remove.dataset.removeTarget),1);plan=undefined;render();return;}
      const pkg=event.target.closest('[data-package]');
      if(pkg){values.packageId=pkg.dataset.package;tab='deploy';render();return;}
      const target=event.target.closest('[data-delivery]');
      if(target)action(target.dataset.delivery);
    });
    dialog.addEventListener('submit',event=>{event.preventDefault();action(event.target.dataset.deliveryForm==='request'?'request':'preview');});
    dialog.addEventListener('input',event=>{
      const {name,value}=event.target;
      if(Object.hasOwn(values,name)){values[name]=value;requestKey=undefined;plan=undefined;}
    });
    dialog.addEventListener('change',event=>{
      const {name,value}=event.target;
      if(Object.hasOwn(values,name)){values[name]=value;plan=undefined;if(name==='deviceId'){values.taskId='';render();}}
    });
    dialog.addEventListener('close',()=>clearInterval(timer));
    render();
    return {
      open(initialTab='assistant') {
        devices=options.devices();
        values.deviceId=String(options.selectedDevice?.()||values.deviceId||devices[0]?.id||'');
        tab=initialTab;message='';render();
        if(!dialog.open)dialog.showModal();
        refresh();clearInterval(timer);timer=setInterval(refresh,5000);
      },
      destroy(){alive=false;controller.abort();clearInterval(timer);dialog.remove();},
    };
  }
  window.EdgeStackDeliveryUI={mount};
})();
