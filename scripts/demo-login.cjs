const assert = require('node:assert/strict');

async function signInIfNeeded(page) {
  await page.waitForFunction(() => Boolean(window.EdgeStackDemo));
  const needsLogin = await page.evaluate(() =>
    window.__EDGESTACK_DEMO_CONFIG__.startAtLogin && !window.EdgeStackDemo.getState().loggedIn);
  if (!needsLogin) return;

  await page.locator('.demo-login-hint').waitFor();
  const admin = new URL(page.url()).hash.includes('portal=admin');
  assert.equal(await page.locator('#username').inputValue(), admin ? 'admin' : 'viewer');
  assert.equal(await page.evaluate(async () => (await fetch('/api/edgestack/devices')).status), 401);
  await page.locator('[data-cy="auth-loginButton"]').click();
  await page.waitForFunction(() => window.EdgeStackDemo.getState().loggedIn);
  const landing = admin ? '[data-cy="portainerSidebar-home"]' : '[data-edgestack-workspace]';
  await page.locator(landing).first().waitFor();
}

module.exports = { signInIfNeeded };
