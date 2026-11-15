"""Optional OpenVoiceStream ASGI entrypoint: uvicorn voice_asgi:app.

Mount this directory at /events and use PYTHONPATH=/events:/opt/speech. No
upstream source mutation, model access or stdout-log inference is involved.
"""
from __future__ import annotations

import json
import hmac
import os
from pathlib import Path
import sys
import uuid

from edgestack_events import EventWriter, VoiceSession


class VoiceEventMiddleware:
    def __init__(self, upstream, writer, runtime_observer=None, token_file=None):
        self.upstream, self.writer, self.runtime_observer = upstream, writer, runtime_observer
        self.token_file = Path(token_file) if token_file else None

    async def authorize(self, scope, send):
        """Independent Server-to-Worker credential; check before every route."""
        if self.token_file is None or scope["type"] not in {"http", "websocket"}:
            return True
        unavailable = False
        try:
            with self.token_file.open("rb") as stream:
                expected = stream.read(4097).strip()
            if not expected or len(expected) > 4096 or b"\n" in expected or b"\r" in expected:
                raise ValueError("invalid_voice_upstream_credential")
        except (OSError, ValueError):
            unavailable = True
            expected = b""
        headers = [value for name, value in scope.get("headers", []) if name.lower() == b"authorization"]
        supplied = b""
        if len(headers) == 1:
            scheme, separator, value = headers[0].partition(b" ")
            if separator and scheme.lower() == b"bearer":
                supplied = value
        matches = hmac.compare_digest(supplied, expected)
        if not unavailable and bool(expected) and matches:
            return True
        if scope["type"] == "websocket":
            await send({"type": "websocket.close", "code": 1013 if unavailable else 4401})
        else:
            payload = b'{"error":"voice_service_unavailable"}' if unavailable else b'{"error":"unauthorized"}'
            await send({"type": "http.response.start", "status": 503 if unavailable else 401,
                        "headers": [(b"content-type", b"application/json"), (b"cache-control", b"no-store"), (b"www-authenticate", b"Bearer")]})
            await send({"type": "http.response.body", "body": payload})
        return False

    async def __call__(self, scope, receive, send):
        if not await self.authorize(scope, send):
            return
        if scope["type"] == "http" and scope.get("path") == "/model-info":
            info = {"schemaVersion": 1, "asr": {"confirmed": False, "loaded": None, "reason": "runtime_identity_unconfirmed"},
                    "dialogue": {"confirmed": False, "loaded": None, "reason": "client_side_or_unconfigured"}}
            if self.runtime_observer:
                try:
                    info["asr"] = self.runtime_observer()
                except Exception:
                    pass
            payload = json.dumps(info).encode()
            await send({"type": "http.response.start", "status": 200, "headers": [(b"content-type", b"application/json"), (b"cache-control", b"no-store")]})
            await send({"type": "http.response.body", "body": payload})
            return
        if scope["type"] != "websocket" or scope.get("path") not in {"/asr/stream", "/v2v/stream"}:
            return await self.upstream(scope, receive, send)
        session = VoiceSession(self.writer)
        pending_text = []
        pending_chars = 0
        explicit_final = False

        def record(callback, *args, **kwargs):
            try:
                return callback(*args, **kwargs)
            except Exception:
                # Keep voice processing available; sequence reservation leaves a
                # detectable gap for most write failures. Never log text/tokens.
                sys.stderr.write("edgestack_business_event_write_failed\n")
                return None

        async def wrapped_receive():
            nonlocal pending_chars, explicit_final
            while True:
                message = await receive()
                if message.get("type") != "websocket.receive" or not message.get("text"):
                    return message
                try:
                    payload = json.loads(message["text"])
                except (ValueError, TypeError):
                    return message
                if not isinstance(payload, dict):
                    return message
                kind = payload.get("type")
                if kind == "config" and isinstance(payload.get("business_session_id"), str):
                    try:
                        session.session_id = str(uuid.UUID(payload["business_session_id"]))
                    except ValueError:
                        pass
                if kind == "config" and payload.get("business_test_mode") in {"microphone_echo", "synthetic_roundtrip"}:
                    session.provenance = {"kind": "integration_test", "producer": "openvoicestream-asgi", "mode": payload["business_test_mode"]}
                if kind == "edgestack_assistant_final":
                    text = payload.get("text")
                    if isinstance(text, str) and len(text) <= 16384:
                        event = record(session.final_reply, text, phase="generated")
                        await send({"type": "websocket.send", "text": json.dumps({"type": "edgestack_business_ack", "phase": "generated", "eventId": event["eventId"] if event else None, "sessionId": session.session_id})})
                        explicit_final = True
                    continue  # Private hook messages must not reach legacy dispatcher.
                if kind == "edgestack_playback_ack":
                    if payload.get("phase") in {"playing", "played", "interrupted"}:
                        event = record(session.playback, payload["phase"], actual_client_ack=True)
                        await send({"type": "websocket.send", "text": json.dumps({"type": "edgestack_business_ack", "phase": payload["phase"], "eventId": event["eventId"] if event else None, "sessionId": session.session_id})})
                    continue
                if kind == "text" and isinstance(payload.get("text"), str):
                    # Contract matches server/core/v2v.py CLIENT_TEXT. Bound RAM
                    # independently of client behavior; oversized replies fail visibly.
                    pending_chars += len(payload["text"])
                    if pending_chars <= 16384:
                        pending_text.append(payload["text"])
                elif kind == "tts_flush":
                    if pending_chars > 16384:
                        sys.stderr.write("edgestack_business_reply_too_large\n")
                    elif not explicit_final:
                        record(session.final_reply, "".join(pending_text), phase="tts_requested")
                    pending_text.clear()
                    pending_chars, explicit_final = 0, False
                elif kind == "abort":
                    pending_text.clear()
                    pending_chars, explicit_final = 0, False
                    record(session.playback, "synthesis_aborted")
                return message

        async def wrapped_send(message):
            # Record a successfully produced result even if the browser disconnects
            # during transport. The phase does not claim successful speaker output.
            if message.get("type") == "websocket.send" and message.get("text"):
                try:
                    payload = json.loads(message["text"])
                except (ValueError, TypeError):
                    payload = {}
                if isinstance(payload, dict):
                    kind = payload.get("type")
                    if kind == "asr_final" or (scope["path"] == "/asr/stream" and payload.get("is_final")):
                        text = payload.get("text")
                        if isinstance(text, str):
                            record(session.final_transcript, text, duplicate_of_streamed=bool(payload.get("duplicate_of_streamed")), language=payload.get("language"))
                    elif kind == "tts_started":
                        record(session.playback, "synthesis_started")
                    elif kind == "tts_done":
                        record(session.playback, "synthesis_complete")
            await send(message)

        await self.upstream(scope, wrapped_receive, wrapped_send)


