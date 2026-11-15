# EdgeStack business API

This in-process module stores its business records in `<Portainer data>/edgestack/business.db` (normally `/data/edgestack/business.db`). Original administration APIs and their `AdminAccess` middleware are unchanged. Device visibility requires both the business ACL and the existing Portainer endpoint/group authorization. Task ACLs apply in addition to the device ACL. An empty ACL grants no business-user access; administrators retain administrative access.

Authenticated routes use the existing Portainer session, including its HttpOnly cookie. They are under `/api/edgestack`:

- `GET /devices`, `GET /devices/{id}`, `GET /devices/{id}/event-sources`
- `GET /events?deviceId=...&from=...&to=...&type=voice.user_utterance,voice.assistant_reply&sessionId=...&cursor=...&limit=100`
- `GET /tasks/{id}/models`
- `POST /tasks/{id}/model-change-preview`: `{ "packageId": "...", "slot": "llm", "revision": 1 }`
- `POST /tasks/{id}/model-changes`: `{ "previewId": "...", "confirm": true }`
- `GET /operations/{id}`
- `POST /summaries`: `{ "deviceId": "...", "from": "UTC RFC3339", "to": "UTC RFC3339", "question": "..." }`
- `GET /summaries/{id}`
- `GET /tasks/{id}/voice-test` and `/tasks/{id}/voice/{path}` when the task has `voice-test` capability and an operator-configured `voiceOrigin`.

Administration-only provisioning uses `GET`/`POST /admin/devices`, `/admin/tasks`, `/admin/sources`, and `/admin/models`; `GET`/`POST /admin/ingest-credentials`; and `DELETE /admin/ingest-credentials/{id}`. Model package bodies are immutable after creation. New packages start as unpublished drafts and require `modelId`, `runtimeBackend`, compatible `templateIds`, a pinned image digest and model hash. Publication uses `POST /admin/models/{id}/publication` with `{"metadataVersion":1,"published":false}` or `{"metadataVersion":1,"published":true}` and is subject to the validation/scope rules below. Full JSON types are in `types.go`.

Event type/session filters apply after authorization and the time window, before pagination. `type` accepts a single supported event type or comma-separated supported types; unknown types are rejected. Cursors resolve only within the matching set. `filteredCount` reports the full matching count before pagination; `coverage.eventCount` retains the authorized whole-window total.

Public `dataLocations` entries default to `verification:"registered"`. An operator may opt a Worker location into narrow live verification by registering an explicit `containerPath` and optional `expectedReadOnly` (boolean) or `volumeName`. After business task/device ACLs and the original endpoint authorization pass, the server uses the original Docker client factory and Edge reverse tunnel to list containers with both the exact `io.portainer.edge_stack_id` and `com.docker.compose.service` labels. It inspects exactly one running matching container, rechecks its identity and running state, and matches the exact registered mount destination and expectations. A match produces only `verification:"verified"` and `observedAt`; otherwise the location remains registered with a bounded `verificationReason`. Raw inspect data, environment values, other mounts and actual host sources are never returned by this provider. Registered friendly names and location text remain visible.

Location network queries run outside business database transactions, share a two-second projection budget, and cache observations for eight seconds. Concurrent requests share one in-flight target query. Query failures, ambiguity, missing paths, wrong read-only/volume expectations, and slow tunnel establishment all remain conservative registered results. Existing server-managed record locations (`kind:"server"`) remain registered with `verificationReason:"server_managed_records"` and are excluded from Worker mount checks. Without an explicit `containerPath`, a legacy location stays registered. These projection fields are derived by the server; neither a client nor an operator-supplied `verification`/`observedAt` value can self-certify a location. Authorization and target revision are checked again before a delayed projection is returned.

Task `runtimeState` is separate from administrator-registered `state`. Only the same unique-running-container List/Inspect proof yields `runtimeState:"running"` with `runtimeObservedAt`; query failure or unavailable proof yields `unknown` without a timestamp. Runtime checks also work when no mount paths are registered, sharing the same two-second budget, eight-second snapshot cache and before/after authorization. Running describes the container process only, not business health or a loaded model; model observations never imply container runtime state.

## Ingestion

