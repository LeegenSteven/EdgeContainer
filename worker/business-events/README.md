# Worker business events

This adapter transports application-produced events, independently from Edge Agent
heartbeats and Docker container logs. It does not capture raw audio/video, mount a
Docker socket, send model weights to Server, or execute model changes.

## Event contract and producer hooks

The business program writes newline-terminated UTF-8 JSON objects to its dedicated
outbox. Fields are `schemaVersion:1`, globally stable `eventId`, `sourceId`,
`taskId`, `type`, timezone-qualified `occurredAt`, `sessionId`, monotonically
increasing per-session `sequence`, and `text`. Optional fields are
`replyToEventId`, `provenance` (small JSON object), and `phase`.

```python
import os
from edgestack_events import EventWriter, VoiceSession

writer = EventWriter(os.environ['EVENT_OUTBOX'], os.environ['EVENT_SOURCE_ID'], os.environ['EVENT_TASK_ID'])
session = VoiceSession(writer)  # one per actual voice session
session.final_transcript(final_asr_text)  # skip partials/duplicate close-out finals
session.final_reply(completed_reply)     # only after final application reply
session.playback('synthesis_complete')   # synthesis/transport completion
session.playback('played', actual_client_ack=True)  # real client onended only
```

Event IDs remain identical through delivery retries. To repeat a failed append
using a previously constructed event, call `writer.append(event)` unchanged.
The producer allocates sequences durably before appending and fsyncs each line;
an interrupted write/allocation may leave an observable sequence gap.

Supported types: `voice.user_utterance`, `voice.assistant_reply`,
`voice.playback_state`, and `camera.observation`. Camera events need a separately
defined detector/program integration; this repository contains no real camera
collector. A demonstration camera event must use `provenance.kind="demo"` and
must not contain an image, raw video, or a media download URL.

## Deployment and authentication

Use [compose.business-events.yaml](../../deploy/worker/compose.business-events.yaml)
from the repository root. Provision the device, task and source in Server first,
then create a dedicated ingestion credential scoped to that exact triple, with
`ingest-events` and `ingest-status` capabilities only. Store the returned secret
in a protected file; set `INGEST_TOKEN_PATH` to its path. It is unrelated to
`EDGE_KEY`, an administrator session, and any future cloud credential.

Set `INGEST_BASE_URL`, `EVENT_DEVICE_ID`, `EVENT_SOURCE_ID`, `EVENT_TASK_ID`, and
`EVENT_OUTBOX_DIR`. Run `docker compose -f deploy/worker/compose.business-events.yaml
up -d`. Pin `BUSINESS_ADAPTER_IMAGE` to a tested immutable ARM64 image before
production deployment. The example uses Python 3.11 and no third-party packages.

The business application gets a read/write mount to the dedicated outbox only.
The adapter mounts that directory read-only, and writes its SQLite cursor and
pending queue to a separate persistent named volume. Do not mount the original
voice application's general data directory, logs, model directories or secrets
into this adapter. Copy/bind `adapter.py`, `edgestack_events.py` and any desired
voice integration helpers into the business program's import path.

The adapter POSTs batches to `/api/edgestack/ingest/events` and status to
`/api/edgestack/ingest/status`. Device binding is checked by Server against the
credential and registered source; the producer cannot choose a new endpoint or
task through an event payload. HTTP redirects are rejected to protect the token.
Use HTTPS where available. Token files are re-read for rotation.

### Trusting the Server HTTPS certificate

The default HTTPS mode validates the system CA chain and hostname. For a private
CA, set `INGEST_CA_FILE` to its PEM path **inside the container**, then uncomment
the explicit CA file mount in the Compose example and set `INGEST_CA_PATH` to
the trusted host PEM file. CA mode continues to require a matching DNS/IP SAN;
trusting a self-signed certificate does not make a missing LAN-IP SAN valid.

For a Server certificate without that IP SAN, set `INGEST_CERT_SHA256` to the
exact leaf certificate's SHA-256: 64 hexadecimal characters, without colons or
the `sha256:` prefix. Obtain the public certificate through the already trusted
Server SSH connection or another authenticated administrative channel. Compute
the digest over DER certificate bytes, for example:

