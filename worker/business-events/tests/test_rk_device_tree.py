import builtins
import os
from pathlib import Path
import tempfile
from types import ModuleType
import unittest

import sys
sys.path.insert(0, str(Path(__file__).resolve().parents[1]))
from rk_device_tree import install, LEGACY_COMPATIBLE, validate_source


class DeviceTreeTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.root = Path(self.temp.name).resolve()
        self.tree = self.root / "device-tree"
        self.tree.mkdir()
        self.compatible = self.tree / "compatible"
        self.content = b"rockchip,aibox-pro-3588jd4\x00rockchip,rk3588\x00"
        self.compatible.write_bytes(self.content)
        self.mountinfo = self.root / "mountinfo"
        self.write_mount()
        self.sdk = ModuleType("fixture_vendor_sdk")
        self.sdk.os = os
        self.sdk.DEVICE_COMPATIBLE_NODE = LEGACY_COMPATIBLE
        self.sdk.NPU_NODE = "/original/kernel/npu-node"

    def tearDown(self):
        self.temp.cleanup()

    def write_mount(self, mode="ro", filesystem="sysfs", origin="/firmware/devicetree/base"):
        self.mountinfo.write_text(f"42 1 0:21 {origin} {self.tree} {mode},nosuid,nodev,noexec - {filesystem} sysfs rw\n")

    def test_sdk_only_alias_reads_actual_file_and_preserves_global_functions(self):
        original_exists, original_open = os.path.exists, builtins.open
        marker = object()
        self.sdk._check_container = marker
        receipt = install(self.compatible, sdk_module=self.sdk, mountinfo=self.mountinfo)
        self.assertEqual(receipt["path"], str(self.compatible))
        self.assertIs(self.sdk._check_container, marker)
        self.assertIs(os.path.exists, original_exists)
        self.assertIs(builtins.open, original_open)
        self.assertIsNot(self.sdk.os, os)
        self.assertEqual(self.sdk.DEVICE_COMPATIBLE_NODE, str(self.compatible))
        self.assertEqual(self.sdk.NPU_NODE, "/original/kernel/npu-node")
        # Mirrors Cython's cached builtin-open path, which bypasses sdk.open.
        with original_open(self.sdk.DEVICE_COMPATIBLE_NODE, "rb") as stream:
            self.assertEqual(stream.read(), self.content)
        self.assertTrue(self.sdk.os.path.exists(LEGACY_COMPATIBLE))
        with self.sdk.open(LEGACY_COMPATIBLE, "rb") as stream:
            self.assertEqual(stream.read(), self.content)
        fd = self.sdk.os.open(LEGACY_COMPATIBLE, os.O_RDONLY)
        try:
            self.assertEqual(os.read(fd, 4096), self.content)
        finally:
            os.close(fd)
        # Other checks, including missing physical devices, retain real results.
        missing_device = str(self.root / "missing-render-device")
        self.assertFalse(self.sdk.os.path.exists(missing_device))
        self.assertFalse(self.sdk.os.access(missing_device, os.R_OK))

    def test_nonmatching_or_fabricated_prefix_chip_is_rejected_before_install(self):
        for content in (b"rockchip,rk3576\x00", b"rockchip,rk3588-fake\x00", b""):
            self.compatible.write_bytes(content)
            with self.assertRaisesRegex(ValueError, "chip_mismatch"):
                install(self.compatible, sdk_module=self.sdk, mountinfo=self.mountinfo)
            self.assertIs(self.sdk.os, os)
            self.assertFalse(hasattr(self.sdk, "open"))

    def test_missing_or_writable_source_fails_closed(self):
        self.compatible.unlink()
        with self.assertRaisesRegex(ValueError, "source_unavailable"):
            install(self.compatible, sdk_module=self.sdk, mountinfo=self.mountinfo)
        self.compatible.write_bytes(self.content)
        self.write_mount(mode="rw")
        with self.assertRaisesRegex(ValueError, "readonly_sysfs"):
            install(self.compatible, sdk_module=self.sdk, mountinfo=self.mountinfo)
        self.assertIs(self.sdk.os, os)

    def test_regular_copied_file_and_wrong_sysfs_subtree_are_rejected(self):
        self.write_mount(filesystem="ext4")
        with self.assertRaisesRegex(ValueError, "readonly_sysfs"):
            validate_source(self.compatible, self.mountinfo)
        self.write_mount(origin="/unrelated-sysfs-tree")
        with self.assertRaisesRegex(ValueError, "physical_device_tree"):
            validate_source(self.compatible, self.mountinfo)

    def test_more_specific_writable_mount_cannot_be_hidden_by_readonly_parent(self):
        with self.mountinfo.open("a") as stream:
            stream.write(f"43 42 0:21 /firmware/devicetree/base/compatible {self.compatible} rw - sysfs sysfs rw\n")
        with self.assertRaisesRegex(ValueError, "readonly_sysfs"):
            validate_source(self.compatible, self.mountinfo)


if __name__ == "__main__":
    unittest.main()
