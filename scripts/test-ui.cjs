// Focused regression coverage for a presentation overlay in a privileged UI:
// UI labels must change while names, form values, logs and controls remain valid.
// Usage: NODE_PATH=/path/to/node_modules node scripts/test-ui.cjs RELEASE_DIR
const assert = require('node:assert/strict');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const { pathToFileURL } = require('node:url');
const { chromium } = require('playwright');

(async () => {
  const release = path.resolve(process.argv[2]);
  const manifest = JSON.parse(fs.readFileSync(path.join(release, 'manifest.json')));
  const bundle = Object.keys(manifest.files).find((x) => x.endsWith('.js'));
  const temp = fs.mkdtempSync(path.join(os.tmpdir(), 'edgestack-ui-test-'));
  const fixture = path.join(temp, 'fixture.html');
  fs.writeFileSync(fixture, `<!doctype html><html lang="en"><head><meta charset="utf-8"><title>Portainer</title>
  <script defer src="${pathToFileURL(path.join(release, bundle))}"></script></head><body>
  <aside id="sideview"><a data-cy="portainerSidebar-homeImage"><img src="upstream-logo.svg" alt="Logo"></a>
  <a id="home">Home</a><button id="collapse" title="Toggle Sidebar">Collapse</button></aside>
  <h1>Host overview</h1><button id="refresh" title="Refresh page">Refresh</button>
  <input id="search" placeholder="Search..." value="running"><textarea id="compose">Name: Home\nImage: nginx</textarea>
  <table data-cy="host-details"><tbody>
    <tr><td>Hostname</td><td id="hostname">Home</td></tr>
    <tr><td>Version</td><td>29.9.3 (API: 1.56)</td></tr>
    <tr><td>Root directory</td><td>/var/lib/docker</td></tr>
    <tr><td>Disk usage</td><td id="no-data">Not available</td></tr>
  </tbody></table>
  <table id="containers"><thead><tr><th>Name</th><th>State</th><th>Image</th></tr></thead><tbody>
  <tr><td><a id="resource" title="Home">Home</a><span role="status" id="badge">Unused</span></td><td><span id="state">running</span></td><td id="image">Images</td></tr>
    <tr><td id="plain-name">Running</td><td>healthy</td><td>traefik/whoami:v1.10.3@sha256:abc</td></tr>
  </tbody></table>
  <a id="icon-help" title="Documentation"><svg></svg></a>
  <table><thead><tr><th>Name</th><th>Value</th></tr></thead><tbody><tr><td colspan="2" id="empty">No filter available.</td></tr></tbody></table>
  <input type="checkbox" aria-label="Select row" id="select-row">
  <table><thead><tr><th>Group mode</th></tr></thead><tbody><tr><td id="group-mode">Static</td></tr></tbody></table>
  <div data-cy="environment-card-Settings"><span class="break-words font-bold" id="env-name">Settings</span><span class="font-bold" id="heartbeat">Heartbeat</span></div>
  <span class="sr-only" id="counter"></span>
  <pre class="log_viewer" id="logs">running\nHome\nPortainer</pre>
  <code id="command">docker stop Home</code><div class="cm-editor" id="editor">Restart: unless-stopped</div>
  <div contenteditable="true" id="editable">Containers</div>
  <span data-edgestack-raw id="raw">Password</span>
  <button id="remove" data-resource-id="container-123">Remove</button>
  </body></html>`);
  let browser;
  try {
    browser = await chromium.launch({ headless: true, channel: process.env.EDGESTACK_TEST_BROWSER || undefined });
    const page = await browser.newPage();
    const errors = [];
    page.on('pageerror', (error) => errors.push(error.message));
    await page.goto(pathToFileURL(fixture).href);
    try {
      await page.waitForFunction(() => document.querySelector('h1').textContent === '主机概览', null, { timeout: 5000 });
    } catch (error) {
      console.error(JSON.stringify({ errors, body: await page.locator('body').innerText(), runtime: await page.evaluate(() => window.EdgeStackUI || null) }, null, 2));
      throw error;
    }
    const snapshot = await page.evaluate(() => ({
      lang: document.documentElement.lang,
      title: document.title,
      home: document.querySelector('#home').textContent,
      hostname: document.querySelector('#hostname').textContent,
      noData: document.querySelector('#no-data').textContent,
      resource: document.querySelector('#resource').textContent,
      resourceTitle: document.querySelector('#resource').title,
      name: document.querySelector('#plain-name').textContent,
      image: document.querySelector('#image').textContent,
      state: document.querySelector('#state').textContent,
      input: document.querySelector('#search').value,
      placeholder: document.querySelector('#search').placeholder,
      textarea: document.querySelector('#compose').value,
      logs: document.querySelector('#logs').textContent,
      code: document.querySelector('#command').textContent,
      editor: document.querySelector('#editor').textContent,
      editable: document.querySelector('#editable').textContent,
      raw: document.querySelector('#raw').textContent,
      environment: document.querySelector('#env-name').textContent,
      refresh: document.querySelector('#refresh').title,
      logo: document.querySelector('img').alt,
      badge: document.querySelector('#badge').textContent,
      iconHelp: document.querySelector('#icon-help').title,
      empty: document.querySelector('#empty').textContent,
      selectRow: document.querySelector('#select-row').getAttribute('aria-label'),
      heartbeat: document.querySelector('#heartbeat').textContent,
      groupMode: document.querySelector('#group-mode').textContent,
    }));
    assert.deepEqual(snapshot, {
      lang: 'zh-CN', title: 'EdgeStack.io', home: '首页', hostname: 'Home',
      noData: '暂无数据', resource: 'Home', resourceTitle: 'Home', name: 'Running',
      image: 'Images', state: '运行中', input: 'running', placeholder: '搜索…',
      textarea: 'Name: Home\nImage: nginx', logs: 'running\nHome\nPortainer',
      code: 'docker stop Home', editor: 'Restart: unless-stopped',
      editable: 'Containers', raw: 'Password', environment: 'Settings',
      refresh: '刷新页面', logo: 'EdgeStack.io',
      badge: '未使用', iconHelp: '使用文档', empty: '尚未设置过滤条件。', selectRow: '选择此行',
      heartbeat: '心跳正常',
      groupMode: '静态分组',
    });
    await page.evaluate(() => {
      document.querySelector('#remove').onclick = (event) => {
        window.clickedResource = event.currentTarget.dataset.resourceId;
      };
      document.querySelector('h1').textContent = 'Containers';
      document.querySelector('#state').firstChild.nodeValue = 'stopped';
      document.querySelector('#logs').append('\nhealthy');
      const row = document.createElement('tr');
      row.innerHTML = '<td id="new-name">Volumes</td><td id="new-state">healthy</td><td>Settings</td>';
      document.querySelector('#containers tbody').append(row);
      document.title = 'rk3588-demo-01';
      document.querySelector('#counter').append('2', ' of ', '11', ' containers running');
    });
    await page.waitForFunction(() => document.querySelector('h1').textContent === '容器' && document.querySelector('#new-state').textContent === '健康');
    await page.click('#remove');
    assert.deepEqual(await page.evaluate(() => [
      document.querySelector('#state').textContent,
      document.querySelector('#new-name').textContent,
      document.querySelector('#logs').textContent,
      document.title,
      window.clickedResource,
      document.querySelector('#counter').textContent,
    ]), ['已停止', 'Volumes', 'running\nHome\nPortainer\nhealthy', 'EdgeStack.io · rk3588-demo-01', 'container-123', '2 / 11 个容器运行中']);
    assert.deepEqual(errors, []);
    console.log(JSON.stringify({
      status: 'passed', version: manifest.version, phrase_count: manifest.phrase_count,
      checks: ['Chinese labels and accessibility copy', 'EdgeStack branding',
        'resource names, images and form values preserved', 'logs and code preserved',
        'React/Angular-style asynchronous updates', 'action handlers retain resource IDs',
        'no uncaught browser errors'],
    }, null, 2));
  } finally {
    if (browser) await browser.close();
    fs.rmSync(temp, { recursive: true, force: true });
  }
})().catch((error) => { console.error(error); process.exitCode = 1; });