```sh
openssl x509 -in trusted-server-cert.pem -outform DER | openssl dgst -sha256
```

Use the final 64-character digest as the environment value. Do not bootstrap
trust by accepting whichever certificate an unverified network connection offers.
No private certificate key is needed or copied. The generic Compose passes the
pin directly and needs no CA mount for pin-only mode.

Pin-only mode uses the exact leaf certificate as its trust anchor, allowing the
known self-signed certificate and LAN-IP name mismatch. The TLS connection must
match that pin before any HTTP Authorization header or event body is sent.
With both `INGEST_CA_FILE` and `INGEST_CERT_SHA256`, CA-chain verification remains
required and the exact pin supplies endpoint identity in place of hostname
matching. Wrong pins, invalid CA chains and redirects are rejected; there is no
general TLS-verification bypass flag. TLS options with an `http://` URL are
configuration errors. Coordinate a trusted pin update when renewing the Server
certificate; delivery stays queued while an old pin rejects the replacement.

## Recovery, limits and coverage

Cursor advance and event enqueue share one SQLite transaction. A lost HTTP
response retains the same payload and event ID; Server's idempotent ingestion
returns accepted/duplicate counts before local acknowledgement removes rows.
Malformed receipts never acknowledge a batch. Failed deliveries back off between
2 and 301 seconds, preserving the retry schedule through adapter restarts.

The default queue is limited to 10,000 events / 32 MiB, request batches to 100
events / approximately 512 KiB, lines to 128 KiB and text to 16,384 characters.
Full queues stop consuming the producer files; the cursor resumes after delivery.
There is no silent queue eviction. SQLite can retain allocated disk pages after
deletion. Keep the state volume private because queued final text is business data.

The producer rotates at 8 MiB and retains eight files by default (roughly 64 MiB).
Increase that retention to cover the expected offline interval and application
event rate. Continued production beyond retained disk space can lose unread
history. The adapter reports observable gaps for sequence jumps, invalid lines,
copy/truncate/replacement and unread removed files. It cannot prove events never
written or files removed before first observation. Consequently status always
reports `coverage.complete=false`, with `basis=observed_producer_jsonl_only`;
neither a healthy adapter heartbeat nor `gapCount=0` proves complete conversation
history. Status reports cursor offsets, queue depth/bytes, last delivery/event,
and up to 32 recent gap descriptions, without raw conversation text.

## OpenVoiceStream integration

The optional RK3588 wrapper targets the pinned upstream image `sensecraft-missionpack.seeed.cn/solution/seeed-local-voice@sha256:a0d3a02a114110ede6deb9cc6ed218f3a02322ef1a5746cc75cf57e65e91cac5`.
The application directory is `/opt/speech`; original entrypoint is
`python3 -m uvicorn server.main:app --host 0.0.0.0 --port 8621`.

Source hooks in that pinned image:

| Hook | Source and meaning |
| --- | --- |
| ASR stream final | `server/main.py` `/asr/stream`, final sends around lines 2635–2739; `is_final=true` only |
| V2V final | `server/main.py` `/v2v/stream`, `asr_final` sends around 4078–4109; omit empty / `duplicate_of_streamed` close-outs |
| Assistant content | Default path gets client `text` chunks then `tts_flush`; reconstruct only as `phase=tts_requested`, or record explicit application-final reply |
| TTS state | `tts_started` around 4223 and `tts_done` around 4343 mark synthesis/transport, not physical playback |
| Speaker playback | Must originate from client playback queue `onended` / interruption acknowledgement |

`voice_asgi.py` wraps these ASGI protocol messages without mutating upstream
source. For an isolated test clone, mount this directory `/events:ro`, a new
dedicated outbox `/business-outbox:rw`, and use:

