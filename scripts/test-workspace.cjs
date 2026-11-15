// Historical deployment core regressions; optional browser QA for the user workspace.
const assert=require('node:assert/strict');
const C=require('../portainer/app/edgestack/flow-core.js');
const catalog=require('../deploy/ui/catalog/apps.json');
const device={Id:4,Status:1,LastCheckInDate:100,EdgeCheckinInterval:5};
assert.equal(C.normalizeHost('192.0.2.20'),'192.0.2.20');assert.equal(C.normalizeHost('256.0.0.1'),'');assert.equal(C.normalizeHost('javascript:alert(1)'),'');assert(C.online(device,110));assert(!C.online(device,131));assert(!C.online({...device,Status:2},110));
const groups=[{Id:1,Endpoints:[4,5],Dynamic:false},{Id:2,Endpoints:[4],Dynamic:true},{Id:3,Endpoints:[4],Dynamic:false}];
assert.equal(C.exactGroup(groups,4).Id,3);assert(!C.groupsMatch(groups,1,4));assert(!C.groupsMatch(groups,3,5));assert(C.groupsMatch(groups,3,4));
const form={name:'workflow-test',port:18084,volume:'workflow-test_data',title:'标题 "\n: test',reuseVolume:false};
assert.deepEqual(C.validate(form,catalog.recipes[1]),[]);assert(C.validate({...form,name:'../../bad',port:'3.2'},catalog.recipes[1]).length>=2);
const spec=JSON.parse(C.compose(form,catalog.recipes[1],catalog.recipes[1].versions[0],4));
assert.equal(spec.services.app.environment.SITE_TITLE,form.title);assert.equal(spec.services.app.labels['io.edgestack.device-id'],'4');assert.equal(spec.volumes.data.name,form.volume);
assert.equal(spec.services.app.platform,'linux/arm64');assert(spec.services.app.healthcheck);
assert(C.portConflict([{State:'running',Ports:[{PublicPort:18084}]}],18084));assert(!C.portConflict([{State:'exited',Ports:[{PublicPort:18084}]}],18084));
assert(C.health({State:{Running:true}}).includes('未配置'));assert(C.health({State:{Running:false,Health:{Status:'healthy'}}}).includes('已停止'));
const frame=Buffer.alloc(8);frame[0]=1;frame.writeUInt32BE(Buffer.byteLength('测试日志'),4);const log=Buffer.concat([frame,Buffer.from('测试日志')]);assert.equal(C.decodeLogs(log.buffer.slice(log.byteOffset,log.byteOffset+log.byteLength)),'测试日志');
console.log('Model checks passed: stale heartbeat, exact target, typed parameters, storage, ports, health and log frames.');
// The user workspace now uses the business API. Keep the historical core checks
// above and run the isolated spatial interface suite when browser QA is requested.
if (process.argv[2]) require('./test-scene-ui.cjs');
