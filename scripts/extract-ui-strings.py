#!/usr/bin/env python3
"""Collect fixed English UI copy for the EdgeStack presentation overlay.

This is an authoring aid, never a runtime translator. Review translations before
adding them to deploy/ui/locales. API identifiers and user data are not inputs.
"""
from collections import defaultdict
from html import unescape
from html.parser import HTMLParser
from pathlib import Path
import json
import re

ROOT = Path(__file__).resolve().parents[1]
APP = ROOT / 'portainer/app'
OUTPUT = ROOT / 'docs/localization'
UI_KEYS = r'(?:label|title|title-text|placeholder|message|description|header|tooltip|buttonText|emptyMessage|confirmText|cancelText|text|helpText|aria-label|ariaLabel)'


def normalize(value):
    value = re.sub(r'\s+', ' ', unescape(value)).strip()
    if not re.search(r'[A-Za-z]', value) or len(value) > 700:
        return None
    if any(s in value for s in ['{{', '}}', '=>', '===', '${', '\\n', 'className', '://']):
        return None
    if re.search(r'[;{}]|(?:^|\s)(?:flex|items-center|col-md-|text-sm|px-\d|ng-if)', value):
        return None
    if not re.match(r'^[A-Za-z0-9(\["\'!]', value):
        return None
    return value


class CopyParser(HTMLParser):
    def __init__(self):
        super().__init__()
        self.texts = []

    def handle_data(self, data):
        self.texts.append(data)

    def handle_starttag(self, tag, attrs):
        for key, value in attrs:
            if re.fullmatch(UI_KEYS, key or '', re.I) and value:
                self.texts.append(value.strip('"\''))


def extract(path):
    source = path.read_text(errors='replace')
    values = []
    if path.suffix == '.html':
        parser = CopyParser()
        parser.feed(source)
        values += parser.texts
    else:
        if path.suffix == '.tsx':
            values += re.findall(r'>([^<>{}]+)<', source)
        literal = r'''(?:"((?:\\.|[^"\\\n])*)"|'((?:\\.|[^'\\\n])*)')'''
        for pattern in [
            rf'\b{UI_KEYS}\s*[:=]\s*{literal}',
            rf'\b(?:notifyError|notifySuccess|error|success|warning)\(\s*{literal}',
        ]:
            for match in re.finditer(pattern, source):
                values.append(next(v for v in match.groups() if v is not None))
    return [v for value in values if (v := normalize(value))]


def main():
    buckets = {name: defaultdict(list) for name in ['docker', 'platform', 'edge']}
    for path in sorted(APP.rglob('*')):
        if path.suffix not in ['.tsx', '.ts', '.js', '.html']:
            continue
        if any(s in path.name for s in ['.test.', '.stories.', '.d.', '.snap']):
            continue
        rel = str(path.relative_to(APP))
        if rel.startswith(('kubernetes/', 'react/kubernetes/', 'azure/', 'react/azure/')):
            continue
        scope = 'docker' if rel.startswith(('docker/', 'react/docker/')) else 'edge' if rel.startswith(('edge/', 'react/edge/')) else 'platform'
        for value in extract(path):
            buckets[scope][value].append(rel)
    OUTPUT.mkdir(parents=True, exist_ok=True)
    for name, values in buckets.items():
        items = [{'text': k, 'sources': v[:2]} for k, v in sorted(values.items())]
        (OUTPUT / f'{name}-source.json').write_text(json.dumps(items, ensure_ascii=False, indent=2) + '\n')
        print(name, 'phrases', len(items), 'words', sum(len(x['text'].split()) for x in items))


if __name__ == '__main__':
    main()