`POST /ingest/events` and `/ingest/status` require `Authorization: Bearer es_ingest_...`. This credential is generated independently of Portainer sessions, administrator keys and `EDGE_KEY`. Only its SHA-256 hash is stored. Its explicit device, task, source and capability scopes must all match. It can be revoked without modifying the worker's infrastructure identity.

Event requests contain `{"events":[Event]}`. Each event includes `schemaVersion:1`, `eventId`, `sourceId`, `taskId`, `type`, `occurredAt`, `sessionId`, `sequence` and `text`. The server derives `deviceId` from the source and assigns `receivedAt`. Optional fields include `replyToEventId`, `provenance`, `phase` and `metadata`. Accepted types are `voice.user_utterance`, `voice.assistant_reply`, `camera.observation` and `voice.playback_state`. Playback state is retained but excluded from conversational cloud summaries. Integration-test provenance is preserved in evidence and identified explicitly in the summary prompt.

A batch contains at most 500 events, with a 2 MiB JSON limit and 16,384 bytes per event text. Identical stable IDs replay safely; changing the content of an existing ID produces a conflict. A rejected batch commits no events. Events and summaries have a seven-day retention boundary; operation/audit metadata is retained for 90 days; event/evidence reads enforce retention even between cleanup runs.

Status requests contain `sourceId`, `taskId`, `status`, `checkpoint`, optional `lastEventAt`, `errorCode` and `coverage`. Checkpoints and coverage are JSON objects or scalar values. Supported adapter states include `streaming`, `backpressure`, `blocked`, `connected`, `healthy`, `disconnected`, `error`, `idle`, `unconfigured` and `unsupported`. Coverage reports missing sources, disconnections, disabled sources and producer-history gaps instead of interpreting an empty history as no interaction.

Conversation coverage counts only voice/camera producers. Model observer health remains visible in `sources` but does not establish or reduce conversation coverage. A producer can establish complete coverage only with an explicit `complete:true`, no gaps, and `from`/`to` (or `observedFrom`/`observedTo`) boundaries enclosing the requested window. Missing history or incomplete boundaries produce partial coverage even if the source is currently healthy.

## Model changes

Only a published immutable catalog package compatible with the registered task kind, architecture and slot can be selected. Packages pin both an image digest and a model hash. Task `modelEnvKeys` names the only existing environment variables a package may replace. A task must have explicit `replace-model` capability. The dispatcher supports independent, single-service Compose Edge Stacks attached to exactly the approved Docker Edge endpoint; shared stacks and arbitrary containers are unavailable.

Withdrawing publication removes a package from new selections and blocks confirmation of an already-created preview while it remains withdrawn. It does not stop a running task or hide a trusted loaded-model receipt matching the immutable identity/hash. A repeated bootstrap import compares immutable package identity without `published` and preserves the database's existing publication state, so an older operator file cannot revive a withdrawn package. Administrators may explicitly republish it through the publication endpoint.

A preview binds the current task revision, target, current Edge Stack version and full Compose hash. Confirmation rechecks all bindings and authorization. The update changes only the selected image and approved environment values, retaining unrelated environment values, ports, volumes, networks and service settings. It uses the existing Edge Stack file/version/status deployment mechanism.

Preview and operation `previousPackageId` preserve the registered model package in the selected task slot before the change. Older records with an absent field remain readable; confirming an older preview derives this value from the still-matching task revision.

If a file write or later status/metadata update fails, the dispatcher restores the previous Compose bytes before releasing the datastore write lock. A commit failure triggers a guarded restoration only while the old database version and emitted bytes still match, preserving intervening administrator edits. A runtime-reported failure or observation timeout can be followed by a fresh preview and explicit confirmation of the previous published compatible package; recovery still requires a trusted model receipt. There is no automatic runtime rollback or cancellation of an in-flight change.