```text
PYTHONPATH=/events:/opt/speech
OVS_PROFILE=rk3588-default
RK_ARTIFACT_AUTO_DOWNLOAD=0
ASR_DECODER_TYPE=rkllm
ASR_DECODER_QUANT=w8a8
EVENT_OUTBOX=/business-outbox
EVENT_SOURCE_ID=<registered-source>
EVENT_TASK_ID=<registered-task>
VOICE_UPSTREAM_TOKEN_FILE=/run/secrets/voice_upstream_token
RKNN_DEVICE_TREE_FILE=/run/edgestack-device-tree/compatible
python3 -m uvicorn voice_asgi:app --host 0.0.0.0 --port 8621
```

Expose the integration service on a dedicated internal port, reuse compatible
model mounts read-only, and use separate application-data and event-outbox
volumes. Keep the source adapter independent so pending events survive speech
process restarts. Validate new integrations separately from existing workloads.

The pinned upstream artifact manifest lists both FP16 and W8A8 decoder files.
A deployment that mounts only W8A8 should disable automatic artifact downloads. Its downloader ensures every manifest
entry regardless of `ASR_DECODER_QUANT`; therefore set
`RK_ARTIFACT_AUTO_DOWNLOAD=0` for this preinstalled, read-only deployment.
Selecting W8A8 alone does not prevent an attempted FP16 download. Actual loaded
W8A8/encoder files are still hashed and confirmed by the runtime observer.
The independent `voice_asgi.py` wrapper sets this default to `0` before importing
the upstream application. Keep an explicit `0` in deployment configuration to
use preinstalled read-only models; the upstream application source is unchanged.

The RKNN SDK's fixed `/proc/device-tree/compatible` path resolves through
`/sys/firmware`, which Docker masks by default. A bind to that old path, even of
the full directory, can remain hidden by the parent mask. For this wrapper,
replace that old bind with the following read-only directory bind while keeping
the existing explicit DRM/DMA device mappings:

```yaml
volumes:
  - /sys/firmware/devicetree/base:/run/edgestack-device-tree:ro
```

`rk_device_tree.py` runs only from the independent wrapper. Before installing
compatibility access, it verifies `/proc/self/mountinfo` identifies the alternate
file as a read-only physical `sysfs` device-tree mount and reads its actual
NUL-delimited board identifiers, requiring exact `rockchip,rk3588`. Missing,
copied, writable, unrelated or nonmatching chip sources fail closed. It then
rebinds only the vendor `rknnlite.api.rknn_runtime` module's own filesystem
references for that one legacy filename; the application-wide `os.path` and
`builtins.open` remain unchanged. It also sets the vendor module's own
`DEVICE_COMPATIBLE_NODE` filename to the validated physical path: Cython's cached
builtin file reader bypasses a module `open` alias. This lets the original
`_is_npu_self` parse the real board data and identify RK3588; no target string or
NPU-kernel-node value is supplied or invented. The SDK's original container checks and native
initialization are retained, including DRM-device and runtime-library checks.
Logs identify the actual source path and its public hardware-file hash.

Keep Docker's default masked/read-only paths in place. The adapter changes only
the vendor module's compatible-file lookup; it does not need `privileged`,
system-path unconfinement, or a one-off Engine API configuration. Compose
recreation should retain the explicit read-only bind. Device-tree compatibility
is separate from successful native model initialization and inference: verify
those through the runtime observation contract below.

`asr_compat.py` repairs a typed-result mismatch in the pinned migrated
`voxedge.backends.rk.asr._RKASRStreamAdapter`: its original short-audio final
passes the inner `(text, language)` tuple into a string cleaner, producing a
literal tuple representation. The wrapper consumes the inner final exactly once,
unpacks typed results before the original text cleaner, and returns the proper
`(text, language)` pair. The upstream long-audio segmentation path is preserved.
It never evaluates or reparses transcript strings. Language is retained in the
wire payload and event `provenance.asrLanguage`; startup logs identify the typed
compatibility hook. Existing historical records are not rewritten.

