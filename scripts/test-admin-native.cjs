// Native React administration smoke using an offline HTML build and synthetic
// API records only. It launches a fresh browser profile, never opens a live
// Server, and never reads a real account, token, model or device configuration.
// Requires Playwright; see the CLI usage below. Reports stay under .local by
// default. The test-only facade is not imported by any production build.
const fs = require("node:fs");
const path = require("node:path");
const assert = require("node:assert/strict");
const { pathToFileURL } = require("node:url");
const { chromium } = require("playwright");
if (!process.argv[2]) {
  console.error(
    "Usage: node scripts/test-admin-native.cjs OFFLINE_NATIVE_HTML [REPORT_DIRECTORY]",
  );
  process.exit(2);
}
const folder = path.resolve(process.argv[3] || ".local/admin-native-ui");
fs.mkdirSync(path.join(folder, "screenshots"), { recursive: true });
const input = path.resolve(process.argv[2]),
  file = path.join(folder, "admin-native-facade.html");
assert.notEqual(
  input,
  file,
  "Choose a report directory different from the input fixture directory",
);
const original = fs.readFileSync(input, "utf8"),
  marker = "const business=window.EdgeStackDemoBusiness.create";
assert.equal(original.split(marker).length, 2);
fs.writeFileSync(
  file,
  original.replace(
    marker,
    fs.readFileSync(
      path.join(__dirname, "fixtures/admin-business.js"),
      "utf8",
    ) +
      "\n" +
      marker,
  ),
);
(async () => {
  const browser = await chromium.launch({
    headless: true,
    channel: process.env.EDGESTACK_TEST_BROWSER || "chrome",
  });
  const page = await browser.newPage({
    viewport: { width: 1470, height: 1000 },
  });
  const errors = [],
    consoleErrors = [],
    external = [],
    checks = [];
  page.on("pageerror", (e) => errors.push(e.message));
  page.on("console", (m) => {
    if (m.type() === "error") consoleErrors.push(m.text().slice(0, 500));
  });
  page.on("request", (r) => {
    if (/^https?:/.test(r.url())) external.push(r.url());
  });
  try {
    await page.goto(pathToFileURL(file).href + "#!/home", {
      waitUntil: "load",
    });
    await page.locator("[data-cy=portainerSidebar-home]").waitFor();
    assert.equal(
      await page.evaluate(() =>
        document.body.classList.contains("edgestack-user"),
      ),
      false,
    );
    checks.push("管理员原生首页与完整侧栏");
    await page.locator("[data-cy=edgestack-admin-menu-link]").click();
    for (const [section, title] of [
      ["devices", "设备业务登记"],
      ["tasks", "业务任务"],
      ["access", "业务授权"],
      ["sources", "来源与凭据"],
      ["models", "模型目录"],
      ["operations", "操作与审计"],
      ["cloud", "云端总结设置"],
    ]) {
      await page.locator("[data-cy=edgestack-admin-" + section + "]").click();
      await page.locator("[data-cy=edgestack-admin-view]").waitFor();
      await page.locator("h1").filter({ hasText: title }).waitFor();
      await page.waitForTimeout(150);
      assert.equal(
        await page.evaluate(() =>
          document.body.classList.contains("edgestack-user"),
        ),
        false,
      );
      await page.screenshot({
        path: path.join(folder, "screenshots", "native-" + section + ".png"),
        fullPage: true,
      });
      checks.push("原生路由 " + section);
      if (section === "sources") {
        for (let i = 1; i <= 2; i++) {
          await page.locator("[data-cy=credential-create]").click();
          await page
            .locator("[data-cy=business-name]")
            .fill("Same name fixture");
          await page
            .locator("[data-cy=business-sourceId]")
            .selectOption("demo-source-demo-task-1-1");
          await page
            .locator("[data-cy=business-editor] input[type=checkbox]")
            .first()
            .check();
          await page.locator("[data-cy=business-save]").click();
          const dialog = page.getByRole("dialog");
          await dialog
            .getByText("test-credential-" + i, { exact: true })
            .waitFor();
          await dialog
            .getByText("DEMO_NOT_A_REAL_ONCE_TOKEN", { exact: true })
            .waitFor();
          await page.locator("[data-cy=credential-dismiss]").click();
          await page
            .getByText("test-credential-" + i, { exact: true })
            .waitFor();
          assert.equal(
            await page
              .getByText("DEMO_NOT_A_REAL_ONCE_TOKEN", { exact: true })
              .count(),
            0,
          );
        }
        await page.locator("[data-cy=credential-revoke]").nth(1).click();
        await page
          .getByRole("dialog")
          .getByText("test-credential-2", { exact: true })
          .waitFor();
        assert.equal(
          await page
            .getByRole("dialog")
            .getByText("test-credential-1", { exact: true })
            .count(),
          0,
        );
        await page.locator("[data-cy=business-confirm-cancel]").click();
        checks.push(
          "Same-name credential IDs, one-time token clearing, revoke confirmation ID",
        );
      }
    }
    await page.evaluate(() => {
      location.hash = "#!/business/devices/demo-device-1";
    });
    const name = page.locator("[data-cy=business-name]");
    await name.waitFor();
    await name.fill("Home");
    await page.evaluate(() => (window.__ADMIN_TEST__.casNext = true));
    await page.locator("[data-cy=business-save]").click();
    await page.getByRole("alert").filter({ hasText: "刷新" }).waitFor();
    assert.equal(await name.inputValue(), "Home");
    checks.push("PATCH CAS冲突保留输入");
    await page.locator("[data-cy=business-save]").click();
    await page.waitForFunction(
      () => window.__ADMIN_TEST__.devices[0].name === "Home",
    );
    await page.locator("[data-cy=business-add-record]").waitFor();
    checks.push("原生编辑表单PATCH成功并离开已保存表单");
    await page.waitForTimeout(200);
    assert.equal(
      await page.locator("[data-cy=business-edit-record]").first().innerText(),
      "Home",
    );
    checks.push("与界面文案相同的资源名保持原值");
    await page.evaluate(() => {
      location.hash = "#!/business/cloud";
    });
    const key = page.locator("[data-cy=cloud-api-key]");
    await key.waitFor();
    await key.fill("DEMO_NOT_A_REAL_KEY");
    await page.locator("[data-cy=cloud-save]").click();
    await page.locator("[data-cy=cloud-result]").waitFor();
    assert.equal(await key.inputValue(), "");
    assert(
      !(await page.locator("body").innerText()).includes("DEMO_NOT_A_REAL_KEY"),
    );
    checks.push("云端候选Key保存后清空且不回显");
    await key.fill("DEMO_NOT_A_REAL_BAD_KEY");
    await page.evaluate(() => (window.__ADMIN_TEST__.failKey = true));
    await page.locator("[data-cy=cloud-save]").click();
    await page
      .getByRole("alert")
      .filter({ hasText: "候选密钥验证失败" })
      .waitFor();
    assert(
      (await page.getByRole("alert").innerText()).includes("当前配置未更改"),
    );
    assert.equal(await key.inputValue(), "");
    assert(
      !(await page.locator("body").innerText()).includes(
        "DEMO_NOT_A_REAL_BAD_KEY",
      ),
    );
    checks.push(
      "Chinese cloud failure, old configuration retained, candidate key cleared",
    );
    await page.evaluate(() => {
      location.hash = "#!/home";
    });
    await page.locator("[data-cy=portainerSidebar-home]").waitFor();
    assert.equal(
      await page.evaluate(() =>
        document.body.classList.contains("edgestack-user"),
      ),
      false,
    );
    checks.push("离开业务页恢复原生首页");
    assert.deepEqual(errors, []);
    assert.deepEqual(consoleErrors, []);
    assert.deepEqual(external, []);
    const result = {
      passed: true,
      fixture: true,
      input,
      sourceHtmlSha256: require("node:crypto")
        .createHash("sha256")
        .update(original)
        .digest("hex"),
      checks,
      errors,
      consoleErrors,
      external,
      requests: await page.evaluate(() =>
        window.__ADMIN_TEST__.requests.map(({ body, ...r }) => r),
      ),
    };
    fs.writeFileSync(
      path.join(folder, "native-admin-smoke.json"),
      JSON.stringify(result, null, 2),
    );
    console.log(JSON.stringify(result, null, 2));
  } catch (e) {
    await page.screenshot({
      path: path.join(folder, "screenshots", "native-smoke-failure.png"),
      fullPage: true,
    });
    fs.writeFileSync(
      path.join(folder, "native-smoke-failure.json"),
      JSON.stringify(
        {
          error: String(e),
          errors,
          consoleErrors,
          external,
          body: await page.locator("body").innerText(),
          requests: await page.evaluate(
            () =>
              window.__ADMIN_TEST__?.requests.map(({ body, ...r }) => r) || [],
          ),
        },
        null,
        2,
      ),
    );
    throw e;
  } finally {
    await browser.close();
  }
})().catch((e) => {
  console.error(e);
  process.exitCode = 1;
});