Successful dispatch means `awaiting_observation`, never “loaded.” Only a separately scoped `observe-model` credential can submit `observations:[{slot,modelId,modelHash,observedAt,state:"loaded"}]` through the matching source's status endpoint. A loaded receipt must provide the actual loaded model identity and SHA-256 hash matching the current registered immutable package. The server resolves the pending operation using task, slot, package and revision; a receipt older than the deployment is rejected. A matching receipt can also confirm the initial model binding. Optional `operationId`, `packageId` and `revision` must match if supplied, and cannot substitute for actual identity/hash. A loaded receipt makes the operation `succeeded`. Missing receipts time out after 15 minutes with `model_observation_timeout`; a later trusted receipt may confirm the unchanged revision. Confirmation replays never dispatch twice.

## DeepSeek

`DEEPSEEK_API_KEY_FILE` supplies the initial host-file secret. Administrators may explicitly replace it through the cloud settings API; managed secrets remain in private server files, while the database stores only an opaque reference. Web settings remain authoritative across restart and bootstrap. The key remains on the server. `DEEPSEEK_MODEL` initially defaults to `deepseek-flash`. The fixed official endpoint is `https://api.deepseek.com/chat/completions`, with JSON output, `thinking:{"type":"disabled"}`, `max_tokens:2048`, a 60-second deadline, and no automatic retry. Cloud calls occur only after an explicit summary POST with authorized recorded evidence; zero events produces `no_data` without a cloud call. The server validates topic references, constructs evidence itself, and rechecks current authentication, endpoint access and task access before persisting or returning results. Provider failures expose bounded error codes without provider response bodies or secret values.

Each request permits at most 200 eligible events and 64 KiB of input text, including the question and event text/provenance/phase. Larger ranges return `summary_range_too_large` without truncating or calling the cloud; select a smaller time range. Each user may have only one summary in progress (`summary_already_in_progress`), with two concurrent cloud calls globally (`summary_capacity_reached`). The server creates a redacted snapshot before generation and persistence: exact server API-key replacement plus common bearer/basic, `sk-`, ingestion/JWT tokens, assigned passwords/API keys/tokens and nested credential fields. Original local events are not rewritten. Cloud evidence omits original event IDs, and sensitive reference IDs become stable hashes. Server-derived coverage is included in provider input and cannot be supplied by a client. Output is requested in Simplified Chinese; `finish_reason:"length"` fails as `provider_output_truncated` even when its partial JSON parses. Every generated topic needs valid evidence references, and summary/answer are instructed to summarize those topics only.

Summary prose is requested in plain Simplified Chinese without internal protocol field names, error codes or raw UTC ranges. An empty question produces a general summary with an empty `answer`, without asking the user to supply a question. Topics must be nonempty and each topic must cite valid evidence references.

## Startup operator configuration

`EDGESTACK_BOOTSTRAP_FILE` optionally imports a private regular JSON file (0600) at startup. This is an operator-only file import, not an HTTP endpoint. Its schema is:

```json
{
  "deployments": [{
    "name": "edgestack-example-voice",
    "endpointId": 1,
    "edgeGroupName": "example-workers",
    "compose": "services:\n  voice:\n    image: registry/image@sha256:<approved digest>\n"
  }],
  "devices": [],
  "tasks": [],
  "sources": [],
  "models": [],
  "credentials": []
}
```

Each deployment uses the existing internal Edge Stack creation service, requires a single-device Edge Group and a single-service Compose manifest, and has a name starting with `edgestack-`. It never overwrites an existing stack. A task may resolve its numeric target through `target.stackName`. Its endpoint/service/stack binding is validated before registration. Startup reimports seed missing IDs only. Existing device/task/source metadata, ACLs, publication policy and credential state remain database-authoritative, even when an old file differs. Existing registered tasks do not trigger recreation or re-resolution of old deployment targets. New seeded model packages remain drafts. Explicit metadata updates use the preview/confirm import flow below. Credential entries contain the regular credential fields plus `tokenHash`, the 64-character lowercase hex SHA-256 hash of the independently generated ingestion token; no plaintext token is needed by the server.

`EDGESTACK_VOICE_TEST_FILE` supplies the test HTML at startup. Its task proxy allows only `/v2v/stream`, `/asr/stream`, `/health` and `/model-info`, validates same-origin WebSocket requests before upgrade, and removes Portainer cookies and authorization headers before forwarding to the fixed operator-controlled upstream. The private task `voiceTokenFile` identifies a regular 0600 server secret file (at most 4096 bytes); the proxy injects its independent bearer credential after removing browser credentials. Missing or invalid upstream credentials fail before a request is forwarded. Neither `voiceOrigin` nor `voiceTokenFile` is exposed in the public task response. The test page uses a scoped CSP with hashes of its bundled inline scripts.


