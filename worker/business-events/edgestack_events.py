"""Application hooks for the dedicated business-event outbox; never records media."""
from __future__ import annotations

import fcntl
import json
import os
from pathlib import Path
import sqlite3
import uuid

from adapter import canonical, now, validate


class EventWriter:
    def __init__(self, outbox, source_id, task_id, producer="voice-app", max_file_bytes=8 * 1024 * 1024, retained_files=8):
        self.outbox, self.source_id, self.task_id, self.producer = Path(outbox), source_id, task_id, producer
        self.max_file_bytes, self.retained_files = max_file_bytes, max(2, retained_files)
        self.outbox.mkdir(parents=True, exist_ok=True, mode=0o700)

    def emit(self, event_type, session_id, text="", *, event_id=None, reply_to_event_id=None, phase=None, provenance=None):
        """Returns the stable event object. Caller may append that same object on retry.

        Sequence allocation is durable before append: a producer crash between these
        steps leaves a detectable gap instead of reusing the sequence of a prior event.
        Use one session UUID per real session; do not derive identity from user content.
        """
        with (self.outbox / ".writer.lock").open("a") as lock:
            fcntl.flock(lock, fcntl.LOCK_EX)
            with sqlite3.connect(self.outbox / ".sequence.sqlite3") as db:
                db.execute("PRAGMA synchronous=FULL")
                db.execute("CREATE TABLE IF NOT EXISTS sessions (session TEXT PRIMARY KEY, sequence INTEGER)")
                db.execute("INSERT INTO sessions VALUES (?,1) ON CONFLICT(session) DO UPDATE SET sequence=sequence+1", (session_id,))
                sequence = db.execute("SELECT sequence FROM sessions WHERE session=?", (session_id,)).fetchone()[0]
            event = {"schemaVersion": 1, "eventId": event_id or str(uuid.uuid4()), "sourceId": self.source_id, "taskId": self.task_id,
                     "type": event_type, "occurredAt": now(), "sessionId": session_id, "sequence": sequence, "text": text,
                     "provenance": provenance or {"kind": "runtime", "producer": self.producer}}
            if reply_to_event_id:
                event["replyToEventId"] = reply_to_event_id
            if phase:
                event["phase"] = phase
            validate(event, self.source_id, self.task_id)
            self._append_locked(event)
            return event

    def append(self, event):
        """Retry a previously constructed event unchanged, preserving eventId and sequence."""
        validate(event, self.source_id, self.task_id)
        with (self.outbox / ".writer.lock").open("a") as lock:
            fcntl.flock(lock, fcntl.LOCK_EX)
            self._append_locked(event)

    def _append_locked(self, event):
        path = self.outbox / "events.jsonl"
        line = (canonical(event) + "\n").encode()
        if path.exists() and path.stat().st_size and path.stat().st_size + len(line) > self.max_file_bytes:
            (self.outbox / f"events.jsonl.{self.retained_files - 1}").unlink(missing_ok=True)
            for index in range(self.retained_files - 2, 0, -1):
                older = self.outbox / f"events.jsonl.{index}"
                if older.exists():
                    older.rename(self.outbox / f"events.jsonl.{index + 1}")
            path.rename(self.outbox / "events.jsonl.1")
        fd = os.open(path, os.O_WRONLY | os.O_APPEND | os.O_CREAT, 0o640)
        try:
            remaining = memoryview(line)
            while remaining:
                remaining = remaining[os.write(fd, remaining):]
            os.fsync(fd)
        finally:
            os.close(fd)


class VoiceSession:
    """Hooks called by the application that owns ASR, LLM and actual playback.

    ASR finals and completed LLM replies are authoritative. TTS server completion
    is recorded separately from the client's speaker/playback completion.
    """
    def __init__(self, writer, session_id=None, provenance=None):
        self.writer, self.session_id = writer, session_id or str(uuid.uuid4())
        self.provenance = provenance
        self.last_user_event_id = None
        self.last_reply_event_id = None

    def final_transcript(self, text, *, duplicate_of_streamed=False, language=None):
        if duplicate_of_streamed or not text.strip():
            return None
        provenance = dict(self.provenance or {"kind": "runtime", "producer": self.writer.producer})
        if isinstance(language, str) and len(language) <= 32:
            provenance["asrLanguage"] = language
        event = self.writer.emit("voice.user_utterance", self.session_id, text, phase="final", provenance=provenance)
        self.last_user_event_id = event["eventId"]
        return event

    def final_reply(self, text, *, phase="generated"):
        if not text.strip():
            return None
        event = self.writer.emit("voice.assistant_reply", self.session_id, text, phase=phase, reply_to_event_id=self.last_user_event_id, provenance=self.provenance)
        self.last_reply_event_id = event["eventId"]
        return event

    def playback(self, phase, *, actual_client_ack=False):
        if phase in {"playing", "played", "interrupted"} and not actual_client_ack:
            raise ValueError("actual_client_playback_ack_required")
        return self.writer.emit("voice.playback_state", self.session_id, "", phase=phase, reply_to_event_id=self.last_reply_event_id, provenance=self.provenance)
