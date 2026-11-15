import asyncio
import json
import os
from pathlib import Path
import sys
import tempfile
import unittest
from unittest import mock
from types import ModuleType, SimpleNamespace

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))
from adapter import Adapter, canonical
from edgestack_events import EventWriter, VoiceSession
from voice_asgi import VoiceEventMiddleware
from voice_asgi import build_app


class Transport:
    def __init__(self):
        self.requests, self.received = [], {}
        self.fail_after_accept = False
        self.fail = False
        self.bad_receipt = False

    def post(self, path, payload):
        self.requests.append((path, payload))
        if self.fail:
            raise OSError("network unavailable")
        if path.endswith("status"):
            return {"ok": True}
        accepted, duplicates = 0, 0
        for event in payload["events"]:
            if event["eventId"] in self.received:
                assert self.received[event["eventId"]] == event
                duplicates += 1
            else:
                self.received[event["eventId"]] = event
                accepted += 1
        if self.fail_after_accept:
            self.fail_after_accept = False
            raise TimeoutError("response lost after commit")
        return {} if self.bad_receipt else {"accepted": accepted, "duplicates": duplicates}


def event(sequence=1, event_id=None):
    return {"schemaVersion": 1, "eventId": event_id or f"event-{sequence}", "sourceId": "voice-source", "taskId": "voice-task",
            "type": "voice.user_utterance", "occurredAt": "2026-11-15T00:00:00Z", "sessionId": "session-1", "sequence": sequence, "text": "test utterance", "phase": "final"}


