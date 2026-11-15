#!/usr/bin/env python3
"""Durable, source-scoped JSONL business-event transport. Python 3.11+ only."""
from __future__ import annotations

import argparse
import datetime as dt
import hashlib
import hmac
import http.client
import json
import os
from pathlib import Path
import random
import signal
import sqlite3
import ssl
import time
import urllib.error
import urllib.request
import urllib.parse

TYPES = {"voice.user_utterance", "voice.assistant_reply", "voice.playback_state", "camera.observation"}
FIELDS = {"schemaVersion", "eventId", "sourceId", "taskId", "type", "occurredAt", "sessionId", "sequence", "text", "replyToEventId", "provenance", "phase"}


def now():
    return dt.datetime.now(dt.timezone.utc).isoformat().replace("+00:00", "Z")


def canonical(value):
    return json.dumps(value, ensure_ascii=False, sort_keys=True, separators=(",", ":"))


def validate(event, source_id, task_id):
    if not isinstance(event, dict) or set(event) - FIELDS:
        raise ValueError("invalid_fields")
    for name in ("eventId", "sourceId", "taskId", "sessionId"):
        if not isinstance(event.get(name), str) or not 1 <= len(event[name]) <= 200:
            raise ValueError("invalid_identity")
    if event["sourceId"] != source_id or event["taskId"] != task_id:
        raise ValueError("source_scope_mismatch")
    if type(event.get("schemaVersion")) is not int or event["schemaVersion"] != 1 or event.get("type") not in TYPES:
        raise ValueError("invalid_schema_or_type")
    if type(event.get("sequence")) is not int or event["sequence"] < 1:
        raise ValueError("invalid_sequence")
    try:
        timestamp = dt.datetime.fromisoformat(event["occurredAt"].replace("Z", "+00:00"))
        if timestamp.tzinfo is None:
            raise ValueError()
    except (ValueError, KeyError, TypeError, AttributeError):
        raise ValueError("invalid_timestamp") from None
    if not isinstance(event.get("text"), str) or len(event["text"]) > 16384:
        raise ValueError("invalid_text")
    if event["type"] != "voice.playback_state" and not event["text"].strip():
        raise ValueError("empty_text")
    if "replyToEventId" in event and (not isinstance(event["replyToEventId"], str) or len(event["replyToEventId"]) > 200):
        raise ValueError("invalid_reply_reference")
    if "phase" in event and (not isinstance(event["phase"], str) or len(event["phase"]) > 64):
        raise ValueError("invalid_phase")
    if "provenance" in event and (not isinstance(event["provenance"], dict) or len(canonical(event["provenance"])) > 4096):
        raise ValueError("invalid_provenance")
    return event


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        raise urllib.error.HTTPError(req.full_url, code, "ingestion_redirect_rejected", headers, fp)


class PinnedHTTPSConnection(http.client.HTTPSConnection):
    def __init__(self, *args, cert_sha256, **kwargs):
        self.cert_sha256 = cert_sha256
        super().__init__(*args, **kwargs)

    def connect(self):
        super().connect()
        # http.client calls connect before sending HTTP headers or body, so an
        # untrusted peer never receives the source ingestion credential.
        actual = hashlib.sha256(self.sock.getpeercert(binary_form=True)).hexdigest()
        if not hmac.compare_digest(actual, self.cert_sha256):
            self.close()
            raise ssl.SSLCertVerificationError("ingestion_leaf_certificate_pin_mismatch")


class PinnedHTTPSHandler(urllib.request.HTTPSHandler):
    def __init__(self, context, cert_sha256):
        super().__init__(context=context)
        self.cert_sha256 = cert_sha256

    def https_open(self, request):
        def connection(host, **kwargs):
            return PinnedHTTPSConnection(host, cert_sha256=self.cert_sha256, **kwargs)
        return self.do_open(connection, request, context=self._context)


