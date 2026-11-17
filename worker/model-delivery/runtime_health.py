#!/usr/bin/env python3
"""Container health is based on successful real startup inference."""
import json
import sys
from urllib.request import ProxyHandler, build_opener

try:
    with build_opener(ProxyHandler({})).open('http://127.0.0.1:18622/health', timeout=3) as response:
        healthy = json.loads(response.read(65536)).get('healthy') is True
except Exception:
    healthy = False
sys.exit(0 if healthy else 1)
