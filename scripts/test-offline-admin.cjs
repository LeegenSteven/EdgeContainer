// Exercise the delivered offline HTML itself. No facade, init script or
// replacement API is injected; unexpected network requests are blocked.
const fs = require('node:fs');
const path = require('node:path');
const assert = require('node:assert/strict');
const { createHash } = require('node:crypto');
const { pathToFileURL } = require('node:url');
const { chromium } = require('playwright');

if (!process.argv[2]) {
  console.error('Usage: node scripts/test-offline-admin.cjs DELIVERED_HTML [REPORT_DIRECTORY] [--peer-html OTHER_DELIVERED_HTML]');
  process.exit(2);
}
const input = path.resolve(process.argv[2]);
const folder = path.resolve(process.argv[3] || 'deliverables/验证记录/offline-admin');
const original = fs.readFileSync(input);
const peerFlag = process.argv.indexOf('--peer-html');
const peerInput = peerFlag < 0 ? null : path.resolve(process.argv[peerFlag + 1] || '');
if (peerInput) assert(fs.statSync(peerInput).isFile(), 'Peer HTML is required after --peer-html');
fs.mkdirSync(path.join(folder, 'screenshots'), { recursive: true });
const sourceHtmlSha256 = createHash('sha256').update(original).digest('hex');
const checks = [], errors = [], consoleErrors = [], external = [], requests = [];
const sections = [
  ['devices', '设备业务登记'], ['tasks', '业务任务'], ['access', '业务授权'],
  ['sources', '来源与凭据'], ['models', '模型目录'],
  ['operations', '操作与审计'], ['cloud', '云端总结设置'],
];