Deployment requires `VOICE_UPSTREAM_TOKEN_FILE`, a read-only file containing a
separate Server-to-Worker shared secret. Configure the Server task's
`VoiceTokenFile` to its corresponding protected secret file. The authenticated
Server proxy strips browser authorization before injecting this Bearer token.
The middleware validates it with a constant-time comparison on every HTTP and
WebSocket path, including `/health` and `/model-info`, before forwarding or
recording any event. A configured file that is missing, empty or unreadable fails
closed. The file is re-read for rotation. This secret is independent from source
ingestion, Edge Agent and cloud credentials. Leaving this variable unset disables
the extra check only for isolated development; never expose such a service port.
An authenticated Docker health check must read this file inside its process and
add the Authorization header without printing the secret. The digital harness
accepts `--voice-token-file` for the same purpose.

`voice_test.html` is a self-contained, explicitly labeled microphone/echo test.
Serve it through the authenticated task endpoint
`/api/edgestack/tasks/{taskId}/voice-test`. It uses only the same-origin WebSocket
`/api/edgestack/tasks/{taskId}/voice/v2v/stream`, with existing session cookies.
No browser token query, direct mixed-content Worker request or embedded admin
credential is needed. Permission is requested on click, PCM is resampled to
16 kHz signed little-endian 16-bit mono, and all microphone tracks stop at finish
or cancel. Echo replies use `provenance.kind=integration_test` and
`mode=microphone_echo`. Browser playback ends only after every scheduled source's
`onended`; the resulting `played` acknowledgement returns the actual persisted
event ID. It proves browser queue completion, not that a human heard a speaker.
On load, the page checks the authenticated same-origin `/voice/health` endpoint
and enables recording only when ASR and TTS both report ready. It refreshes while
idle and rechecks on click before requesting microphone permission.
`voice_client_hooks.js` provides the same explicit final/playback hooks for a
future business client. Existing voice auth remains enforced by the upstream
websocket before it reads these hook messages.

`voice_roundtrip.py` is the synthetic digital harness. Run it inside the speech
image (which supplies `websockets`), pointing at the hooked clone. It generates
input speech via `/tts` in RAM, exercises real ASR and controlled-reply TTS, and
does not write a WAV or send a false playback acknowledgement. Its result labels
microphone, dialogue-model and speaker verification false.

The documented packages use Qwen3 ASR with a W8A8 decoder and RK3588 encoder
window artifacts in `/opt/asr/models/encoder/rk3588/`. Provision compatible model
files separately; model weights are not included in this repository.
Window variants are packages of the same family, not two distinct ASR models.
The default profile does not wire a server-side dialogue model; LLM work is
client-side unless the optional server-loop mode is explicitly configured.

`ASR_ENCODER_SIZES=2` and `ASR_ENCODER_SIZES=4` select the installed 2s and 4s
packages respectively. The runtime variable is read by
`/opt/speech/third_party/rkvoice-stream/rkvoice_stream/backends/asr/qwen3_rk.py`
(lines 203–225 in the pinned image); true streaming defaults to `[2,4]`.
A 2s-only package clips longer input, so use a short generated phrase when
comparing these two window variants. The public 2s/4s merged-encoder artifact hashes for this runtime are:

| Window | Merged encoder SHA-256 |
| --- | --- |
| 2s | `25e0d95ebe8642b06f8657516bc114f9b01aca7d05a440389c2e7d22a1de258e` |
| 4s | `a39b7f803513cffe944c3006c4018c5aae775ac2c67a7941627e07972147cae8` |

`runtime_observer.py` instruments the original native functions, preserving
their arguments/results. It captures Worker-local paths and SHA-256 after a
successful RKNN `load_rknn`, records successful `init_runtime`, and checks those
same native instances remain in the current ASR encoder's `_models` mapping.
The decoder receipt follows successful `RKLLMDecoder` initialization. An encoder
`encode` receipt records the window returned by real inference. Model bytes never
leave Worker. Release/unload removes the receipt. The optional instrumentation
is installed before the upstream ASGI lifespan loads runtimes; if dependencies,
load or hashing fail, `/model-info` reports unconfirmed identity.

