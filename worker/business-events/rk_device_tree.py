"""Vendor-module-local path compatibility for a real read-only RK3588 device tree.

Docker masks /sys/firmware, making its /proc/device-tree symlink inaccessible.
This maps only the SDK's fixed compatible filename to a separately mounted
physical sysfs file. SDK checks/detection functions are never replaced.
"""
from __future__ import annotations

import builtins
import hashlib
import os
from pathlib import Path
import re
import stat
import sys

LEGACY_COMPATIBLE = "/proc/device-tree/compatible"
DEFAULT_COMPATIBLE = "/run/edgestack-device-tree/compatible"


def unescape(value):
    return re.sub(r"\\([0-7]{3})", lambda match: chr(int(match.group(1), 8)), value)


def validate_source(filename, mountinfo="/proc/self/mountinfo"):
    try:
        path = Path(filename)
        if not path.is_absolute():
            raise ValueError("rknn_device_tree_requires_absolute_path")
        path = path.resolve(strict=True)
        if not stat.S_ISREG(path.stat().st_mode):
            raise ValueError("rknn_device_tree_requires_regular_compatible_file")
        selected = None
        for line in Path(mountinfo).read_text().splitlines():
            before, after = line.split(" - ", 1)
            fields, filesystem = before.split(), after.split()[0]
            root, target = unescape(fields[3]), Path(unescape(fields[4]))
            if (path == target or target in path.parents) and (selected is None or len(str(target)) > len(str(selected[0]))):
                selected = (target, root, set(fields[5].split(",")), filesystem)
        if selected is None or selected[3] != "sysfs" or "ro" not in selected[2]:
            raise ValueError("rknn_device_tree_requires_readonly_sysfs_mount")
        if selected[1] != "/firmware/devicetree/base" and not selected[1].startswith("/firmware/devicetree/base/"):
            raise ValueError("rknn_device_tree_requires_physical_device_tree")
        with path.open("rb") as stream:
            content = stream.read(4097)
        if len(content) > 4096 or b"rockchip,rk3588" not in content.split(b"\x00"):
            raise ValueError("rknn_device_tree_chip_mismatch")
        return {"path": str(path), "sha256": hashlib.sha256(content).hexdigest()}
    except OSError:
        raise ValueError("rknn_device_tree_source_unavailable") from None


def remap(path, actual):
    try:
        candidate = os.fsdecode(os.fspath(path))
    except TypeError:
        return path
    return actual if candidate == LEGACY_COMPATIBLE else path


class SDKPathView:
    def __init__(self, original, actual):
        self.original, self.actual = original, actual

    def __getattr__(self, name):
        original = getattr(self.original, name)
        if name in {"exists", "lexists", "isfile", "isdir", "islink", "getsize", "getmtime", "getctime"}:
            def call(path, *args, **kwargs):
                return original(remap(path, self.actual), *args, **kwargs)
            return call
        return original


class SDKOSView:
    def __init__(self, original, actual):
        self.original, self.actual = original, actual
        self.path = SDKPathView(original.path, actual)

    def __getattr__(self, name):
        original = getattr(self.original, name)
        if name in {"access", "stat", "lstat", "open"}:
            def call(path, *args, **kwargs):
                return original(remap(path, self.actual), *args, **kwargs)
            return call
        return original


def install(filename=DEFAULT_COMPATIBLE, *, sdk_module=None, mountinfo="/proc/self/mountinfo"):
    receipt = validate_source(filename, mountinfo)
    if sdk_module is None:
        import rknnlite.api.rknn_runtime as sdk_module
    original_os = getattr(sdk_module, "_edgestack_original_os", sdk_module.os)
    original_open = getattr(sdk_module, "_edgestack_original_open", getattr(sdk_module, "open", builtins.open))

    def sdk_open(path, *args, **kwargs):
        return original_open(remap(path, receipt["path"]), *args, **kwargs)

    # Rebind only globals belonging to the vendor extension module. Global os,
    # os.path and builtins remain unchanged for the rest of the application.
    sdk_module._edgestack_original_os, sdk_module._edgestack_original_open = original_os, original_open
    sdk_module.os = SDKOSView(original_os, receipt["path"])
    sdk_module.open = sdk_open
    # Cython's _is_npu_self caches a builtin-open reference, so its reads bypass
    # a module-level open alias. Point its own filename constant at the same
    # validated physical file; retain the original SoC parser and target logic.
    if hasattr(sdk_module, "DEVICE_COMPATIBLE_NODE"):
        sdk_module.DEVICE_COMPATIBLE_NODE = receipt["path"]
    sys.stderr.write("edgestack_rknn_device_tree_source=" + receipt["path"] + " sha256:" + receipt["sha256"] + "\n")
    return receipt
