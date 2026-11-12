#!/usr/bin/env python3
"""Pack the deployed public frontend and local simulator into one offline HTML.

Only a public asset directory is accepted. No /data files, real API responses,
browser cookies or access credentials are read or embedded.
"""
from pathlib import Path
import argparse
import base64
import hashlib
import json
import mimetypes
import re

ROOT = Path(__file__).resolve().parents[1]


def data_url(path):
    mime = mimetypes.guess_type(path.name)[0] or 'application/octet-stream'
    if path.suffix == '.js':
        mime = 'application/javascript'
    return f'data:{mime};base64,' + base64.b64encode(path.read_bytes()).decode()


def safe_script(code):
    code = re.sub(r'\n?//# sourceMappingURL=.*', '', code)
    return re.sub(r'</script', r'<\\/script', code, flags=re.I)


def build(public, output):
    html = (public / 'index.html').read_text()
    html = re.sub(r'<!--\[if.*?<!\[endif\]-->', '', html, flags=re.S)
    scripts = re.findall(r'<script\b[^>]*\bsrc="([^"]+)"[^>]*></script>', html)
    styles = re.findall(r'<link\b[^>]*href="([^"]+\.css)"[^>]*>', html)
    main_names = {Path(p).name for p in scripts}
    assets = {}
    hashes = {}
    for p in sorted(public.rglob('*')):
        if not p.is_file() or p.name.endswith(('.map', '.LICENSE.txt')):
            continue
        rel = str(p.relative_to(public))
        if rel in ['index.html', 'timeout.html'] or p.name in main_names or p.suffix == '.css':
            continue
        if p.name.startswith('edgestack-ui.'):
            continue
        assets[p.name] = data_url(p)
        assets[rel] = assets[p.name]
        hashes[rel] = hashlib.sha256(p.read_bytes()).hexdigest()
    # Remap images referenced by the localization overlay too.
    def css_text(css):
        def substitute(m):
            raw = m.group(1).strip('\"\'')
            if raw.startswith(('data:', 'blob:', '#')):
                return m.group(0)
            file, _, anchor = raw.partition('#')
            asset = assets.get(Path(file).name)
            return 'url("' + asset + ('#' + anchor if anchor else '') + '")' if asset else m.group(0)
        return re.sub(r'url\(([^)]+)\)', substitute, css)

    for source in styles:
        css = css_text((public / source).read_text())
        pattern = r'<link\b[^>]*href="' + re.escape(source) + r'"[^>]*>'
        html = re.sub(pattern, lambda _: '<style>\n' + css + '\n</style>', html, count=1)
    for source in scripts:
        path = public / source
        code = path.read_text()
        if source.startswith('runtime.'):
            if 'u.p=e' not in code:
                raise ValueError('Unexpected upstream webpack runtime; inspect before repacking.')
            code = code.replace('u.p=e', 'u.p="file:///__edgestack_demo__/"', 1)
        # Classic inline scripts need an original-looking source for i18n assets.
        prefix = 'Object.defineProperty(document.currentScript,"src",{value:' + json.dumps('file:///__edgestack_demo__/' + source) + '});\n'
        pattern = r'<script\b[^>]*\bsrc="' + re.escape(source) + r'"[^>]*></script>'
        html = re.sub(pattern, lambda _: '<script>\n' + prefix + safe_script(code) + '\n</script>', html, count=1)
        hashes[source] = hashlib.sha256(path.read_bytes()).hexdigest()
    # Inline static icon/image links still present in the HTML shell.
    for source, url in sorted(assets.items(), key=lambda kv: -len(kv[0])):
        html = html.replace('href="' + source + '"', 'href="' + url + '"')
        html = html.replace('src="' + source + '"', 'src="' + url + '"')
    icon = assets['edgestack-icon.svg']
    html = re.sub(r'<link\b[^>]*\brel="icon"[^>]*>', '<link rel="icon" href="' + icon + '">', html)
    html = re.sub(r'<meta\s+name="msapplication-config"[^>]*>', '', html)
    csp = "default-src 'none'; script-src 'unsafe-inline' 'unsafe-eval' blob: data:; style-src 'unsafe-inline' blob: data:; img-src blob: data:; font-src blob: data:; connect-src 'none'; worker-src blob: data:; frame-src blob: data:; base-uri file:; form-action 'none'"
    boot = (
        '<meta charset="utf-8"><meta http-equiv="Content-Security-Policy" content="' + csp + '">'
        '<style>\n' + (ROOT / 'demo/conference/demo.css').read_text() + '\n</style>'
        '<script>window.__EDGESTACK_DEMO_ASSETS__=' + json.dumps(assets, separators=(',', ':')) + ';\n' +
        safe_script((ROOT / 'demo/conference/demo-runtime.js').read_text()) + '\n</script>'
    )
    html = html.replace('<head>', '<head>' + boot, 1)
    notices = {'Portainer CE': (ROOT / 'portainer/LICENSE').read_text()}
    for path in public.glob('*.LICENSE.txt'):
        notices[path.name] = path.read_text()
    license_data = safe_script(json.dumps(notices, ensure_ascii=False))
    html = html.replace('</body>', '<script type="application/json" id="edgestack-open-source-notices">' + license_data + '</script></body>')
    html = html.replace('<title>EdgeStack.io</title>', '<title>EdgeStack.io · 会议演示</title>')
    output.parent.mkdir(parents=True, exist_ok=True)
    output.write_text(html)
    info = {'file': output.name, 'size_bytes': output.stat().st_size, 'sha256': hashlib.sha256(output.read_bytes()).hexdigest(), 'upstream': 'Portainer CE 2.45.3', 'offline': True, 'public_source_sha256': hashes}
    print(json.dumps({k: v for k, v in info.items() if k != 'public_source_sha256'}, ensure_ascii=False, indent=2))
    (output.parent / '演示构建记录.json').write_text(json.dumps(info, ensure_ascii=False, indent=2) + '\n')


if __name__ == '__main__':
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument('--public', type=Path, required=True)
    p.add_argument('--output', type=Path, required=True)
    args = p.parse_args()
    build(args.public, args.output)