class HTTPTransport:
    def __init__(self, base_url, token_file, timeout=10, ca_file=None, cert_sha256=None):
        self.base_url = base_url.rstrip("/")
        parsed = urllib.parse.urlsplit(self.base_url)
        if parsed.scheme not in {"https", "http"} or not parsed.hostname or parsed.username or parsed.password or parsed.query or parsed.fragment:
            raise ValueError("Server URL must use HTTP(S)")
        ca_file, cert_sha256 = ca_file or None, cert_sha256 or None
        if (ca_file or cert_sha256) and parsed.scheme != "https":
            raise ValueError("ingestion_tls_configuration_requires_https")
        if cert_sha256 is not None:
            cert_sha256 = cert_sha256.lower()
            if len(cert_sha256) != 64 or any(character not in "0123456789abcdef" for character in cert_sha256):
                raise ValueError("INGEST_CERT_SHA256 must be exactly 64 hexadecimal characters")
        handlers = [NoRedirect()]
        if parsed.scheme == "https":
            if cert_sha256 and not ca_file:
                # Exact leaf identity is the trust anchor for self-signed/LAN
                # endpoints. This context is usable only with the mandatory
                # connect-time pin check above; there is no unverified mode.
                context = ssl.SSLContext(ssl.PROTOCOL_TLS_CLIENT)
                context.check_hostname = False
                context.verify_mode = ssl.CERT_NONE
            else:
                context = ssl.create_default_context(cafile=ca_file)
            if cert_sha256:
                context.check_hostname = False
                handlers.append(PinnedHTTPSHandler(context, cert_sha256))
            else:
                handlers.append(urllib.request.HTTPSHandler(context=context))
        self.token_file, self.timeout = Path(token_file), timeout
        self.opener = urllib.request.build_opener(*handlers)

    def post(self, path, payload):
        # Re-read to permit independent credential rotation without restarting.
        token = self.token_file.read_text().strip()
        if not token or "\n" in token or "\r" in token:
            raise ValueError("invalid_ingestion_credential")
        request = urllib.request.Request(self.base_url + path, data=canonical(payload).encode(),
                                        headers={"Content-Type": "application/json", "Authorization": "Bearer " + token}, method="POST")
        with self.opener.open(request, timeout=self.timeout) as response:
            return json.loads(response.read(1024 * 1024))