## Administrator workspace contracts

All routes below require the existing Portainer administrator authorization. Existing users and teams are selected from Portainer; there is no separate business identity store. `ValidateACL` verifies referenced native user/team IDs. `GET /admin/access-preview?userId=...&deviceId=...&taskId=...` reports device business access, task business access, native endpoint access, their intersection and missing permissions. It never grants native permissions. An unavailable native-access resolver returns unknown/denied rather than assuming access.

### Lists, safe editing and archival

`GET /admin/devices`, `/admin/tasks`, `/admin/sources`, `/admin/models`, `/admin/ingest-credentials`, `/admin/operations` and `/admin/audit` return `{items,totalCount,page,pageSize}`. Defaults are page 1 and page size 25; the maximum page size is 100. Lists support `q` and applicable device/task/source/status/published/archived/actor/time filters; audit results use `result`. Credential scope filters match their device/task/source arrays. Individual device/task/source/model and administrator operation records have `GET /admin/{kind}/{id}`.

Device, task and source `POST` registers a new ID; it does not overwrite existing IDs. `PATCH /admin/{devices|tasks|sources}/{id}` requires the current `metadataVersion`; stale updates return `409 metadata_version_conflict`. Example: `{"metadataVersion":1,"name":"Friendly task name","description":"Business purpose","userIds":[],"teamIds":[]}`. Empty ACLs do not grant ordinary-user access.

Device editable metadata is name, description, ACL and archival status; endpoint and architecture remain fixed. Task device, kind, deployment target and model bindings are fixed. Task name/description/ACL/data-location edits preserve deployment `revision` and all actual model observations. Changes to capabilities, approved model environment keys, voice proxy configuration, test-task designation, runtime backend or template ID increment the deployment/configuration revision and are rejected while a model change is active. Sources allow name/description/enabled/archived edits while preserving runtime status, checkpoint and history; source type and device/task binding stay fixed. Archival hides business access and stops scoped ingestion, without stopping or deleting a container.

`verification`, runtime state, loaded identity/hash and observation times are server-derived facts. New registered locations cannot assert those derived fields. Administrator task lists expose registration metadata; `GET /devices/{id}` provides authorized live runtime/mount observations. Archived or inaccessible records do not acquire live facts through a management bypass.

### Controlled model validation and publication

`POST /admin/models/{id}/validation-preview` accepts `{"taskId":"example-test-task","slot":"asr-encoder","revision":1}`. The target must be an administrator-designated `testTask:true`, independent registered deployment with matching `runtimeBackend`, `templateId`, architecture, task kind, slot and approved environment parameters. This administrator-only route can select a draft; ordinary user preview/confirmation paths cannot.

`POST /admin/models/{id}/validations` accepts `{"previewId":"...","confirm":true}` and returns the normal operation shape with `purpose:"model-validation"` and `validationId`. Dispatch alone leaves validation pending. A separately scoped trusted observer must report the actual matching immutable model identity/hash after the operation's creation. Before accepting that receipt as validation evidence, the server independently checks the unique running container's pinned image digest and approved model environment values through the existing Edge tunnel. Missing or mismatched runtime proof returns `503 model_runtime_unverified` and leaves validation pending for retry. Authentication precedes this two-second query; it runs outside database transactions, and the write transaction rechecks authorization, task revision, target, operation and package identity. `GET /admin/models/{id}/validations` lists these records and evidence metadata; no model bytes, inspect data, environment values or dialogue are included.

Administrator model records add `metadataVersion`, `validationStatus`, `legacyScoped`, `allowedTaskIds`, `legacyTaskScopes`, `validatedScopes` and `lastValidationId`. Statuses include `draft`, `validating`, `passed`, `failed`, `legacy_scoped` and restore-time `needs_review`. Publication requires a passed scope (task kind, architecture, backend and template), or an existing frozen legacy scope. Publication also uses optimistic metadata version checks. Withdrawing a package blocks new selections and old-preview confirmation but does not falsify actual loaded-model receipts.

