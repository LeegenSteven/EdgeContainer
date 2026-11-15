#!/usr/bin/env python3
"""Synthetic speech integration test. Run in the speech image (websockets needed).

TTS generates the input utterance in RAM. The real ASR websocket recognizes
it, then a controlled text reply exercises TTS and the business-event hooks.
No microphone, dialogue model, or actual speaker playback is claimed.
"""
import argparse
import asyncio
import audioop
import inspect
import io
import json
from pathlib import Path
import urllib.request
import wave


async def run(args):
    import websockets
    base = args.url.rstrip("/")
    headers = {}
    if args.voice_token_file:
        headers["Authorization"] = "Bearer " + Path(args.voice_token_file).read_text().strip()
    request = urllib.request.Request(base + "/tts", data=json.dumps({"text": args.text}).encode(),
                                     headers={"Content-Type": "application/json", **headers}, method="POST")
    with urllib.request.urlopen(request, timeout=args.timeout) as response:
        audio = response.read(16 * 1024 * 1024)
    with wave.open(io.BytesIO(audio), "rb") as wav:
        width, channels, rate = wav.getsampwidth(), wav.getnchannels(), wav.getframerate()
        pcm = wav.readframes(wav.getnframes())
    if width != 2 or channels != 1:
        raise RuntimeError("Expected mono signed 16-bit TTS WAV")
    if rate != 16000:
        pcm, _ = audioop.ratecv(pcm, 2, 1, rate, 16000, None)
    pcm += b"\x00\x00" * 8000  # Synthetic endpoint silence, never written to disk.
    ws_url = ("wss://" if base.startswith("https://") else "ws://") + base.split("://", 1)[1] + "/v2v/stream"
    kwargs = {"max_size": 4 * 1024 * 1024, "open_timeout": args.timeout}
    if headers:
        parameter = "additional_headers" if "additional_headers" in inspect.signature(websockets.connect).parameters else "extra_headers"
        kwargs[parameter] = headers
    recognized, output_bytes, first_binary = "", 0, True
    async with websockets.connect(ws_url, **kwargs) as socket:
        await socket.send(json.dumps({"type": "config", "asr_language": args.language, "tts_language": args.language,
                                      "sample_rate": 16000, "multi_utterance": True, "business_test_mode": "synthetic_roundtrip"}))
        for offset in range(0, len(pcm), 3200):
            await socket.send(pcm[offset:offset + 3200])
            await asyncio.sleep(0.01)
        await socket.send(json.dumps({"type": "asr_eos"}))
        while True:
            frame = await asyncio.wait_for(socket.recv(), args.timeout)
            if isinstance(frame, bytes):
                continue
            payload = json.loads(frame)
            if payload.get("type") == "error":
                raise RuntimeError("Speech service returned an ASR error")
            if payload.get("type") == "asr_final" and payload.get("text"):
                recognized = payload["text"]
                break
        # Controlled fixture, not an LLM-generated assistant answer. The wrapper
        # records this as tts_requested. Never send an actual-playback ack here.
        await socket.send(json.dumps({"type": "text", "text": args.reply}))
        await socket.send(json.dumps({"type": "tts_flush"}))
        while True:
            frame = await asyncio.wait_for(socket.recv(), args.timeout)
            if isinstance(frame, bytes):
                if first_binary and len(frame) == 4:
                    first_binary = False
                else:
                    output_bytes += len(frame)
                continue
            payload = json.loads(frame)
            if payload.get("type") == "error":
                raise RuntimeError("Speech service returned a TTS error")
            if payload.get("type") == "tts_done":
                break
    if not recognized.strip() or output_bytes < 1000:
        raise RuntimeError("Roundtrip did not produce ASR text and synthesized reply audio")
    return {"synthetic": True, "inputText": args.text, "recognizedText": recognized, "controlledReply": args.reply,
            "receivedReplyAudioBytes": output_bytes, "microphoneVerified": False, "dialogueModelVerified": False, "speakerPlaybackVerified": False}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--url", default="http://127.0.0.1:8621")
    parser.add_argument("--text", default="你好，这是边缘设备业务事件集成测试。")
    parser.add_argument("--reply", default="测试请求已经收到，业务事件已记录。")
    parser.add_argument("--language", default="zh")
    parser.add_argument("--timeout", type=float, default=120)
    parser.add_argument("--voice-token-file")
    args = parser.parse_args()
    print(json.dumps(asyncio.run(run(args)), ensure_ascii=False))


if __name__ == "__main__":
    main()
