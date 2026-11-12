// Exercises the self-contained file in a fresh, offline browser profile.
const { chromium } = require('playwright');
const fs = require('node:fs');
const path = require('node:path');
const { pathToFileURL } = require('node:url');
const assert = require('node:assert/strict');

(async () => {
  const file = path.resolve(process.argv[2]);
  const browser = await chromium.launch({ headless: true, channel: process.env.EDGESTACK_TEST_BROWSER || undefined });
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
    assert(body.includes('主机概览'), 'Expected the original Host overview view to load');
    assert(body.includes('rk3588-demo'), 'Expected the simulated RK3588 host');
    assert.equal(errors.length, 0, 'No uncaught JavaScript errors');
    assert.equal(external.length, 0, 'No external HTTP requests');
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
      await page.locator('[data-cy="environment-card-rk3588-demo-01"]').getByText('离线', { exact: true }).waitFor();
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
      await page.getByRole('heading', { name: '主机概览', exact: true }).waitFor();
      assert.equal(await page.evaluate(() => window.EdgeStackDemo.getState().edge_groups.length), 1);
      passed.push('一键恢复初始演示数据');
      await page.locator('[data-cy="userMenu-button"]').click();
      await page.locator('[data-cy="userMenu-logOut"]').click();
      await page.locator('#username').waitFor();
      await page.locator('#username').fill('admin');
      await page.locator('#password').fill('demo');
      await page.getByRole('button', { name: '登录', exact: true }).click();
      await page.waitForFunction(() => window.EdgeStackDemo.getState().loggedIn);
      await page.getByRole('heading', { name: '首页', exact: true }).waitFor();
      passed.push('原生退出和演示账户重新登录');
      assert.equal(errors.length, 0);
      assert.equal(external.length, 0);
      fs.writeFileSync(path.join(output, 'controls.json'), JSON.stringify({ status: 'passed', passed, errors, external }, null, 2));
      console.log(JSON.stringify({ controls_status: 'passed', passed, errors, external }, null, 2));
    }
    if (process.argv.includes('--preview')) {
      await page.evaluate(() => { location.hash = '#!/4/docker/host'; });
      await page.getByRole('heading', { name: '主机概览', exact: true }).waitFor();
      await page.screenshot({ path: path.join(output, 'final-preview.png'), fullPage: true });
      await page.locator('#demo-toggle').click();
      await page.screenshot({ path: path.join(output, 'final-controls.png'), fullPage: true });
    }
  } finally { await browser.close(); }
})().catch((error) => { console.error(error); process.exitCode = 1; });