class Adapter:
    def __init__(self, outbox, state, source_id, task_id, device_id, transport,
                 pattern="events*.jsonl*", max_queue_events=10000, max_queue_bytes=32 * 1024 * 1024,
                 max_line_bytes=128 * 1024, batch_size=100):
        self.outbox, self.source_id, self.task_id, self.device_id = Path(outbox), source_id, task_id, device_id
        self.pattern, self.transport = pattern, transport
        self.max_queue_events, self.max_queue_bytes, self.max_line_bytes = max_queue_events, max_queue_bytes, max_line_bytes
        self.batch_size = min(500, batch_size)
        state = Path(state)
        state.mkdir(parents=True, exist_ok=True, mode=0o700)
        self.db = sqlite3.connect(state / "adapter.sqlite3")
        self.db.row_factory = sqlite3.Row
        self.db.execute("PRAGMA journal_mode=WAL")
        self.db.execute("PRAGMA synchronous=FULL")
        self.db.executescript("""
            CREATE TABLE IF NOT EXISTS files (identity TEXT PRIMARY KEY, path TEXT, offset INTEGER, size INTEGER, anchor TEXT, anchor_length INTEGER);
            CREATE TABLE IF NOT EXISTS queue (event_id TEXT PRIMARY KEY, payload TEXT, bytes INTEGER, queued_at TEXT);
            CREATE TABLE IF NOT EXISTS sessions (session_id TEXT PRIMARY KEY, sequence INTEGER);
            CREATE TABLE IF NOT EXISTS meta (key TEXT PRIMARY KEY, value TEXT);
        """)
        self.db.commit()
        if self.get("startedAt") is None:
            with self.db:
                self.set("startedAt", now())
        # Source/task changes must not replay an existing spool using a different credential.
        binding = {"sourceId": source_id, "taskId": task_id, "deviceId": device_id}
        if self.get("binding") not in (None, binding):
            self.db.close()
            raise ValueError("state_binding_mismatch")
        with self.db:
            self.set("binding", binding)

    def close(self):
        self.db.close()

    def get(self, key, default=None):
        row = self.db.execute("SELECT value FROM meta WHERE key=?", (key,)).fetchone()
        return json.loads(row[0]) if row else default

    def set(self, key, value):
        self.db.execute("INSERT OR REPLACE INTO meta VALUES (?, ?)", (key, canonical(value)))

    def gap(self, reason, **details):
        total = self.get("gapCount", 0) + 1
        self.set("gapCount", total)
        gaps = self.get("recentGaps", [])[-31:]
        gaps.append({"reason": reason, "at": now(), **details})
        self.set("recentGaps", gaps)

    def queue_stats(self):
        row = self.db.execute("SELECT COUNT(*), COALESCE(SUM(bytes),0) FROM queue").fetchone()
        return row[0], row[1]

    def scan(self):
        """Commit each cursor advance with its queued event; never consume partial lines."""
        paths = []
        try:
            if not self.outbox.is_dir():
                raise OSError("outbox_unavailable")
            for path in self.outbox.glob(self.pattern):
                # Producer files only; never follow symlinks into model/data mounts.
                if path.is_file() and not path.is_symlink():
                    info = path.stat()
                    paths.append((info.st_mtime_ns, path, info))
        except OSError:
            with self.db:
                self.set("readError", "outbox_unavailable")
            return
        seen = set()
        for _, path, info in sorted(paths, key=lambda item: (item[0], str(item[1]))):
            identity = f"{info.st_dev}:{info.st_ino}"
            seen.add(identity)
            row = self.db.execute("SELECT * FROM files WHERE identity=?", (identity,)).fetchone()
            offset = row["offset"] if row else 0
            try:
                with path.open("rb") as stream:
                    actual = os.fstat(stream.fileno())
                    if (actual.st_dev, actual.st_ino) != (info.st_dev, info.st_ino):
                        continue  # Rotation race: rescan, don't attribute bytes to the wrong inode.
                    anchor_length = row["anchor_length"] if row and row["anchor_length"] else min(info.st_size, 128)
                    anchor = hashlib.sha256(stream.read(anchor_length)).hexdigest()
                    if row and (actual.st_size < offset or (row["anchor_length"] and anchor != row["anchor"])):
                        with self.db:
                            self.gap("file_truncated_or_replaced", file=path.name, previousOffset=offset)
                        offset = 0
                        anchor_length = min(actual.st_size, 128)
                        stream.seek(0)
                        anchor = hashlib.sha256(stream.read(anchor_length)).hexdigest()
                    with self.db:
                        self.db.execute("INSERT OR REPLACE INTO files VALUES (?,?,?,?,?,?)", (identity, path.name, offset, actual.st_size, anchor, anchor_length))
                    stream.seek(offset)
                    while True:
                        line = stream.readline(self.max_line_bytes + 1)
                        if not line:
                            break
                        # Incomplete oversized lines are skipped only once a newline appears.
                        oversized = len(line) > self.max_line_bytes
                        end = stream.tell()
                        if oversized and not line.endswith(b"\n"):
                            while line and not line.endswith(b"\n"):
                                line = stream.readline(self.max_line_bytes + 1)
                            if not line:
                                break
                            end = stream.tell()
                        elif not line.endswith(b"\n"):
                            break
                        event, failure = None, None
                        try:
                            if oversized:
                                raise ValueError("line_too_large")
                            event = validate(json.loads(line), self.source_id, self.task_id)
                        except (ValueError, UnicodeError, RecursionError):
                            failure = "line_too_large" if oversized else "invalid_event"
                        payload = canonical(event) if event is not None else ""
                        count, size = self.queue_stats()
                        if event is not None and (count >= self.max_queue_events or size + len(payload.encode()) > self.max_queue_bytes):
                            with self.db:
                                self.set("backpressure", True)
                            return
                        with self.db:
                            if failure:
                                self.gap(failure, file=path.name, offset=offset)
                            else:
                                existing = self.db.execute("SELECT payload FROM queue WHERE event_id=?", (event["eventId"],)).fetchone()
                                if existing and existing[0] != payload:
                                    self.gap("event_id_content_conflict", eventId=event["eventId"])
                                else:
                                    previous = self.db.execute("SELECT sequence FROM sessions WHERE session_id=?", (event["sessionId"],)).fetchone()
                                    if not previous and event["sequence"] > 1:
                                        self.gap("session_prefix_unobserved", sessionId=event["sessionId"], firstSequence=event["sequence"])
                                    if previous and event["sequence"] > previous[0] + 1:
                                        self.gap("sequence_gap", sessionId=event["sessionId"], after=previous[0], before=event["sequence"])
                                    self.db.execute("INSERT INTO sessions VALUES (?,?) ON CONFLICT(session_id) DO UPDATE SET sequence=MAX(sequence,excluded.sequence)", (event["sessionId"], event["sequence"]))
                                    self.db.execute("INSERT OR IGNORE INTO queue VALUES (?,?,?,?)", (event["eventId"], payload, len(payload.encode()), now()))
                                    self.set("lastEventAt", event["occurredAt"])
                            self.db.execute("UPDATE files SET offset=?,size=? WHERE identity=?", (end, actual.st_size, identity))
                            offset = end
                            self.set("backpressure", False)
                            self.set("readError", None)
            except OSError:
                with self.db:
                    self.set("readError", "outbox_read_failed")
        # A vanished file with unread bytes is evidence of loss, not an empty source.
        for row in self.db.execute("SELECT * FROM files").fetchall():
            if row["identity"] not in seen:
                with self.db:
                    if row["size"] > row["offset"]:
                        self.gap("unread_file_removed", file=row["path"], unreadBytes=row["size"] - row["offset"])
                    self.db.execute("DELETE FROM files WHERE identity=?", (row["identity"],))

    def flush(self, current_time=None):
        current_time = time.time() if current_time is None else current_time
        if current_time < self.get("retryAt", 0):
            return False
        rows = self.db.execute("SELECT * FROM queue ORDER BY rowid LIMIT ?", (self.batch_size,)).fetchall()
        if not rows:
            return True
        # Keep request bodies bounded even for Unicode text near the per-event limit.
        batch, total_bytes = [], 0
        for row in rows:
            if batch and total_bytes + row["bytes"] > 512 * 1024:
                break
            batch.append(row)
            total_bytes += row["bytes"]
        rows = batch
        try:
            result = self.transport.post("/api/edgestack/ingest/events", {"events": [json.loads(row["payload"]) for row in rows]})
            accepted, duplicates = result.get("accepted"), result.get("duplicates")
            if type(accepted) is not int or type(duplicates) is not int or min(accepted, duplicates) < 0 or accepted + duplicates != len(rows):
                raise ValueError("invalid_ingestion_receipt")
        except Exception as error:
            # No HTTP body/exception text is persisted or printed (may contain secrets/content).
            code = "http_" + str(error.code) if isinstance(error, urllib.error.HTTPError) else "delivery_failed"
            with self.db:
                failures = self.get("failures", 0) + 1
                self.set("failures", failures)
                self.set("deliveryError", code)
                self.set("retryAt", current_time + min(300, 2 ** min(failures, 8)) + random.uniform(0, 1))
            return False
        with self.db:
            self.db.executemany("DELETE FROM queue WHERE event_id=?", [(row["event_id"],) for row in rows])
            self.set("lastDeliveryAt", now())
            self.set("deliveryError", None)
            self.set("failures", 0)
            self.set("retryAt", 0)
        return True

    def status(self):
        count, size = self.queue_stats()
        error = self.get("readError") or self.get("deliveryError")
        status = "blocked" if error else "backpressure" if self.get("backpressure") else "streaming"
        return {"sourceId": self.source_id, "taskId": self.task_id, "deviceId": self.device_id,
                "status": status, "checkpoint": {"files": [dict(row) for row in self.db.execute("SELECT path,offset,size FROM files")],
                "queueDepth": count, "queueBytes": size, "lastDeliveryAt": self.get("lastDeliveryAt")},
                "lastEventAt": self.get("lastEventAt"), "errorCode": error or "",
                "coverage": {"complete": False, "basis": "observed_producer_jsonl_only", "observedFrom": self.get("startedAt"),
                             "gapCount": self.get("gapCount", 0), "recentGaps": self.get("recentGaps", []),
                             "rawMediaCollected": False, "producerActivityKnown": bool(self.get("lastEventAt"))}}

    def report_status(self):
        try:
            self.transport.post("/api/edgestack/ingest/status", self.status())
            return True
        except Exception:
            return False


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--once", action="store_true")
    args = parser.parse_args()
    transport = HTTPTransport(os.environ["INGEST_BASE_URL"], os.environ.get("INGEST_TOKEN_FILE", "/run/secrets/ingest_token"),
                              ca_file=os.environ.get("INGEST_CA_FILE"), cert_sha256=os.environ.get("INGEST_CERT_SHA256"))
    adapter = Adapter(os.environ.get("EVENT_OUTBOX", "/outbox"), os.environ.get("ADAPTER_STATE", "/state"),
                      os.environ["EVENT_SOURCE_ID"], os.environ["EVENT_TASK_ID"], os.environ["EVENT_DEVICE_ID"], transport,
                      max_queue_events=int(os.environ.get("MAX_QUEUE_EVENTS", "10000")),
                      max_queue_bytes=int(os.environ.get("MAX_QUEUE_BYTES", str(32 * 1024 * 1024))))
    stopped = False
    def stop(*_):
        nonlocal stopped
        stopped = True
    signal.signal(signal.SIGTERM, stop)
    signal.signal(signal.SIGINT, stop)
    next_status = 0
    try:
        while not stopped:
            adapter.scan()
            adapter.flush()
            if time.monotonic() >= next_status or args.once:
                adapter.report_status()
                next_status = time.monotonic() + 15
            if args.once:
                break
            time.sleep(1)
    finally:
        adapter.close()


if __name__ == "__main__":
    main()
