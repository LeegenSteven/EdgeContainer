# Model delivery execution worker

This Python 3.9+ service consumes claimed `prepare_model` jobs from EdgeContainer. It runs a configured AgentDP deployment, reads back its original job, builds an OCI image with BuildKit, publishes it to the LAN registry, validates the deployed image through Server's isolated test task, and exports an Ed25519-signed offline archive. Server owns publication policy and deployment state.

The first adapters accept prepared RKNN model files for an ASR encoder or YOLO tensor runtime. The corresponding finite AgentDP deployment must already contain that exact prepared release; the worker verifies its actual model ID and hash. An approved recipe pins the runtime base image, build context, actual model identity and test workload. An unregistered prepared release fails its native identity check. A request for an unsupported source format fails with `source_format_unsupported`. Model conversion requires a separately implemented recipe adapter.

## Run

Install Python, BuildKit's `buildctl`, `skopeo`, and OpenSSL 3 with Ed25519 `pkeyutl -rawin` support on the execution host. Configure the builder for the recipe's actual platform. RKNN native and final image inference run on the configured isolated RK3588 task.

Copy [worker.example.json](../../deploy/model-delivery/worker.example.json) to a private administrator-owned configuration. Populate the service addresses, token and CA files, device mapping, recipe identities and signing keys. Enable each recipe after supplying its actual digests and probes.

```sh
python3 -B worker/model-delivery/delivery_worker.py --config /etc/edgestack-delivery/worker.json run
python3 -B worker/model-delivery/delivery_worker.py --config /etc/edgestack-delivery/worker.json run --once
```

The [systemd unit](../../deploy/model-delivery/model-delivery.service) uses the same entry point. Give the service account access to its BuildKit socket and configured read-only inputs. Persistent state, work files and published bundles use the directories in the configuration. Mount `bundleDir` at the same absolute path in Server so Server can serve the returned `file:` bundle reference through its authenticated export endpoint.

Each enabled local recipe binds `serverRecipeDigest` to the frozen Server recipe, including its version, runtime profile, pinned image, source policy and validation gates. `contextDigest` hashes the administrator-owned Dockerfile context independently. `runtimeBundleDigest` approves the installed runtime adapter and its copied ASR instrumentation modules. Obtain these values from the exact sources being installed; a source change requires renewed approval.

```sh
cd worker/model-delivery
python3 -B -c 'from artifacts import recipe_context_digest, runtime_bundle_digest; print(recipe_context_digest("../../deploy/model-delivery/recipes/yolo")); print(runtime_bundle_digest())'
```

The supplied Dockerfiles copy the prepared model, candidate identity, image-local runtime service and unchanged ASR instrumentation modules. Their entrypoint starts that service. The approved base must contain `/opt/edgestack/runtime-config.json`, its measured runtime profile, frozen workload, and actual RKNN/ASR dependencies. Existing speech images without these assets require registration of a new digest-pinned approved base image. They cannot be made valid by a healthy-process flag. Model environment values come from the frozen Server recipe.

## Internal contract

The worker uses a private Bearer credential from `server.token_file`. JSON clients reject redirects and verify configured TLS CAs. Separate AgentDP credentials stay on the execution host. The adapter exposes these fixed paths.

| Path below `/api/edgestack/internal/delivery` | Method | Purpose |
| --- | --- | --- |
| `/jobs/claim` | POST | `{workerId,recipeIds}` obtains a job and its claim |
| `/jobs/{id}/heartbeat` | POST | Renews the exact `claimToken`, `claimEpoch` and `controlEpoch` |
| `/jobs/{id}/validation/start` | POST | Sends the frozen image's draft model package for an isolated Server validation |
| `/jobs/{id}/validation/status` | POST | Reads back the original validation operation after interruption |
| `/jobs/{id}/validation/complete` | POST | Sends measured probe results for Server's independent runtime and scope verification |
| `/jobs/{id}/result` | POST | Returns one idempotent `resultId`, `artifact_ready` or `failed`, and typed evidence |

The claim response contains `job`, `claimToken`, `claimEpoch`, `controlEpoch`, `leaseExpiresAt` and `heartbeatSeconds`. Job inputs bind the recipe, prepared source SHA-256, model ID, device and validation task. Every digest uses the `sha256:` prefix. A stopped heartbeat, expired lease or changed epoch prevents further external execution, and terminates a running local build/probe subprocess group.

