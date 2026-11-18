# Model delivery host configuration

The execution service is [worker/model-delivery](../../worker/model-delivery/README.md). These files describe a separate execution host with narrow Server and AgentDP adapters, a BuildKit builder and a LAN OCI registry.

1. Install the worker sources at `/opt/edgestack/worker/model-delivery`, the unchanged instrumentation dependencies at `/opt/edgestack/worker/business-events`, and the approved recipe contexts at `/opt/edgestack/deploy/model-delivery/recipes`.
2. Provision Server's private worker token, AgentDP's token, TLS CA files, registry authentication and an Ed25519 signing key under `/etc/edgestack-delivery`. Restrict token and private-key files to the service account.
3. Supply frozen ASR/YOLO runtimes as digest-pinned approved base images. Include the actual runtime dependencies, `/opt/edgestack/runtime-config.json`, runtime profile, and hash-checked test assets. Register the corresponding recipes in Server and copy the resulting recipe digests into `worker.json`. Hash the installed contexts with `recipe_context_digest` and installed execution modules with `runtime_bundle_digest`. The worker copies the approved runtime bundle and per-job candidate identity into each image in addition to the model.
4. Map each Server device ID and isolated validation task to its AgentDP device alias. The configured AgentDP deployment must verify the exact prepared model requested by that recipe.
5. Install each actual inference probe. [probe.example.json](probe.example.json) shows the fixed HTTP adapter configuration. The runtime listens on HTTP port 18622; this HTTPS example assumes an administrator-configured TLS reverse proxy. Its frozen probe inputs are exactly `{"workloadDigest":"sha256:<workload-manifest-file-hash>"}`. Mount the independent validation secret in the isolated task as `EDGESTACK_VALIDATION_TOKEN_FILE` and give the probe its matching credential. Scope this route to the execution host. Observer credentials cannot authorize validation.
6. Mount the artifact directory into Server at the same absolute path. Provision Server's export handler with the trusted signing public key and artifact root, and configure matching registry trust on the builder and target workers.
7. Copy [model-delivery.service](model-delivery.service) into the host's service manager and start the configured worker after isolated end-to-end validation.

Generate a signing key using OpenSSL 3 on the deployment host.

```sh
umask 077
openssl genpkey -algorithm ED25519 -out /etc/edgestack-delivery/offline-ed25519.key
openssl pkey -in /etc/edgestack-delivery/offline-ed25519.key -pubout -out /etc/edgestack-delivery/offline-ed25519.pub
```

The example recipes ship disabled with placeholder approval digests. Enable them with the installed runtime and measured validation mappings. Runtime build, registry copy, final scope verification and signature errors are surfaced as failed jobs. Local journals keep external execution IDs for reconciliation after a restart.

## Approved runtime preparation

[runtime/](runtime/) contains ASR and YOLO configuration/workload schemas. These examples contain no trained models or test assets. Replace every placeholder with an actual frozen input, verified reference and file hash. Copy the selected runtime configuration into the base image at `/opt/edgestack/runtime-config.json`.

For ASR, the base must supply its real `server.main`, `rkvoice_stream`, NumPy, WebSockets, Uvicorn, RKNN/RKLLM libraries and other original speech dependencies. Its configured encoder must read `/opt/edgestack/models/encoder.rknn`, select one supported encoder window and report the same model family as the prepared release. The approved example exposes the existing ASR business service on `0.0.0.0:8621`, authenticating with mounted `VOICE_UPSTREAM_TOKEN_FILE`. Port 18622 serves separate health/validation. First-time business sources, ports, proxy and secret mounts need an approved administrator template. Existing ASR images without the runtime configuration, profile and assets require a new approved base registration.

For YOLO, the base must supply a working `rknnlite.api.RKNNLite`, NumPy and compatible native runtime. Mount the board's real `/sys/firmware/devicetree/base` read-only at `/run/edgestack-device-tree`, and grant the image's runtime user the numeric groups required by its exact NPU/DMA device nodes. The device-tree adapter rejects copied files or writable/non-sysfs mounts; it retains the SDK's real SoC and device checks. Frozen inputs use `.npy`; references use `.npz` arrays named `output_0`, `output_1`, etc. The example is a tensor inference runtime. To run a camera business pipeline as well, add a fixed absolute `businessArgv` to the approved configuration, include its executable and implementation in the measured profile, and configure the template's camera/device mounts and service route. No shell or user-provided command is accepted. Health also checks that business process remains alive; tensor tests do not establish camera acquisition or detection mAP.

Generate the runtime profile on the actual base-image architecture. The file list must include inference implementation files, native inference libraries and optional business implementation files. The helper always includes the runtime configuration and workload manifest. For example, after replacing the library/module paths with their real installed locations:

```sh
python3 -B worker/model-delivery/runtime_profile.py \
  --output /opt/edgestack/runtime-profile.json \
  --runtime-config /opt/edgestack/runtime-config.json \
  --workload /opt/edgestack/workloads/yolo/manifest.json \
  --backend rknn-yolo \
  --file rknn-native=/usr/lib/librknnrt.so \
  --file rknn-python=/opt/venv/lib/python3.11/site-packages/rknnlite/api/rknn_lite.py
```

Freeze the printed `runtimeProfileDigest` into Server's recipe before approving the base image digest. The digest comes from installed files and the actual architecture. Workload manifests in turn bind the audio/tensor/reference files. Hash the workload manifest with `file_digest` to populate the fixed probe inputs.

Before approving an image, run an image-only CPU preflight as UID/GID `1000:1000`, without NPU/DMA devices or host runtime/model/workload mounts. Verify directory traversal, readability of configuration, profile, workload and model files, and their frozen hashes without initializing inference. The model directory must remain traversable (`0555`); model files remain read-only (`0444`).

Runtime startup must complete real inference before Docker health passes. It repeats the frozen workload every 60 seconds and sends source-scoped model receipts every 20 seconds. Server injects `EDGESTACK_SERVER_URL`, optional `EDGESTACK_SERVER_CA_FILE`, and `EDGESTACK_MODEL_OBSERVER_SOURCE_ID`, `TOKEN_FILE`, `TASK_ID`, `DEVICE_ID`, `SLOT` variables. Register the dedicated observer source and credential separately from business events and validation credentials. The receipt's model hash is actual loaded/inferred evidence; a report's image string is a request binding that Server independently verifies by Docker inspection.

The observer bearer value is delivered only as a runtime container file: a standard Compose `configs.<name>.content` entry and long service config reference target `/run/secrets/edgestack-model-observer`, UID/GID `0:0`, mode `0444`, readable by runtime UID `1000`. Docker Compose copies this inline config through its Docker file-copy API, so Server, Agent and execution host need no shared credential-file path. The environment contains the path, not the bearer value, and the image does not contain this credential. The pinned Compose schema rejects `secrets.*.content`. Administrator templates must have a writable root filesystem for inline config injection; `read_only: true`, unresolved root-filesystem settings and conflicting config/secret targets fail before provisioning.

Native tests and resident validation containers share board resource groups. Default `AllowStopValidationTask=false` requires an administrator to stop and prove the isolated validation task has stopped before handing the board to native validation. With that policy enabled, Server's claim-scoped stop endpoint may perform this handoff for the job's isolated test task only. A failed or unknown stop keeps the resource unavailable. AgentDP must prove its finite native process stopped before Server acquires a resident container lease. Server maintains that lease while the validation service runs, and releases it only after an actual confirmed stop.