def build_app():
    # This wrapper uses preinstalled read-only model mounts. The upstream
    # manifest also lists unused variants, so its downloader must default off.
    os.environ.setdefault("RK_ARTIFACT_AUTO_DOWNLOAD", "0")
    from rk_device_tree import install as install_device_tree
    install_device_tree(os.environ.get("RKNN_DEVICE_TREE_FILE", "/run/edgestack-device-tree/compatible"))
    from server import main as upstream_module
    from asr_compat import install as install_asr_compat
    install_asr_compat()
    upstream = upstream_module.app
    writer = EventWriter(os.environ["EVENT_OUTBOX"], os.environ["EVENT_SOURCE_ID"], os.environ["EVENT_TASK_ID"], producer="openvoicestream-asgi")
    observer = None
    try:
        from runtime_observer import install
        observer = install(upstream_module._get_asr_backend)
    except Exception as error:
        sys.stderr.write("edgestack_model_runtime_observer_unavailable:" + type(error).__name__ + "\n")
    return VoiceEventMiddleware(upstream, writer, runtime_observer=observer, token_file=os.environ.get("VOICE_UPSTREAM_TOKEN_FILE"))


# Lazy initialization keeps this module importable for isolated protocol tests.
_app = None


async def app(scope, receive, send):
    global _app
    if _app is None:
        _app = build_app()
    await _app(scope, receive, send)