For a trusted replacement receipt, require `/model-info.asr.confirmed=true`,
`modelHash` and `lastInferenceModelHash` matching the registered package hash,
`inputWindowSeconds` matching 2 or 4, and `successfulEncoderInferences>0`.
`modelHash` is populated only when a single merged encoder window is loaded.
Desired labels/environment are never used as model-load evidence. Hashing the
large local artifacts adds startup disk I/O; it does not execute a model or send
weights to Server. The independent source adapter never initiates model changes.

### Continuous model receipt bridge

`model_receipt.py` polls authenticated Worker-loopback `/model-info` every 20
seconds and forwards only verified loaded/inferred model receipts through the
same strict HTTPS transport. Provision a dedicated observer source bound to the same task/device, with a
separate credential
with exactly `ingest-status` and `observe-model` capabilities. Keeping it separate
also prevents model-health status from replacing event-adapter backpressure.

```text
EVENT_SOURCE_ID=<registered-observer-source>
EVENT_TASK_ID=<same-voice-task>
EVENT_DEVICE_ID=<worker-device>
VOICE_UPSTREAM_TOKEN_FILE=/run/secrets/voice_upstream_token
MODEL_OBSERVER_TOKEN_FILE=/run/secrets/model_observer_token
MODEL_INFO_URL=http://127.0.0.1:18621/model-info
INGEST_BASE_URL=https://<server>:9443
INGEST_CERT_SHA256=<trusted-leaf-certificate-64-hex-digest>
MODEL_OBSERVER_SLOT=asr-encoder
MODEL_OBSERVER_EXPECTED_MODEL_ID=qwen3-asr
python3 /app/model_receipt.py
```

Run in a network namespace that can reach that loopback port (for example the
Worker host network, or the speech container with its internal port 8621).
`MODEL_INFO_URL` accepts loopback HTTP `/model-info` only; proxying and redirects
are disabled so the upstream secret cannot be forwarded elsewhere. Token files
are re-read for rotation. `--once` performs one check and exits nonzero unless
Server accepted the receipt. Normal operation logs only status changes.

The bridge requires confirmed, ready native runtimes, at least one successful
encoder inference, identical loaded/inferred artifact hashes and window sizes,
and an authenticated actual `modelFamily`. The observer supplies that family
only for the supported actual Qwen3 backend and engine Python types. The
optional `MODEL_OBSERVER_EXPECTED_MODEL_ID` is a filter; it cannot substitute a
desired identity. No expected package/hash environment variable constructs a
receipt. Output hashes have Server's `sha256:` prefix, while `observedAt` is the
actual `lastInferenceAt` converted to UTC, never the poll time. This lets Server
reject receipts predating a new deployment until new inference occurs.

Unconfirmed, unavailable or inconsistent runtime evidence produces no loaded
receipt. Server delivery failures are retried from fresh local evidence at the
next poll. The bridge does not dispatch model changes and does not modify event
queue checkpoints or coverage fields.

## Verification

From the repository root, run the 40 Worker tests with Python 3.11 and OpenSSL
available. The HTTPS cases bind only a temporary localhost test server; they do
not contact a deployed Server or Worker.

```sh
PYTHONDONTWRITEBYTECODE=1 python3 -m unittest discover -s worker/business-events/tests -v
```

Keep generated caches and test output outside the source tree. To check Python
syntax without importing runtime dependencies or writing bytecode:

```sh
python3 - <<'CHECK'
from pathlib import Path
for path in sorted(Path('worker/business-events').rglob('*.py')):
    compile(path.read_bytes(), str(path), 'exec')
print('Worker Python syntax: OK')
CHECK
```
The suite covers durable retries after response loss and restart, partial lines,
rename rotation, copy/truncate recovery, queue backpressure, malformed receipts,
scope checks, gaps/coverage and ASGI final/reply/playback semantics. A temporary
localhost TLS server with generated test certificates verifies CA/hostname and
leaf-pin modes, rejection before credentials/body are sent, and redirect denial
(`openssl` is needed only to generate these test fixtures). Synthetic and
microphone tests require a deployed speech clone and are separate from these
offline protocol tests.