class AdapterTests(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.root = Path(self.tmp.name)
        self.outbox = self.root / "outbox"
        self.outbox.mkdir()
        self.transport = Transport()
        self.adapter = self.open()

    def open(self, **kwargs):
        return Adapter(self.outbox, self.root / "state", "voice-source", "voice-task", "rk3588", self.transport, **kwargs)

    def tearDown(self):
        self.adapter.close()
        self.tmp.cleanup()

    def append(self, payload):
        with (self.outbox / "events.jsonl").open("a") as stream:
            stream.write(canonical(payload) + "\n")

    def test_lost_response_restart_retries_identical_event(self):
        self.append(event())
        self.adapter.scan()
        self.transport.fail_after_accept = True
        self.assertFalse(self.adapter.flush(current_time=100))
        retry_at = self.adapter.get("retryAt")
        self.adapter.close()
        self.adapter = self.open()
        self.adapter.scan()
        self.assertEqual(self.adapter.queue_stats()[0], 1)
        self.assertFalse(self.adapter.flush(current_time=100))
        self.assertTrue(self.adapter.flush(current_time=retry_at + 1))
        self.assertEqual(self.adapter.queue_stats()[0], 0)
        self.assertEqual(len(self.transport.received), 1)
        self.assertEqual(self.transport.requests[0][1], self.transport.requests[1][1])

    def test_partial_line_not_consumed_until_newline(self):
        line = canonical(event())
        path = self.outbox / "events.jsonl"
        path.write_text(line[:50])
        self.adapter.scan()
        self.assertEqual(self.adapter.queue_stats()[0], 0)
        with path.open("a") as stream:
            stream.write(line[50:] + "\n")
        self.adapter.scan()
        self.assertEqual(self.adapter.queue_stats()[0], 1)
        self.assertEqual(self.adapter.get("gapCount", 0), 0)

    def test_rename_rotation_is_drained_and_restart_cursor_persists(self):
        self.append(event())
        self.adapter.scan()
        (self.outbox / "events.jsonl").rename(self.outbox / "events.jsonl.1")
        self.append(event(2))
        self.adapter.scan()
        self.assertEqual(self.adapter.queue_stats()[0], 2)
        self.adapter.flush()
        self.adapter.close()
        self.adapter = self.open()
        self.adapter.scan()
        self.assertEqual(self.adapter.queue_stats()[0], 0)
        self.assertEqual(self.adapter.get("gapCount", 0), 0)

    def test_copytruncate_recovery_reports_loss_and_reads_new_event(self):
        self.append(event())
        self.adapter.scan()
        (self.outbox / "events.jsonl").write_text(canonical(event(2)) + "\n")
        self.adapter.scan()
        self.assertEqual(self.adapter.queue_stats()[0], 2)
        self.assertEqual(self.adapter.get("recentGaps")[0]["reason"], "file_truncated_or_replaced")

    def test_queue_limit_stops_cursor_and_resumes_after_delivery(self):
        self.adapter.close()
        self.adapter = self.open(max_queue_events=1)
        self.append(event())
        self.append(event(2))
        self.adapter.scan()
        self.assertEqual(self.adapter.queue_stats()[0], 1)
        self.assertTrue(self.adapter.get("backpressure"))
        self.adapter.flush()
        self.adapter.scan()
        self.assertEqual(self.adapter.queue_stats()[0], 1)
        self.adapter.flush()
        self.assertEqual(len(self.transport.received), 2)

    def test_bad_receipt_keeps_queue_and_failed_status_does_not_ack(self):
        self.append(event())
        self.adapter.scan()
        self.transport.bad_receipt = True
        self.assertFalse(self.adapter.flush())
        self.transport.fail = True
        self.assertFalse(self.adapter.report_status())
        self.assertEqual(self.adapter.queue_stats()[0], 1)

    def test_invalid_record_scope_sequence_and_removed_partial_report_gaps(self):
        self.append({**event(), "sourceId": "unrelated-source"})
        self.append(event(4))
        path = self.outbox / "events.jsonl"
        with path.open("a") as stream:
            stream.write("partial")
        self.adapter.scan()
        path.unlink()
        self.adapter.scan()
        coverage = self.adapter.status()["coverage"]
        self.assertFalse(coverage["complete"])
        self.assertEqual(coverage["gapCount"], 3)
        self.assertEqual(self.adapter.queue_stats()[0], 1)

    def test_source_binding_cannot_change_on_restart(self):
        with self.assertRaisesRegex(ValueError, "state_binding_mismatch"):
            Adapter(self.outbox, self.root / "state", "other-source", "voice-task", "rk3588", self.transport)

    def test_oversized_record_is_not_queued_and_reports_gap(self):
        self.adapter.max_line_bytes = 40
        self.append(event())
        self.adapter.scan()
        self.assertEqual(self.adapter.queue_stats()[0], 0)
        self.assertEqual(self.adapter.get("recentGaps")[0]["reason"], "line_too_large")

    def test_writer_restarts_preserve_sequence_and_reply_linkage(self):
        writer = EventWriter(self.outbox, "voice-source", "voice-task")
        session = VoiceSession(writer, "real-session")
        user = session.final_transcript("test final")
        reply = session.final_reply("test reply")
        self.assertEqual(reply["replyToEventId"], user["eventId"])
        with self.assertRaisesRegex(ValueError, "actual_client"):
            session.playback("played")
        replay = EventWriter(self.outbox, "voice-source", "voice-task").emit("voice.playback_state", "real-session", phase="synthesis_complete")
        self.assertEqual(replay["sequence"], 3)
        self.adapter.scan()
        self.assertEqual(self.adapter.queue_stats()[0], 3)

    def test_asgi_records_real_protocol_finals_and_distinguishes_playback(self):
        async def run():
            incoming = iter([
                {"type": "websocket.receive", "text": json.dumps({"type": "text", "text": "test reply"})},
                {"type": "websocket.receive", "text": json.dumps({"type": "tts_flush"})},
                {"type": "websocket.receive", "text": json.dumps({"type": "edgestack_playback_ack", "phase": "played"})},
                {"type": "websocket.disconnect"},
            ])
            async def receive():
                return next(incoming)
            async def send(_):
                pass
            async def upstream(scope, receive, send):
                await send({"type": "websocket.send", "text": json.dumps({"type": "asr_final", "text": "test final", "language": "zh"})})
                await send({"type": "websocket.send", "text": json.dumps({"type": "asr_final", "text": "test final", "duplicate_of_streamed": True})})
                self.assertEqual(json.loads((await receive())["text"])["type"], "text")
                self.assertEqual(json.loads((await receive())["text"])["type"], "tts_flush")
                await send({"type": "websocket.send", "text": json.dumps({"type": "tts_done"})})
                self.assertEqual((await receive())["type"], "websocket.disconnect")
            app = VoiceEventMiddleware(upstream, EventWriter(self.outbox, "voice-source", "voice-task"))
            await app({"type": "websocket", "path": "/v2v/stream"}, receive, send)
        asyncio.run(run())
        self.adapter.scan()
        payloads = [json.loads(row[0]) for row in self.adapter.db.execute("SELECT payload FROM queue ORDER BY rowid")]
        self.assertEqual([item["phase"] for item in payloads], ["final", "tts_requested", "synthesis_complete", "played"])
        self.assertEqual(payloads[1]["replyToEventId"], payloads[0]["eventId"])
        self.assertEqual(payloads[0]["provenance"]["asrLanguage"], "zh")

    def test_explicit_echo_reply_has_test_provenance_and_durable_receipt(self):
        messages, outgoing = [
            {"type": "config", "business_session_id": "978dfe6e-0778-4776-97cc-a86f7061c09e", "business_test_mode": "microphone_echo"},
            {"type": "edgestack_assistant_final", "text": "controlled echo reply"},
            {"type": "text", "text": "controlled echo reply"},
            {"type": "tts_flush"},
        ], []
        async def receive():
            if messages:
                return {"type": "websocket.receive", "text": json.dumps(messages.pop(0))}
            return {"type": "websocket.disconnect"}
        async def send(message):
            outgoing.append(message)
        async def upstream(scope, receive, send):
            for expected in ["config", "text", "tts_flush"]:
                self.assertEqual(json.loads((await receive())["text"])["type"], expected)
        app = VoiceEventMiddleware(upstream, EventWriter(self.outbox, "voice-source", "voice-task"))
        asyncio.run(app({"type": "websocket", "path": "/v2v/stream"}, receive, send))
        self.adapter.scan()
        payloads = [json.loads(row[0]) for row in self.adapter.db.execute("SELECT payload FROM queue")]
        self.assertEqual(len(payloads), 1)
        self.assertEqual(payloads[0]["phase"], "generated")
        self.assertEqual(payloads[0]["provenance"]["kind"], "integration_test")
        self.assertEqual(payloads[0]["sessionId"], "978dfe6e-0778-4776-97cc-a86f7061c09e")
        self.assertEqual(json.loads(outgoing[0]["text"])["eventId"], payloads[0]["eventId"])

    def test_model_info_never_reports_desired_model_as_loaded(self):
        outgoing = []
        async def send(message):
            outgoing.append(message)
        app = VoiceEventMiddleware(None, None)
        asyncio.run(app({"type": "http", "path": "/model-info"}, None, send))
        info = json.loads(outgoing[1]["body"])
        self.assertFalse(info["asr"]["confirmed"])
        self.assertIsNone(info["asr"]["loaded"])

    def test_voice_wrapper_defaults_artifact_download_off_before_loading_upstream(self):
        server = ModuleType("server")
        server.main = SimpleNamespace(app=object(), _get_asr_backend=lambda: None)
        environment = {"EVENT_OUTBOX": str(self.outbox), "EVENT_SOURCE_ID": "voice-source", "EVENT_TASK_ID": "voice-task"}
        with mock.patch.dict(os.environ, environment, clear=True), mock.patch.dict(sys.modules, {"server": server}), mock.patch("runtime_observer.install", return_value=None), mock.patch("rk_device_tree.install", return_value={}), mock.patch("asr_compat.install"):
            build_app()
            self.assertEqual(os.environ["RK_ARTIFACT_AUTO_DOWNLOAD"], "0")
            os.environ["RK_ARTIFACT_AUTO_DOWNLOAD"] = "1"
            build_app()
            self.assertEqual(os.environ["RK_ARTIFACT_AUTO_DOWNLOAD"], "1")

    def test_voice_upstream_auth_protects_all_http_and_websocket_paths(self):
        token_file = self.root / "voice-upstream-token"
        token_file.write_text("test-worker-upstream-secret\n")
        forwarded = []
        async def upstream(scope, receive, send):
            forwarded.append(scope["path"])
        app = VoiceEventMiddleware(upstream, EventWriter(self.outbox, "voice-source", "voice-task"), token_file=token_file)
        async def request(kind, path, headers=None):
            outgoing = []
            async def send(message):
                outgoing.append(message)
            await app({"type": kind, "path": path, "headers": headers or []}, None, send)
            return outgoing
        for path in ("/health", "/model-info", "/tts", "/unknown-route"):
            self.assertEqual(asyncio.run(request("http", path))[0]["status"], 401)
        for path in ("/v2v/stream", "/asr/stream", "/unknown-route"):
            self.assertEqual(asyncio.run(request("websocket", path))[0], {"type": "websocket.close", "code": 4401})
        self.assertEqual(forwarded, [])
        self.assertFalse((self.outbox / "events.jsonl").exists())
        valid = [(b"authorization", b"Bearer test-worker-upstream-secret")]
        asyncio.run(request("http", "/health", valid))
        self.assertEqual(forwarded, ["/health"])
        self.assertEqual(asyncio.run(request("http", "/model-info", valid))[0]["status"], 200)
        self.assertEqual(asyncio.run(request("http", "/health", valid + valid))[0]["status"], 401)
        self.assertEqual(asyncio.run(request("http", "/health", [(b"authorization", b"Bearer wrong-token")]))[0]["status"], 401)
        token_file.write_text("rotated-worker-secret")
        self.assertEqual(asyncio.run(request("http", "/health", valid))[0]["status"], 401)
        asyncio.run(request("http", "/health", [(b"authorization", b"Bearer rotated-worker-secret")]))
        self.assertEqual(forwarded, ["/health", "/health"])
        token_file.unlink()
        self.assertEqual(asyncio.run(request("http", "/model-info", valid))[0]["status"], 503)
        self.assertEqual(asyncio.run(request("websocket", "/v2v/stream", valid))[0]["code"], 1013)


if __name__ == "__main__":
    unittest.main()