The first upgrade freezes each existing published package to its then-compatible registered tasks and their exact kind/architecture/backend/template scope. It does not invent a validation history or automatically allow new tasks or changed execution scopes. Restoring the original task metadata restores its original legacy eligibility. A new manifest/identity is needed to add missing immutable execution requirements to a historical package. Validating a new draft never expands another package's legacy scope.

### Credentials, import and audit

Credential creation returns plaintext only once. List records expose scopes, capabilities, `createdAt`, `createdByUserId`, `lastUsedAt`, `lastSuccessfulAt`, `revokedAt` and `rotatedFromId`, never the token/hash. Usage timestamps are updated only with successfully committed ingestion/status transactions. `POST /admin/ingest-credentials/{id}/rotate` with optional `name` creates a replacement with identical immutable scope and leaves the old credential active. After observing successful use, `DELETE /admin/ingest-credentials/{oldId}?replacementId={newId}` revokes the old credential; it returns `409 rotation_not_observed` before the replacement succeeds. Plain administrator DELETE remains available for immediate revocation without a replacement.

`POST /admin/imports/preview` accepts `{"config":{"devices":[],"tasks":[],"sources":[],"models":[],"credentials":[]}}` and returns `{id,expiresAt,changes:[{kind,id,action,fields,metadataVersion}]}`. It validates the whole import in a rolled-back transaction without applying metadata or deployment changes. The response contains field names, not credential hashes or raw input. `POST /admin/imports/confirm` with `{"previewId":"...","confirm":true}` applies the saved plan atomically only for its owner, within five minutes, while all expected metadata versions still match. Confirmation is idempotent. Existing model bodies and source/task targets cannot change; credential scope/hash changes require rotation. Existing publication state is preserved.

Audit records retain actor ID (zero for unknown/system history), target IDs, action, timestamp, result and changed field names for 90 days. They do not contain plaintext credentials, source event text, questions, cloud response bodies or complete before/after configurations. Operations preserve initiating actor and deployment target snapshots alongside previous/new package identity. Historical missing actors remain unknown.

### Cloud settings

`GET /admin/settings/cloud` returns configuration metadata and key presence, never the key or private file reference. `POST /admin/settings/cloud/test` validates a candidate DeepSeek key/model against the fixed official model catalog without sending business evidence. `PUT` or `PATCH /admin/settings/cloud` requires the current `metadataVersion`, provider `deepseek`, a model, and an optional replacement `apiKey`; candidates must pass validation before activation. Key files are written privately before the database reference changes, and failed validation or version conflicts preserve the previous active configuration. `POST /admin/settings/cloud/restore-hostfile` explicitly restores the configured host-file source after validation. In-flight summaries keep the key/model snapshot they began with and never retry with a newly rotated key. Successful changes and bounded test outcomes are audited without key material or raw provider responses.

Summary requests may include an IANA `timeZone`; the selected zone is persisted as `usedTimeZone` for consistent result display while event timestamps and range filtering remain UTC.

### Backup and restore boundary

Business snapshots use bbolt transaction `WriteTo` under the existing backup coordinator; they are not raw copies of an open database and do not include the private secrets directory. Cleanup and GET-triggered expiry writes acquire the configured background mutation lease. POST handlers already protected by the HTTP mutation gate do not acquire a nested lease. Close is idempotent and stops the retention loop.

A restore marker causes startup to skip initial cleanup until the restore policy runs. A separate persistent restore guard suppresses the old operator bootstrap on the first restored startup and every later restart; it also applies to legacy archives without a business database, so absent records or deployments are not automatically seeded. Administrators must explicitly preview and confirm any subsequent configuration import. Restore revokes ingestion credentials, marks active operations and model validations `needs_review`, clears old model previews and unconfirmed import plans, validates restored native/ACL relations, and never automatically redeploys a device. Missing managed cloud secrets require reconfiguration. Startup seed cannot revive old credentials or overwrite restored database metadata. Cloud settings and private-key lifecycle are documented by their administrator settings implementation; their secret bytes are excluded from ordinary business/audit records.
