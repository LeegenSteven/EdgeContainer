# Model delivery integration

EdgeContainer can accept model requests, run an approved AgentDP deployment, build an immutable OCI image, validate that final image on an isolated test task, and publish it according to an administrator policy. Published versions can update existing tasks or create independent tasks from installed templates.

This integration uses the existing user, team, endpoint and business permissions. The user interface is under **模型部署助手**. Administrators manage requests, jobs, recipes and publication policies under **模型目录 → 模型交付与发布**. Existing model drafts and manually validated packages remain supported.

## Workflow

1. A user submits a device-bound request and can add information after refreshing or reopening the page.
2. An administrator selects a versioned recipe and policy and registers the exact model URI, SHA-256 and model identity. Ordinary users do not supply build commands or Dockerfiles.
3. The worker claims a durable job. It freezes the recipe, source, actor and publication policy, journals AgentDP session/plan/job identities, and renews its claim. A lost or ambiguous physical job requires reconciliation.
4. AgentDP validates the prepared model under the shared device lease. The first adapters accept prepared RKNN models for Qwen3 ASR encoders and YOLOv8n/v8s. Conversion from other input formats requires a separately installed adapter.
5. BuildKit builds from a digest-pinned runtime base and an approved build context. Skopeo copies the OCI manifest to the LAN registry and reads its digest back. No container commit is used.
6. The final image is deployed to the recipe's registered isolated test task. Its actual image and approved parameters are inspected independently by Server. The runtime performs a frozen inference workload and sends scoped, trusted model receipts. Native-only success does not publish an image.
7. Server checks metrics, source/model/recipe/runtime identities, artifact integrity and the current policy. Eligible images are automatically published. A policy change, revoked endpoint access, expired claim or stale control epoch prevents publication.
8. A user chooses an authorized published model, adds targets, reviews the plan, and explicitly confirms. The first target is the canary. Each target needs three health checks, at least ten seconds apart, and a 120-second observation period before continuing. Failure starts restoration and stops later targets. Unconfirmed restoration is shown as requiring review.

A failed stage can be retried under administrator authorization. Unchanged inputs reuse the original job ID and worker checkpoints; changed recipe or policy snapshots create a new job.

Model requests, job attempts, artifacts, plans, rollout targets and observations are persisted in the business database. Restore invalidates active delivery work and credentials, disables publication policies and requires reconciliation. The external control epoch and observer derivation key are private files outside ordinary database backups.

## Components and configuration

| Component | Configuration |
| --- | --- |
| Server feature | `EDGESTACK_DELIVERY_ENABLED=true` |
| Worker authentication | `EDGESTACK_DELIVERY_WORKER_TOKEN_FILE`, a regular private `0600` file |
| Installed task templates | `EDGESTACK_DELIVERY_TEMPLATES_FILE`, a private JSON map of template IDs to `serviceId`, `compose`, `modelEnvKeys` |
| Runtime callback URL | `EDGESTACK_DELIVERY_OBSERVER_URL`, the Server URL reachable from target containers |
| Signed artifact downloads | `EDGESTACK_DELIVERY_ARTIFACT_ROOT` and `EDGESTACK_DELIVERY_TRUSTED_PUBLIC_KEY_FILE` |
| Shared execution lease | `EDGESTACK_LEASE_URL`, `EDGESTACK_LEASE_TOKEN_FILE`, `EDGESTACK_DELIVERY_RESOURCES_FILE` |
| Worker installation | [worker/model-delivery](../worker/model-delivery/README.md), [host configuration](../deploy/model-delivery/README.md) |

Run the upgraded Server and **synchronous Docker Edge Agent** together. The Agent advertises execution-grant support and rechecks the live lease before pulling, deploying or removing a managed task. It records accepted fences and managed task markers on persistent storage. Unsupported asynchronous execution fails closed. Managed targets reject conflicting native mutations and GitOps configuration.

New task templates must define one independent Compose service, approved model environment keys, preserved data volumes and a real health check. The target device needs an independent single-device Edge group. Server injects a task-scoped observation secret at `/run/secrets/edgestack-model-observer`; its value is not added to an image layer or environment variable. Native administrators retain privileged access to private Compose configuration.