AgentDP paths are limited to sessions, device plans, confirmation, original jobs and `verify`. The worker records session, plan and job IDs in SQLite before advancing. AgentDP confirmations remain scoped to the original plan and session. An uncertain plan creation is recovered by its delivery-job marker, or fails for reconciliation. An uncertain confirmation is recovered by reading the original plan's job ID. The worker never creates a replacement inference job to discover an unknown result.

Before a new native test, the worker calls `/api/edgestack/internal/execution-leases/stop-validation-task` with `{jobId,claim}`. Server may stop only that job's isolated `TestTask` when the administrator's `AllowStopValidationTask` policy permits it. The default policy requires explicit administrator handoff. The worker waits with claim heartbeats on a 409 and preserves the original native plan/job; it never stops another business workload. After native execution, AgentDP must prove `lease.release_info.remote_stop_confirmed: true`. Server then acquires and maintains its own resident lease while the final validation container serves. A healthy container is never treated as stopped when releasing that lease.

The SQLite journal uses full synchronization, and the CLI holds an exclusive process lock. Successful results are stored before transmission. A lost result acknowledgement retries the same `resultId` and evidence. Build interruption requires a valid recovered OCI output or explicit reconciliation. Registry publication reads back the immutable manifest digest. Missing builders, missing probes, failed verification and ambiguous native outcomes produce errors.

A new claim epoch after an explicit administrator retry clears the previous failed result and preserves completed stages and execution history. A failed native run repeats only after its original job is terminal and its stop proof is confirmed. Unknown outcomes remain attached to their original IDs. A completed Server validation report is resubmitted verbatim; a failed Server validation operation repeats only after Server clears its operation binding. Heartbeats report `waiting_for_device`, `native_validation`, `building`, `image_validation`, and `policy_evaluation` as progress while job status remains `claimed`.

## Native identity and final image validation

`agentdpDeployment` names a preconfigured finite AgentDP run for the prepared source. `nativeModelIdPath` and `nativeModelHashPath` locate the actual identity in its `result.raw`. The source ID and exact bytes must match the deployment. Its `verify` verdict must be `succeeded`, its raw record must explicitly say `simulated: false`, and the shared board lease must have confirmed remote stop evidence.

`containerProbeArgv` is an administrator-owned argument array executed without a shell. It supports `{deviceId}`, `{taskId}`, `{image}`, `{modelHash}`, `{runtimeProfileDigest}` and `{nonce}` substitutions. The supplied [container_probe.py](container_probe.py) calls one fixed inference-validation endpoint using a frozen workload. A runtime adapter may instead supply a fixed executable that performs the equivalent real test.

The inference adapter must accept the bound nonce idempotently and return its actual runtime identities and measurements.

```json
{
  "deviceId": "rk3588-device",
  "taskId": "rk3588-isolated-test",
  "image": "registry.example.test/edgestack/models@sha256:<manifest-hash>",
  "modelHash": "sha256:<model-hash>",
  "runtimeProfileDigest": "sha256:<profile-hash>",
  "nonce": "<request-nonce>",
  "simulated": false,
  "healthy": true,
  "latencyMs": 32.0,
  "memoryBytes": 536870912,
  "errorRate": 0.0,
  "quality": 0.98,
  "observedAt": "2026-11-17T08:00:00Z"
}
```

These JSON values illustrate the contract. The live probe must supply measured values, match the exact bound image and model, and pass every recipe gate. After that probe, Server independently verifies the registered test task and establishes its trusted scope before accepting the final artifact. The worker cannot mark a model published or establish a validated scope itself.

## Image-local inference service

[runtime_service.py](runtime_service.py) listens on port 18622 for `/health` and authenticated `/edgestack/validation`. It performs the frozen workload on startup, measures the installed runtime profile from actual file bytes and architecture, and obtains the model hash from the loaded model's actual file. Startup, inference or identity failures keep health unavailable. A fixed workload runs again every 60 seconds. Model/library changes, a dead configured business process or inference evidence older than 120 seconds invalidate health.

ASR starts the real approved `server.main` app through `voice_asgi` with its RKNN instrumentation. It sends frozen mono PCM16 16 kHz audio through the real ASR WebSocket and checks the resulting transcript against its reference. Runtime receipts require the same initialized RKNN encoder instance and actual successful encoder inference. Public ASR uses approved `asrHost: "0.0.0.0"`, `asrPort: 8621` and the mounted `VOICE_UPSTREAM_TOKEN_FILE`; without that credential the adapter generates a private local credential. Self-test transcripts are marked as integration tests. The base must select exactly the candidate encoder path and supported single encoder window; automatic model download stays disabled.

