// Test-only fake API for the isolated native-bundle smoke. Every identifier,
// package, account scope, token and operation below is a synthetic fixture.
// Never import this adapter from a production or conference build.
(function () {
  const original = window.EdgeStackDemoBusiness.create;
  window.EdgeStackDemoBusiness.create = function (context) {
    context.db.demoPersona = "admin";
    context.db.loggedIn = true;
    const app = original(context),
      b = context.db.demoBusiness;
    const clone = (value) => JSON.parse(JSON.stringify(value));
    const meta = {
      metadataVersion: 1,
      description: "仅用于隔离测试的虚构登记",
      userIds: [10001],
      teamIds: [],
      archived: false,
    };
    const devices = b.devices.map((d) => ({
      ...meta,
      id: d.id,
      name: d.name,
      endpointId: 4,
      architecture: "arm64",
    }));
    const tasks = b.devices
      .flatMap((d) => d.tasks)
      .map((t, i) => ({
        ...clone(t),
        ...meta,
        target: { endpointId: 4, stackId: 1, serviceId: "demo" },
        testTask: i === 0,
        runtimeBackend: "example-backend",
        templateId: "example-template",
        modelEnvKeys: [],
      }));
    const sources = b.devices
      .flatMap((d) => d.sources)
      .map((s) => ({ ...clone(s), ...meta }));
    const models = b.packages.map((p) => ({
      ...clone(p),
      ...meta,
      architectures: ["arm64"],
      image: "example.invalid/model@sha256:" + "a".repeat(64),
      modelId: "example-weights",
      modelHash: "sha256:" + "b".repeat(64),
      environment: {},
      validationStatus: "passed",
    }));
    const credentials = [];
    const operations = [];
    const audit = [];
    const validations = [];
    let cloud = {
      metadataVersion: 1,
      provider: "deepseek",
      model: "example-summary-model",
      source: "hostfile",
      hasKey: true,
    };
    const control = (window.__ADMIN_TEST__ = {
      fixture: true,
      requests: [],
      casNext: false,
      devices,
      tasks,
      sources,
      models,
      credentials,
      operations,
      audit,
    });
    const json = context.jsonResponse;
    const failure = (errorCode, status = 400) =>
      json({ errorCode, fixture: true }, status);
    const table = {
      devices,
      tasks,
      sources,
      models,
      "ingest-credentials": credentials,
      operations,
      audit,
    };
    return {
      ...app,
      route(method, path, q, body) {
        if (!path.startsWith("/edgestack/admin/"))
          return app.route(method, path, q, body);
        control.requests.push({ method, path, body: clone(body) });
        const p = path.slice("/edgestack/admin/".length),
          parts = p.split("/");
        if (p === "settings/cloud") {
          if (method === "GET") return json(cloud);
          if (control.failKey)
            return failure("provider_authentication_failed", 502);
          if (body.metadataVersion !== cloud.metadataVersion)
            return failure("cloud_settings_conflict", 409);
          cloud = {
            ...cloud,
            metadataVersion: cloud.metadataVersion + 1,
            model: body.model,
            source: body.apiKey ? "managed" : cloud.source,
          };
          return json(cloud);
        }
        if (p === "settings/cloud/test")
          return json({
            ok: true,
            ...cloud,
            availableModels: ["example-summary-model"],
            checkedAt: new Date().toISOString(),
          });
        if (p === "settings/cloud/restore-hostfile") {
          cloud = {
            ...cloud,
            metadataVersion: cloud.metadataVersion + 1,
            source: "hostfile",
          };
          return json(cloud);
        }
        if (p === "access-preview")
          return json({
            userId: +q.get("userId"),
            deviceId: q.get("deviceId"),
            taskId: q.get("taskId"),
            deviceBusinessAccess: true,
            taskBusinessAccess: true,
            nativeEndpointAccess: false,
            effectiveAccess: false,
            missing: ["native_endpoint_permission"],
          });
        if (
          parts[0] === "models" &&
          parts[2] === "validations" &&
          method === "GET"
        )
          return json({
            items: validations,
            totalCount: validations.length,
            page: 1,
            pageSize: 100,
          });
        if (parts[0] === "models" && parts[2] === "validation-preview")
          return json({
            id: "test-preview",
            taskId: body.taskId,
            packageId: parts[1],
            slot: body.slot,
            revision: body.revision,
            target: { endpointId: 4, stackId: 1, serviceId: "demo" },
            effects: [
              "Wait for a trusted runtime receipt before marking the model loaded",
            ],
            expiresAt: new Date(Date.now() + 300000).toISOString(),
          });
        if (parts[0] === "models" && parts[2] === "validations") {
          const op = {
            id: "test-operation",
            taskId: tasks[0].id,
            deviceId: devices[0].id,
            packageId: parts[1],
            status: "awaiting_observation",
            revision: 2,
            createdAt: new Date().toISOString(),
          };
          operations.push(op);
          return json(op, 202);
        }
        if (parts[0] === "models" && parts[2] === "publication") {
          const m = models.find((x) => x.id === parts[1]);
          m.published = body.published;
          m.metadataVersion++;
          return json(m);
        }
        if (parts[0] === "ingest-credentials" && method === "POST") {
          const c = {
            ...body,
            id: "test-credential-" + (credentials.length + 1),
            revoked: false,
          };
          credentials.push(c);
          return json(
            { id: c.id, token: "DEMO_NOT_A_REAL_ONCE_TOKEN", credential: c },
            201,
          );
        }
        const rows = table[parts[0]];
        if (rows) {
          if (method === "GET") {
            if (parts[1]) {
              const item = rows.find((x) => x.id === parts[1]);
              return item ? json(item) : failure("not_found", 404);
            }
            const items = rows.filter(
              (x) => !q.get("q") || (x.name || x.id).includes(q.get("q")),
            );
            return json({
              items,
              totalCount: items.length,
              page: 1,
              pageSize: 100,
            });
          }
          if (method === "PATCH") {
            const item = rows.find((x) => x.id === parts[1]);
            if (control.casNext) {
              control.casNext = false;
              return failure("metadata_version_conflict", 409);
            }
            if (body.metadataVersion !== item.metadataVersion)
              return failure("metadata_version_conflict", 409);
            Object.assign(item, body, {
              metadataVersion: item.metadataVersion + 1,
            });
            audit.push({
              id: "test-audit",
              actorUserId: 1,
              action: "metadata.update",
              resourceId: item.id,
              result: "succeeded",
              occurredAt: new Date().toISOString(),
              fields: ["name"],
            });
            return json(item);
          }
          if (method === "POST") {
            rows.push({ ...body, metadataVersion: 1 });
            return json(rows[rows.length - 1], 201);
          }
          if (method === "DELETE") {
            const c = rows.find((x) => x.id === parts[1]);
            c.revoked = true;
            return json({}, 204);
          }
        }
        return failure("test_route_not_implemented", 404);
      },
    };
  };
})();
