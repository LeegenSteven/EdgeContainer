#!/usr/bin/env python3
"""Build a dependency-free Chinese/EdgeStack overlay for an original index.html.

The original JavaScript bundles and backend are kept byte-for-byte unchanged.
Input must come from the pinned Server image, never from an authenticated page.
"""
import argparse
import hashlib
import json
from pathlib import Path
import re

ROOT = Path(__file__).resolve().parents[1]
UI = ROOT / 'deploy/ui'


def digest(data):
    return hashlib.sha256(data).hexdigest()


def load_copy():
    result = {}
    paths = sorted((UI / 'locales').glob('zh-CN.*.json'))
    overrides = UI / 'locales/zh-CN.overrides.json'
    paths = [p for p in paths if p != overrides] + [overrides]
    for path in paths:
        data = json.loads(path.read_text())
        for key, value in data.items():
            if not isinstance(value, str) or not key.strip() or key != ' '.join(key.split()):
                raise ValueError(f'Invalid translation key/value in {path}: {key!r}')
            result[key] = value
    return dict(sorted(result.items()))


def build(index, output):
    source_index = index.read_text()
    if 'data-edgestack-ui' in source_index:
        raise ValueError('Use the saved upstream index.html, not an already modified page.')
    if 'data-edition="CE"' not in source_index or not re.search(r'<title>Portainer</title>', source_index):
        raise ValueError('Expected the original compiled Portainer CE index.html.')
    copy = load_copy()
    template = (UI / 'src/edgestack-ui.js').read_text()
    css = (UI / 'src/edgestack-ui.css').read_bytes()
    payload = json.dumps(copy, ensure_ascii=False, separators=(',', ':')).replace('</', '<\\/')
    seed = template.encode() + payload.encode() + css
    for name in ['edgestack-logo.svg', 'edgestack-icon.svg']:
        seed += (UI / 'src' / name).read_bytes()
    version = digest(seed)[:12]
    javascript = template.replace('__EDGESTACK_TRANSLATIONS__', payload).replace('__EDGESTACK_VERSION__', version)
    static = output / 'edgestack'
    static.mkdir(parents=True, exist_ok=True)
    js_name, css_name = f'edgestack-ui.{version}.js', f'edgestack-ui.{version}.css'
    (static / js_name).write_text(javascript)
    (static / css_name).write_bytes(css)
    for name in ['edgestack-logo.svg', 'edgestack-icon.svg']:
        (static / name).write_bytes((UI / 'src' / name).read_bytes())

    html = re.sub(r'<html\s+lang="[^"]*"', '<html lang="zh-CN"', source_index, count=1)
    html = html.replace('<title>Portainer</title>', '<title>EdgeStack.io</title>')
    html = html.replace('content="Portainer.io"', 'content="EdgeStack.io (based on Portainer)"')
    html = html.replace('Loading Portainer...', '正在加载 EdgeStack.io…')
    html = re.sub(r'<link\b[^>]*\brel="(?:icon|shortcut icon|mask-icon|apple-touch-icon)"[^>]*>', '', html)
    # Defer runs before the upstream entry bundle, without delaying HTML parsing.
    script_tag = f'<script defer src="edgestack/{js_name}" data-edgestack-ui="{version}"></script>'
    html = html.replace('<head>', '<head>' + script_tag, 1)
    extras = (
        f'<link rel="stylesheet" href="edgestack/{css_name}" data-edgestack-ui="{version}">'
        f'<link rel="icon" type="image/svg+xml" href="edgestack/edgestack-icon.svg?v={version}">'
        '<meta name="application-name" content="EdgeStack.io">'
    )
    html = html.replace('</head>', extras + '</head>', 1)
    (output / 'index.html').write_text(html)
    lock = json.loads((ROOT / 'deploy/images.lock.json').read_text())
    manifest = {
        'version': version,
        'locale': 'zh-CN',
        'brand': 'EdgeStack.io',
        'phrase_count': len(copy),
        'server_image': lock['images']['server']['reference'],
        'original_index_sha256': digest(source_index.encode()),
        'files': {str(p.relative_to(output)): digest(p.read_bytes()) for p in sorted(output.rglob('*')) if p.is_file() and p.name != 'manifest.json'},
    }
    (output / 'manifest.json').write_text(json.dumps(manifest, ensure_ascii=False, indent=2) + '\n')
    print(json.dumps(manifest, ensure_ascii=False, indent=2))


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--index', type=Path, required=True)
    parser.add_argument('--output', type=Path, required=True)
    args = parser.parse_args()
    if args.output.exists() and any(args.output.iterdir()):
        parser.error('Output directory must be empty; use a new release directory.')
    build(args.index, args.output)
