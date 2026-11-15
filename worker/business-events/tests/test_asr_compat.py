from pathlib import Path
from types import SimpleNamespace
import sys
import unittest

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))
from asr_compat import install, unpack_typed_final


class ASRCompatibilityTests(unittest.TestCase):
    def test_short_finalize_preserves_typed_text_and_language_once(self):
        class Inner:
            calls = 0
            def finalize(self):
                self.calls += 1
                return ("你好，测试。", "zh")
        class Stream:
            _chunks, _sample_rate, _long_audio_threshold_s = [range(16000)], 16000, 15
            def _live_inner(self):
                return self.inner
            def finalize(self):
                raise AssertionError("old short path must not stringify tuple")
        install(SimpleNamespace(_RKASRStreamAdapter=Stream, _clean_segment_text=lambda text: text.strip()))
        stream = Stream()
        stream.inner = Inner()
        self.assertEqual(stream.finalize(), ("你好，测试。", "zh"))
        self.assertEqual(stream.inner.calls, 1)
        stream._chunks = []
        self.assertEqual(stream.finalize(), ("你好，测试。", "zh"))
        self.assertEqual(stream.inner.calls, 2)

    def test_long_audio_calls_original_segmentation_without_consuming_inner(self):
        class Stream:
            _chunks, _sample_rate, _long_audio_threshold_s = [range(16001)], 1000, 15
            def _live_inner(self):
                raise AssertionError("must let original long path manage lifecycle")
            def finalize(self):
                return "original segmented output", None
        module = SimpleNamespace(_RKASRStreamAdapter=Stream, _clean_segment_text=lambda text: text)
        install(module)
        installed = Stream.finalize
        install(module)
        self.assertIs(Stream.finalize, installed)
        self.assertEqual(Stream().finalize(), ("original segmented output", None))

    def test_only_typed_results_are_unpacked_without_parsing_transcript_text(self):
        text = "('这是用户原文', 'zh')"
        self.assertEqual(unpack_typed_final(text), (text, None))
        self.assertEqual(unpack_typed_final({"text": "原文", "language": "zh-CN"}), ("原文", "zh-CN"))
        for value in (("text", 3), ("text", "__import__('os')"), (3, "zh"), ("a", "zh", "extra"), ["a", "zh"]):
            with self.assertRaises(TypeError):
                unpack_typed_final(value)


if __name__ == "__main__":
    unittest.main()