Runtime bases must contain an approved runtime configuration, driver-compatible dependencies, a profile manifest and frozen validation inputs. ASR bases also provide the normal authenticated ASR service configuration. Camera capture and business pipelines must be supplied by the approved base image. The included YOLO adapter validates actual RKNN tensor inference; a reference tensor comparison does not establish application accuracy on unseen camera data. Existing voice proxy ports and upstream secrets must match their installed template when replacing an existing service. A newly provisioned ASR task initially exposes model deployment and observation; an administrator can enable its user voice-test proxy by registering the correct `VoiceOrigin`, private `VoiceTokenFile`, `voice-test` capability and interaction sources in task/source management.

Recipe and worker examples ship disabled with explicit placeholder digests. Populate them with installed assets and measured gates before enabling them. Browser configuration does not install host executables, registry trust, runtime libraries or signing keys.

## Artifact identities and offline transfer

The following identities remain separate:

- OCI manifest digest: the deployable registry image identity.
- OCI config digest: Docker image configuration identity.
- Outer archive SHA-256: integrity of the exported offline bundle.
- Model, recipe, runtime profile, release and report digests: provenance and validation binding.

The offline bundle contains an OCI image archive, signed manifest and validation report. Server exports it only within a configured artifact root after verifying the outer hash, Ed25519 signature, bound report and OCI blobs. The worker's import command verifies the bundle before copying the image to another approved registry. Registry digest preservation is checked after copying. Trust the signing public key through the deployment configuration, not a key supplied by the bundle itself.

## API outline

User endpoints are under `/api/edgestack`:

- `GET/POST /model-requests`, `GET /model-requests/{id}`, `POST /model-requests/{id}/messages`
- `GET /delivery/catalog`, `/delivery/jobs`, `/delivery/artifacts`
- `GET /delivery/artifacts/{id}/export` and `/export-manifest`
- `POST /deployment-plans`, `POST /deployment-plans/{id}/confirm`
- `GET /rollouts` and `/rollouts/{id}`

Resident test-device leases are visible in **设备租约**. Administrators preview stopping isolated test tasks, then explicitly confirm release; interrupted stops reconcile the same durable preview. The `allowStopValidationTask` policy flag separately permits a claimed build job to stop its bound isolated validation task before native validation. Business tasks are excluded from this automatic handoff.

The worker selects the AgentDP board using the frozen validation device. The initial handoff requires the current isolated Compose stack version to report completion at zero replicas. After a confirmed resident stop, its matching stop record can also establish that the current version was stopped. In both cases the server inspects the entire Compose project twice before returning ready. An older unversioned completion requires a fresh deployment report for this initial handoff. Each release health check takes a fresh Docker observation of the image, model parameters and health. A new-task rollback restores the previous Compose file if the matching database update fails. Before confirming success it checks the current stack version and two separate observations of the stopped containers.

Administrator endpoints include `/admin/model-requests`, request `/resolve` and `/retry`, `/admin/delivery/recipes`, `/policies`, `/jobs`, `/artifacts` and `/settings`. Versioned recipe/policy writes use `metadataVersion` and reject stale updates.

Worker endpoints are a narrow, separately authenticated internal API for claim, heartbeat, artifact result and final validation start/status/complete. They cannot proxy arbitrary Server or AgentDP operations. Browser-facing projections omit claim secrets and observer tokens.

## Verification and deployment acceptance

Automated checks cover authorization, persistence, epoch and claim fencing, immutable result identity, final validation, publication revocation, signed OCI export, executor grants, worker recovery and UI confirmation. The standalone demo uses explicitly invented data and never builds images or calls devices.

Production acceptance additionally requires configured runtime bases, a trusted LAN registry, signing keys, the shared lease service and upgraded Edge Agents. Verify both model families on actual RK3588 hardware, interrupt a build and a deployment, restore the previous image after a failed health check, and deploy a saved bundle to a second compatible device without its source model directory. Record measured inference results and image digests before describing cross-device delivery as validated.
