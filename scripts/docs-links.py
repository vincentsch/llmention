#!/usr/bin/env python3
"""Check that relative links in the Markdown docs point at files that exist."""
import re
import sys
from pathlib import Path

root = Path(__file__).resolve().parent.parent
link = re.compile(r'\]\(([^)\s]+)\)')
broken = []
for page in [root / 'README.md', *root.glob('*.md'), *(root / 'docs').rglob('*.md'), *(root / 'examples').rglob('*.md')]:
    text = page.read_text(encoding='utf-8')
    for target in link.findall(text):
        if re.match(r'^[a-z]+:', target) or target.startswith('#'):
            continue
        path = target.split('#', 1)[0]
        if path and not (page.parent / path).exists():
            broken.append(f'{page.relative_to(root)}: {target}')
for line in sorted(set(broken)):
    print(line)
sys.exit(1 if broken else 0)
