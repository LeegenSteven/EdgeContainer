/* Pure model for the EdgeStack workflow. No credentials or device addresses. */
(function (root, factory) {
  const core = factory();
  if (typeof module === 'object' && module.exports) module.exports = core;
  else root.EdgeStackFlowCore = core;
})(typeof window === 'object' ? window : globalThis, function () {
  'use strict';
  const LABEL = 'io.edgestack.recipe';
  const id = x => Number(x.Id ?? x.ID);
  function online(device, now = Date.now() / 1000) {
    if (device.Status === 2 || !device.LastCheckInDate) return false;
    return now - device.LastCheckInDate <= (device.EdgeCheckinInterval || 5) * 2 + 20;
  }
  function exactGroup(groups, deviceId) {
    return groups.find(g => !g.Dynamic && Array.isArray(g.Endpoints) && g.Endpoints.length === 1 && +g.Endpoints[0] === +deviceId);
  }
  function validate(form, recipe) {
    const errors = [];
    if (!/^[a-z0-9][a-z0-9_-]{1,47}$/.test(form.name)) errors.push('名称请使用 2–48 位小写字母、数字、横线或下划线。');
    if (!Number.isInteger(+form.port) || +form.port < 1024 || +form.port > 65535) errors.push('访问端口请填写 1024–65535 之间的整数。');
    if (recipe.storage && !/^[a-zA-Z0-9][a-zA-Z0-9_.-]{1,79}$/.test(form.volume)) errors.push('数据卷名称请使用 2–80 位字母、数字、点、横线或下划线。');
    if (recipe.storage && (!form.title?.trim() || form.title.length > 80)) errors.push('网页标题请填写 1–80 个字符。');
    if (form.host !== undefined && !normalizeHost(form.host)) errors.push('请填写设备的业务 IP 或域名，例如板子的局域网 IP；不要填写管理网页的 Server 地址。');
    return errors;
  }
  function normalizeHost(value) {
    const input=String(value||'').trim();
    if(!input || /[\s/@?#\\]/.test(input))return '';
    if(input.includes(':')) {
      try {const u=new URL('http://'+(input.startsWith('[')?input:'['+input+']'));return u.hostname.includes(':')?u.hostname:'';}catch{return '';}
    }
    if(!/^[a-z0-9](?:[a-z0-9.-]{0,251}[a-z0-9])?$/i.test(input)||input.includes('..'))return '';
    if(/^\d+(\.\d+){3}$/.test(input)&&input.split('.').some(n=>+n>255))return '';
    return input;
  }
  function compose(form, recipe, version, deviceId) {
    const service = {
      image: version.image, platform: 'linux/arm64', restart: 'unless-stopped',
      ports: [`${+form.port}:8080`],
      labels: { [LABEL]: recipe.id, 'io.edgestack.recipe-revision': recipe.revision,
        'io.edgestack.app-version': version.name, 'io.edgestack.device-id': String(deviceId) },
      mem_limit: '128m', cpus: 0.5,
      logging: { driver: 'json-file', options: { 'max-size': '10m', 'max-file': '3' } },
    };
    const result = { services: { app: service } };
    if (recipe.storage) {
      service.environment = { SITE_TITLE: form.title };
      service.command = ['python', '-u', '-c',
        'import os,pathlib,http.server,html; os.chdir("/data"); p=pathlib.Path("index.html"); p.exists() or p.write_text("<!doctype html><meta charset=utf-8><h1>"+html.escape(os.environ["SITE_TITLE"])+"</h1><p>EdgeStack persistent data</p>",encoding="utf-8"); print("EdgeStack web ready; data=/data",flush=True); http.server.test(HandlerClass=http.server.SimpleHTTPRequestHandler,port=8080,bind="0.0.0.0")'];
      service.volumes = ['data:/data'];
      service.healthcheck = { test: ['CMD', 'python', '-c', 'import urllib.request; urllib.request.urlopen("http://127.0.0.1:8080",timeout=2)'], interval: '10s', timeout: '3s', retries: 3, start_period: '5s' };
      result.volumes = { data: { name: form.volume, ...(form.reuseVolume ? { external: true } : {}) } };
    } else service.command = ['--port=8080'];
    // JSON is a YAML subset; serialize values without interpolating shell/YAML text.
    return JSON.stringify(result, null, 2);
  }
  function portConflict(containers, port) {
    return containers.some(c => c.State === 'running' && (c.Ports || []).some(p => +p.PublicPort === +port));
  }
  function groupsMatch(groups, groupId, deviceId) {
    const group = exactGroup(groups, deviceId);
    return groups.some(g => id(g) === +groupId && !g.Dynamic && g.Endpoints?.length === 1 && +g.Endpoints[0] === +deviceId) && !!group;
  }
  function health(container) {
    if (!container.State?.Running) return '程序已停止，业务健康待确认';
    return ({healthy:'健康检查通过',unhealthy:'健康检查异常',starting:'健康检查启动中'})[container.State.Health?.Status] || '运行中，未配置健康检查';
  }
  function deployStatus(stack, deviceId) {
    const statuses = stack.Status?.[deviceId]?.Status || [];
    const latest = [...statuses].sort((a,b) => (a.Time || 0) - (b.Time || 0)).at(-1);
    if (!latest) return '已提交，等待设备回报';
    return ({0:'等待设备',1:'设备已收到方案',2:'部署失败',3:'设备已确认方案',4:'已移除',5:'更新完成',6:'镜像已准备',7:'部署完成',8:'部署中',9:'移除中',10:'部署已暂停',11:'回退中',12:'已回退',13:'任务完成'})[latest.Type] || '等待设备回报';
  }
  function decodeLogs(buffer) {
    const bytes = new Uint8Array(buffer), decoder = new TextDecoder();
    let offset = 0, output = '';
    while (offset + 8 <= bytes.length && bytes[offset] <= 2 && !bytes[offset+1] && !bytes[offset+2] && !bytes[offset+3]) {
      const length = new DataView(bytes.buffer, bytes.byteOffset + offset, 8).getUint32(4);
      if (offset + 8 + length > bytes.length) break;
      output += decoder.decode(bytes.slice(offset + 8, offset + 8 + length));
      offset += length + 8;
    }
    return offset === bytes.length && offset ? output : decoder.decode(bytes);
  }
  return { LABEL, id, online, exactGroup, validate, normalizeHost, compose, portConflict, groupsMatch, health, deployStatus, decodeLogs };
});