(async () => {
  const browser = await chromium.launch({
    headless: true,
    channel: process.env.EDGESTACK_TEST_BROWSER || 'chrome',
  });
  const context = await browser.newContext({ viewport: { width: 1470, height: 1000 } });
  const page = await context.newPage();
  page.setDefaultTimeout(20000);
  function watch(target) {
    target.on('pageerror', e => errors.push(e.message));
    target.on('console', m => {
      if (m.type() === 'error') consoleErrors.push(m.text().slice(0, 500));
    });
  }
  watch(page);
  await context.route(/^https?:\/\//, async route => {
    external.push(route.request().url());
    await route.abort();
  });
  const cy = name => page.locator(`[data-cy="${name}"]`);
  const adminPath = tail => `admin/${tail}`;
  async function api(method, tail, body, target = page) {
    return target.evaluate(async ({ method, tail, body }) => {
      const response = await fetch(`/api/edgestack/${tail}`, {
        method,
        headers: { 'Content-Type': 'application/json' },
        ...(body === undefined ? {} : { body: JSON.stringify(body) }),
      });
      const text = await response.text();
      return { status: response.status, data: text ? JSON.parse(text) : null };
    }, { method, tail, body });
  }
  async function ok(method, tail, body, target = page) {
    const result = await api(method, tail, body, target);
    assert(result.status >= 200 && result.status < 300,
      `${method} ${tail}: ${result.status} ${result.data?.errorCode || ''}`);
    return result.data;
  }
  async function collectRequests(target = page) {
    requests.push(...await target.evaluate(() => (window.EdgeStackDemo?.requests || [])
      .map(({ method, path, status, unhandled }) => ({ method, path, status, unhandled }))));
  }
  async function navigate(hash, ready) {
    await page.evaluate(value => { location.hash = value; }, hash);
    if (ready) await page.locator(ready).waitFor();
  }
  async function persona(value, target = page) {
    await collectRequests(target);
    if (!(await target.locator('#demo-panel').isVisible())) await target.locator('#demo-toggle').click();
    await Promise.all([
      target.waitForEvent('load'),
      target.locator(`[data-persona="${value}"]`).click(),
    ]);
    await target.waitForFunction(() => Boolean(window.EdgeStackDemo));
    await (value === 'admin' ? target.locator('[data-cy="portainerSidebar-home"]') : target.locator('.ew-station').first()).waitFor();
  }
  async function reset(target = page) {
    await collectRequests(target);
    if (!(await target.locator('#demo-panel').isVisible())) await target.locator('#demo-toggle').click();
    target.once('dialog', dialog => dialog.accept());
    await Promise.all([target.waitForEvent('load'), target.locator('#demo-reset').click()]);
    await target.waitForFunction(() => Boolean(window.EdgeStackDemo));
    const current = await target.evaluate(() => window.EdgeStackDemo.getState().demoPersona);
    await (current === 'admin' ? target.locator('[data-cy="portainerSidebar-home"]') : target.locator('.ew-station').first()).waitFor();
  }
  async function noStoredSecrets(values) {
    const stored = await page.evaluate(() => JSON.stringify({
      local: Object.fromEntries(Object.entries(localStorage)),
      session: Object.fromEntries(Object.entries(sessionStorage)),
      demo: window.EdgeStackDemo.getState(),
    }));
    for (const value of values) assert(!stored.includes(value), 'One-time/candidate plaintext persisted');
  }
  async function poll(tail, predicate, label) {
    for (let attempt = 0; attempt < 60; attempt++) {
      const data = await ok('GET', tail);
      if (predicate(data)) return data;
      await page.waitForTimeout(200);
    }
    throw new Error(`Timed out: ${label}`);
  }
  async function confirm() {
    await cy('business-confirm').click();
    await page.getByRole('dialog').waitFor({ state: 'detached' });
  }
  try {
    await page.goto(pathToFileURL(input).href, { waitUntil: 'load' });
    await page.waitForFunction(() => Boolean(window.EdgeStackDemo));
    assert.equal(await page.evaluate(() => typeof window.__ADMIN_TEST__), 'undefined', 'Delivered HTML must not contain the test facade');
    await persona('admin');
    assert.equal(await page.evaluate(() => document.body.classList.contains('edgestack-user')), false);
    checks.push('交付 HTML 本身启动管理员原生首页，未注入测试 facade');

    await cy('edgestack-admin-menu-link').click();
    for (const [section, title] of sections) {
      await cy(`edgestack-admin-${section}`).click();
      await cy('edgestack-admin-view').waitFor();
      await page.locator('h1').filter({ hasText: title }).waitFor();
      assert.equal(await page.evaluate(() => document.body.classList.contains('edgestack-user')), false);
      await page.screenshot({ path: path.join(folder, 'screenshots', `offline-${section}.png`), fullPage: true });
      checks.push(`管理员原生菜单与页面：${title}`);
    }

    const devices = (await ok('GET', adminPath('devices'))).items;
    const device = devices[0];
    assert(device?.id && device.metadataVersion, 'Offline administrator device registration is missing');
    const tasks = (await ok('GET', adminPath('tasks'))).items;
    const task = tasks.find(t => t.testTask && !t.archived && t.models?.length);
    assert(task, 'At least one explicit simulated model test task is required');
    const originalDeviceName = device.name;
    const editedName = 'Home';
    await navigate(`#!/business/devices/${encodeURIComponent(device.id)}`, '[data-cy="business-name"]');
    await cy('business-name').fill(editedName);
    await ok('PATCH', adminPath(`devices/${device.id}`), {
      metadataVersion: device.metadataVersion,
      description: '离线演示：另一编辑会话先保存的资料。',
    });
    await cy('business-save').click();
    await page.getByRole('alert').filter({ hasText: '刷新' }).waitFor();
    assert.equal(await cy('business-name').inputValue(), editedName);
    checks.push('真实模拟 API 版本冲突返回 409，表单保留输入');
    await page.reload({ waitUntil: 'load' });
    await cy('business-name').waitFor();
    await cy('business-name').fill(editedName);
    await cy('business-save').click();
    await cy('business-add-record').waitFor();
    assert.equal((await ok('GET', adminPath(`devices/${device.id}`))).name, editedName);
    assert.equal(await cy('business-edit-record').filter({ hasText: editedName }).first().innerText(), editedName);
    checks.push('元数据编辑保存成功，动态资源名与界面同词时保留原值');

    const taskBefore = await ok('GET', adminPath(`tasks/${task.id}`));
    await navigate(`#!/business/tasks/${encodeURIComponent(task.id)}`, '[data-cy="business-name"]');
    await cy('business-name').fill(`${taskBefore.name} · 离线资料编辑`);
    await cy('business-save').click();
    await cy('business-add-record').waitFor();
    const taskAfter = await ok('GET', adminPath(`tasks/${task.id}`));
    assert.equal(taskAfter.revision, taskBefore.revision);
    assert.deepEqual(taskAfter.models, taskBefore.models);
    assert.deepEqual(taskAfter.target, taskBefore.target);
    checks.push('任务资料编辑保留部署版本、目标和模型观察事实');

    await navigate('#!/business/access', '[data-cy="access-device"]');
    await cy('access-device').selectOption(device.id);
    const userOptions = await cy('access-user').locator('option').evaluateAll(options => options.filter(o => o.value).map(o => o.value));
    assert(userOptions.length, 'Existing synthetic user options are required');
    await cy('access-user').selectOption(userOptions.find(value => value !== '1') || userOptions[0]);
    await cy('access-preview').waitFor();
    checks.push('已有模拟用户授权与原生权限交集可查看');

    await navigate('#!/business/sources', '[data-cy="credential-create"]');
    const source = (await ok('GET', adminPath('sources'))).items.find(s => !s.archived);
    assert(source, 'At least one simulated source is required');
    const secretValues = [];
    for (let index = 0; index < 2; index++) {
      await cy('credential-create').click();
      await cy('business-name').fill('离线同名来源凭据');
      await cy('business-sourceId').selectOption(source.id);
      await cy('business-editor').locator('input[type="checkbox"]').first().check();
      await cy('business-save').click();
      await cy('credential-once').waitFor();
      const token = (await cy('credential-once').innerText()).trim();
      assert(token.length > 10, 'A one-time demo token must be displayed');
      secretValues.push(token);
      await cy('credential-dismiss').click();
      assert(!(await page.locator('body').innerText()).includes(token));
      await noStoredSecrets(secretValues);
    }
    const credentials = (await ok('GET', adminPath('ingest-credentials'))).items
      .filter(c => c.name === '离线同名来源凭据');
    assert.equal(credentials.length, 2);
    assert.notEqual(credentials[0].id, credentials[1].id);
    assert(!JSON.stringify(credentials).includes(secretValues[0]));
    await page.reload({ waitUntil: 'load' });
    await cy('credential-create').waitFor();
    assert.equal(await cy('credential-once').count(), 0);
    await noStoredSecrets(secretValues);
    checks.push('同名凭据使用不同 ID；明文仅显示一次，刷新与持久数据均不含明文');

    await navigate('#!/business/models', '[data-cy="model-create"]');
    const existingModels = (await ok('GET', adminPath('models'))).items;
    const template = existingModels.find(m => (m.taskKinds || []).includes(task.kind) && task.models.some(b => b.slot === m.slot));
    assert(template, 'A compatible simulated model template is required');
    const newModelId = 'demo-offline-admin-check';
    await cy('model-create').click();
    await cy('business-id').fill(newModelId);
    await cy('business-name').fill('离线管理员草稿回归');
    await cy('business-version').fill('demo-check-1');
    await cy('business-kind').fill(template.kind || 'voice');
    await cy('business-slot').selectOption(template.slot);
    await cy('business-taskKinds').fill(task.kind);
    await cy('business-architectures').fill('arm64');
    await cy('business-runtimeBackend').fill(task.runtimeBackend || template.runtimeBackend);
    await cy('business-templateIds').fill(task.templateId || template.templateIds[0]);
    await cy('business-image').fill(`example.invalid/offline-model@sha256:${'e'.repeat(64)}`);
    await cy('business-modelId').fill('offline-admin-demo-weights');
    await cy('business-modelHash').fill(`sha256:${'f'.repeat(64)}`);
    await cy('business-save').click();
    await cy('business-save').waitFor({ state: 'detached' });
    const draft = await ok('GET', adminPath(`models/${newModelId}`));
    assert.equal(draft.published, false);
    await navigate(`#!/business/models/${newModelId}`, '[data-cy="model-detail"]');
    assert.equal(await cy('model-publication').isDisabled(), true);
    await cy('model-test-task').selectOption(task.id);
    await cy('model-validation-preview').click();
    await page.getByRole('dialog').waitFor();
    await confirm();
    const operation = await poll(adminPath('operations'), data => data.items.some(op => op.packageId === newModelId), 'simulated model operation');
    const operationId = operation.items.find(op => op.packageId === newModelId).id;
    await poll(`operations/${operationId}`, data => data.status === 'succeeded', 'simulated trusted model receipt');
    await cy('model-refresh').click();
    await cy('model-publication').waitFor({ state: 'visible' });
    await poll(adminPath(`models/${newModelId}`), data => data.validationStatus === 'passed', 'model validation passed');
    await page.waitForFunction(() => !document.querySelector('[data-cy="model-publication"]')?.disabled);
    const beforePublication = await ok('GET', `tasks/${task.id}/models`);
    assert(!beforePublication.items.some(m => m.id === newModelId));
    await cy('model-publication').click();
    await confirm();
    assert((await ok('GET', `tasks/${task.id}/models`)).items.some(m => m.id === newModelId));
    await cy('model-publication').filter({ hasText: '撤回' }).waitFor();
    await cy('model-publication').click();
    await confirm();
    assert(!(await ok('GET', `tasks/${task.id}/models`)).items.some(m => m.id === newModelId));
    checks.push('草稿登记、受控预览确认与模拟回执通过；发布/撤回即时联动用户模型候选');

    await navigate('#!/business/cloud', '[data-cy="cloud-api-key"]');
    const cloudBefore = await ok('GET', adminPath('settings/cloud'));
    const candidate = 'DEMO_OFFLINE_CANDIDATE_KEY';
    const badCandidate = 'DEMO_INVALID_KEY';
    await cy('cloud-api-key').fill(candidate);
    await cy('cloud-test').click();
    await cy('cloud-result').filter({ hasText: '尚未保存' }).waitFor();
    assert.equal(await cy('cloud-api-key').inputValue(), '');
    assert.equal((await ok('GET', adminPath('settings/cloud'))).metadataVersion, cloudBefore.metadataVersion);
    await noStoredSecrets([...secretValues, candidate]);
    await cy('cloud-api-key').fill(candidate);
    await cy('cloud-save').click();
    await cy('cloud-result').filter({ hasText: '配置已验证并保存' }).waitFor();
    assert.equal(await cy('cloud-api-key').inputValue(), '');
    const cloudSaved = await ok('GET', adminPath('settings/cloud'));
    assert.equal(cloudSaved.source, 'managed');
    assert(cloudSaved.metadataVersion > cloudBefore.metadataVersion);
    assert(!JSON.stringify(cloudSaved).includes(candidate));
    await page.getByText(new RegExp(`资料版本\\s*${cloudSaved.metadataVersion}`)).waitFor();
    await cy('cloud-api-key').fill(badCandidate);
    await cy('cloud-save').click();
    await page.getByRole('alert').filter({ hasText: '候选密钥验证失败' }).waitFor();
    assert.equal(await cy('cloud-api-key').inputValue(), '');
    assert.equal((await ok('GET', adminPath('settings/cloud'))).metadataVersion, cloudSaved.metadataVersion);
    await noStoredSecrets([...secretValues, candidate, badCandidate]);
    checks.push('云端验证与保存均为本地模拟；候选不留存，验证不保存，错误候选保留原配置');

    await page.reload({ waitUntil: 'load' });
    await cy('cloud-api-key').waitFor();
    assert.equal(await cy('cloud-api-key').inputValue(), '');
    assert.equal((await ok('GET', adminPath(`devices/${device.id}`))).name, editedName);
    assert.equal((await ok('GET', adminPath('settings/cloud'))).metadataVersion, cloudSaved.metadataVersion);
    assert.equal((await ok('GET', adminPath(`models/${newModelId}`))).published, false);
    checks.push('刷新后设备资料、模型发布状态及模拟云端配置持续保留');

    await persona('user');
    assert.equal(await cy('edgestack-admin-menu').count(), 0);
    for (const [method, tail, body] of [
      ['GET', 'devices'], ['GET', 'tasks'], ['GET', 'models'],
      ['GET', 'settings/cloud'], ['POST', 'ingest-credentials', {}],
      ['PATCH', `devices/${device.id}`, { metadataVersion: 1, name: 'unauthorized' }],
    ]) {
      assert.equal((await api(method, adminPath(tail), body)).status, 403, 'Ordinary demo user must not access administrator APIs');
    }
    const userModels = await ok('GET', `tasks/${task.id}/models`);
    assert(!userModels.items.some(m => m.id === newModelId));
    checks.push('普通用户无管理菜单且管理读写接口拒绝，撤回模型不进入用户候选');

    let peer;
    if (peerInput) {
      peer = await context.newPage();
      watch(peer);
      await peer.goto(pathToFileURL(peerInput).href, { waitUntil: 'load' });
      await peer.waitForFunction(() => Boolean(window.EdgeStackDemo));
      const ownConfig = await page.evaluate(() => window.__EDGESTACK_DEMO_CONFIG__);
      const peerConfig = await peer.evaluate(() => window.__EDGESTACK_DEMO_CONFIG__);
      assert(ownConfig?.storageKey && peerConfig?.storageKey, 'Each delivered HTML must declare its storage namespace');
      assert.notEqual(ownConfig.storageKey, peerConfig.storageKey);
      assert.equal((await ok('GET', 'devices', undefined, peer)).items.find(d => d.id === device.id).name, originalDeviceName);
      await persona('admin', peer);
      const peerDevice = await ok('GET', adminPath(`devices/${device.id}`), undefined, peer);
      await ok('PATCH', adminPath(`devices/${device.id}`), {
        metadataVersion: peerDevice.metadataVersion, name: '用户包独立资料',
      }, peer);
      checks.push('同一隔离浏览器中的两个交付 HTML 使用不同 storageKey，初始和编辑状态互不覆盖');
    }

    await reset();
    await persona('admin');
    assert.equal((await ok('GET', adminPath(`devices/${device.id}`))).name, originalDeviceName);
    assert(!(await ok('GET', adminPath('models'))).items.some(m => m.id === newModelId));
    assert(!(await ok('GET', adminPath('ingest-credentials'))).items.some(c => c.name === '离线同名来源凭据'));
    checks.push('控制面板恢复初始数据同时重置用户与管理员业务状态');

    if (peer) {
      await peer.reload({ waitUntil: 'load' });
      await peer.waitForFunction(() => Boolean(window.EdgeStackDemo));
      assert.equal((await ok('GET', adminPath(`devices/${device.id}`), undefined, peer)).name, '用户包独立资料', 'Resetting the administrator package must preserve the user package');
      const ownDevice = await ok('GET', adminPath(`devices/${device.id}`));
      await ok('PATCH', adminPath(`devices/${device.id}`), {
        metadataVersion: ownDevice.metadataVersion, name: '管理员包独立资料',
      });
      await reset(peer);
      await page.reload({ waitUntil: 'load' });
      await cy('portainerSidebar-home').waitFor();
      assert.equal((await ok('GET', adminPath(`devices/${device.id}`))).name, '管理员包独立资料', 'Resetting the user package must preserve the administrator package');
      await collectRequests(peer);
      await reset();
      checks.push('两包分别恢复初始数据均不清除另一包的持久资料');
      await peer.close();
    }

    await collectRequests();
    assert.deepEqual(errors, []);
    assert.deepEqual(consoleErrors, []);
    assert.deepEqual(external, []);
    assert.equal(createHash('sha256').update(fs.readFileSync(input)).digest('hex'), sourceHtmlSha256);
    const report = { passed: true, deliveredHtml: true, injectedFacade: false,
      input, peerInput, sourceHtmlSha256, checks, errors, consoleErrors, external, requests };
    fs.writeFileSync(path.join(folder, 'offline-admin-checks.json'), JSON.stringify(report, null, 2) + '\n');
    console.log(JSON.stringify({ ...report, requests: `${requests.length} local simulated requests` }, null, 2));
  } catch (error) {
    await collectRequests().catch(() => {});
    await page.screenshot({ path: path.join(folder, 'screenshots', 'offline-admin-failure.png'), fullPage: true }).catch(() => {});
    fs.writeFileSync(path.join(folder, 'offline-admin-failure.json'), JSON.stringify({
      passed: false, input, sourceHtmlSha256, error: String(error), checks,
      errors, consoleErrors, external, requests,
      body: await page.locator('body').innerText().catch(() => ''),
    }, null, 2) + '\n');
    throw error;
  } finally {
    await browser.close();
  }
})().catch(error => { console.error(error); process.exitCode = 1; });