YOLO loads and initializes the actual RKNN file with `rknnlite.api.RKNNLite`, runs frozen `.npy` input tensors and compares the returned tensors to hash-checked `.npz` references named `output_0`, `output_1`, etc. This verifies tensor inference. Camera acquisition and end-to-end detection quality require their own approved business pipeline. An optional `businessArgv` starts a fixed base-image business executable without a shell; the executable and configuration must be profile-bound. Health also requires that process to remain alive. The tensor-only example does not claim camera capture.

ASR quality is one minus character error rate after whitespace normalization, clamped to zero; YOLO quality is the fraction of output tensor elements within the frozen numeric tolerances. Latency is mean wall time for the full adapter call per sample. `memoryBytes` is the actual inference process's Linux peak RSS, not total board memory or NPU memory. Set policy gates with these measurement definitions.

The profile binds the approved runtime configuration, workload manifest, inference implementation and native libraries. Workload manifests bind each input/reference checksum. Generate the profile on the actual base-runtime architecture with [runtime_profile.py](runtime_profile.py), including all inference libraries and configured business code. Put its printed digest in the Server recipe. [Runtime examples](../../deploy/model-delivery/runtime/) contain schema placeholders; actual audio, tensors, expected outputs and installed library paths must be supplied by the administrator.

The validation task mounts an independent `EDGESTACK_VALIDATION_TOKEN_FILE`; the worker probe uses its matching credential. The runtime requires the exact `{"workloadDigest":"sha256:..."}` input, bound task/device/hash/profile and an idempotent nonce. An unconfigured validation secret returns 503; the observer credential cannot authorize this endpoint. The echoed image is a request binding; Server independently inspects the registered container's immutable image and desired model environment.

New task deployments receive these observer variables and a dedicated source-scoped secret from Server:

```text
EDGESTACK_SERVER_URL
EDGESTACK_MODEL_OBSERVER_SOURCE_ID
EDGESTACK_MODEL_OBSERVER_TOKEN_FILE
EDGESTACK_MODEL_OBSERVER_TASK_ID
EDGESTACK_MODEL_OBSERVER_DEVICE_ID
EDGESTACK_MODEL_OBSERVER_SLOT
```

`EDGESTACK_SERVER_CA_FILE` can supply private CA trust. Every 20 seconds the runtime submits a current loaded-model observation to `/api/edgestack/ingest/status`. It submits nothing when actual inference evidence is unavailable. First-time ASR business source, reverse proxy, ports and transport-secret mounts still come from an administrator-approved deployment template.

## Offline archives

The archive contains `manifest.json`, its 64-byte `manifest.ed25519` signature, and `image.oci.tar`. The signed manifest binds image digest, platform, model SHA-256, runtime profile, recipe, release identity, validation report and the full image-archive checksum. Verification uses a separately provisioned trusted public key. It rejects unsafe archive paths, duplicate entries, image checksum changes, missing OCI blobs and platform mismatches.

```sh
python3 -B worker/model-delivery/delivery_worker.py --config /etc/edgestack-delivery/worker.json verify-bundle /srv/import/model.tar
python3 -B worker/model-delivery/delivery_worker.py --config /etc/edgestack-delivery/worker.json import-bundle /srv/import/model.tar
```

Import verifies the signature and every OCI blob, then copies the image to the configured LAN registry and reads back the digest. Model catalogue registration and target deployment continue through Server's normal authorization and validation workflows.

## Checks

```sh
PYTHONDONTWRITEBYTECODE=1 python3 -B -m unittest discover -s worker/model-delivery/tests -v
```

The suite exercises lost confirmation/result recovery, immutable journal inputs, claim fencing and progress, explicit retry boundaries, native stop proof, original validation operations, source identity, OCI integrity, fixed builder arguments, actual subprocess errors, runtime evidence and health, HTTP authentication/nonce behavior, and offline archive tampering. Its hardware and builder fixtures perform no device deployment. The HTTP test opens only a temporary loopback listener. Production acceptance additionally requires the configured builder, registry, signing backend, isolated ASR/YOLO inference services and Server runtime verifier.
