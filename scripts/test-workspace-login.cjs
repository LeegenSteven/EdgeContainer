// Run against an initialized EdgeContainer instance with local authentication enabled.
const assert = require('node:assert/strict');
const { chromium } = require('playwright');

async function run() {
  const entry = new URL(process.argv[2] || 'http://127.0.0.1:19090/');
  entry.hash = '!/auth';
  const browser = await chromium.launch({
    headless: true,
    ...(process.env.CHROME_BIN ? { executablePath: process.env.CHROME_BIN } : { channel: process.env.EDGESTACK_TEST_BROWSER }),
  });
  try {
    const page = await browser.newPage({ viewport: { width: 1280, height: 800 } });
    await page.goto(entry.href);
    const form = page.locator('.ew-login');
    await form.getByLabel('用户名', { exact: true }).waitFor();
    assert.match(await form.getByRole('heading', { level: 1 }).innerText(), /登录我的空间/);
    const password = form.locator('input[name="password"]');
    assert.equal(await password.getAttribute('autocomplete'), 'current-password');
    await password.fill('local-visibility-check');
    await form.getByRole('button', { name: '显示密码' }).click();
    assert.equal(await password.getAttribute('type'), 'text');
    await form.getByRole('button', { name: '隐藏密码' }).click();
    assert.equal(await password.getAttribute('type'), 'password');

    await form.getByRole('link', { name: '管理员入口' }).click();
    await page.locator('.simple-box').getByText('管理员登录', { exact: true }).waitFor();
    assert.match(page.url(), /portal=admin/);
    assert.equal(await page.locator('input[name="password"]').inputValue(), '');
    await page.reload();
    await page.getByRole('link', { name: '返回用户登录' }).click();
    await form.getByLabel('密码', { exact: true }).waitFor();
    assert.doesNotMatch(page.url(), /portal=admin/);
    assert.equal(await password.inputValue(), '');

    for (const [width, height] of [[390, 844], [320, 568]]) {
      await page.setViewportSize({ width, height });
      const button = form.getByRole('button', { name: '进入我的空间' });
      const bounds = await button.boundingBox();
      assert.ok(bounds && bounds.y >= 0 && bounds.y + bounds.height <= height, 'submit stays in the mobile viewport');
      assert.ok(await page.evaluate(() => document.documentElement.scrollWidth <= document.documentElement.clientWidth));
    }
    console.log('Workspace and administrator entries, password controls, and mobile layout passed.');
  } finally {
    await browser.close();
  }
}

run().catch(error => {
  console.error(error);
  process.exitCode = 1;
});
