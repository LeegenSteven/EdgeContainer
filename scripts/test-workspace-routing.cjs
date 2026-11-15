// Focused startup routing race checks. No API, real data, browser profile or
// authentication bypass is used; the restricted route is registered normally.
const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const { createRequire } = require("node:module");
const { JSDOM } = createRequire(path.resolve("portainer/package.json"))(
  "jsdom",
);
const source = fs
  .readFileSync("portainer/app/edgestack/workspace.js", "utf8")
  .replaceAll("__EDGESTACK_VERSION__", "routing-test");

async function fixture({
  hash = "#!/home",
  admin = false,
  current = "portainer.home",
  active,
  rejection,
  duplicate,
} = {}) {
  const dom = new JSDOM(
    '<!doctype html><html><body><main ng-app><aside id="sideview"><div class="sidebar"></div></aside></main></body></html>',
    {
      url: "http://localhost/" + hash,
      runScripts: "outside-only",
      pretendToBeVisual: true,
    },
  );
  const { window } = dom;
  let resolve, reject;
  const starts=[];
  const calls = [],
    errors = [],
    hooks = [],
    routes = new Map();
  const pending = new Promise((yes, no) => {
    resolve = yes;
    reject = no;
  });
  const router = {
    current: { name: current },
    params: { view: "space" },
    transition: active ? { to: () => ({ name: active }) } : undefined,
    go(name, params) {
      calls.push({ name, params });
      return pending;
    },
  };
  const services = {
    $state: router,
    Authentication: { isAuthenticated: () => true, isPureAdmin: () => admin },
    $stateRegistry: {
      get: (name) => routes.get(name),
      register: (route) => routes.set(route.name, route),
    },
    $transitions: { onSuccess: (_, hook) => hooks.push(hook) },
  };
  window.angular = {
    element: () => ({ injector: () => ({ get: (name) => services[name] }) }),
  };
  window.setInterval = (callback) => {
    starts.push(callback);
    return starts.length;
  };
  window.clearInterval = () => {};
  window.setTimeout = () => 2;
  window.requestAnimationFrame = () => 0;
  window.console.error = (...args) => errors.push(args);
  try {
    window.eval(source);
    if(duplicate)window.eval(source);
    starts.forEach(start=>start());
    assert.equal(hooks.length,1);
    assert(routes.has("portainer.workspace"));
    assert.equal(routes.get("portainer.workspace").data.access, "restricted");
    hooks.forEach((hook) => hook({ to: () => ({ name: "portainer.home" }) }));
    hooks.forEach((hook) => hook({ to: () => ({ name: "portainer.home" }) }));
    if (rejection) reject(rejection);
    else {
      router.current = { name: "portainer.workspace" };
      resolve();
    }
    await Promise.resolve();
    await Promise.resolve();
    return { calls: JSON.parse(JSON.stringify(calls)), errors };
  } finally {
    window.close();
  }
}

(async () => {
  let r = await fixture();
  assert.equal(r.calls.length, 1);
  assert.deepEqual(r.calls[0], {
    name: "portainer.workspace",
    params: { view: "space", device: null },
  });
  assert.equal(r.errors.length, 0);
  r=await fixture({duplicate:true});assert.equal(r.calls.length,1);
  r = await fixture({
    hash: "#!/workspace?view=library&device=example-device",
  });
  assert.equal(r.calls.length, 1);
  assert.deepEqual(r.calls[0].params, {
    view: "library",
    device: "example-device",
  });
  r = await fixture({ hash: "#!/edgestack?view=events&device=example-device" });
  assert.equal(r.calls.length, 1);
  assert.deepEqual(r.calls[0].params, {
    view: "interactions",
    device: "example-device",
  });
  for (const active of ["portainer.workspace", "portainer.edgestack"]) {
    r = await fixture({ hash: "#!/workspace?view=space", active });
    assert.equal(r.calls.length, 0);
  }
  r = await fixture({
    hash: "#!/workspace?view=space",
    current: "portainer.workspace",
  });
  assert.equal(r.calls.length, 0);
  r = await fixture({ admin: true });
  assert.equal(r.calls.length, 0);
  r = await fixture({ admin: true, hash: "#!/workspace?view=space" });
  assert.equal(r.calls.length, 1);
  r = await fixture({
    rejection: { type: 2, message: "expected newer navigation" },
  });
  assert.equal(r.errors.length, 0);
  r = await fixture({
    rejection: { type: 6, message: "real routing failure" },
  });
  assert.equal(r.errors.length, 1);
  assert.equal(r.errors[0][1].message, "real routing failure");
  console.log(
    JSON.stringify(
      {
        passed: true,
        checks: [
          "home/startup redirect single-flight",
          "deep-link view/device preserved",
          "legacy route preserved",
          "native pending/current workspace not superseded",
          "administrator native home retained",
          "administrator explicit preview retained",
          "only expected SUPERSEDED cancellation consumed",
          "actual routing failures reported",
        ],
      },
      null,
      2,
    ),
  );
})().catch((error) => {
  console.error(error);
  process.exitCode = 1;
});
