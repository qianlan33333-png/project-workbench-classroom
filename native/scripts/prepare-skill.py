#!/usr/bin/env python3
"""Build a self-contained standard Skill for a Host without filesystem tools.
Source references remain separately editable. Only explicitly linked Markdown
references are included; no model filesystem permission or loader is added.
"""
import argparse
from pathlib import Path
import re
import shutil


def prepare(source, target):
    source, target = Path(source).resolve(), Path(target).resolve()
    if source == target or source in target.parents or target in source.parents:
        raise ValueError('source and target must be separate directories')
    main = (source / 'SKILL.md').read_text()
    refs = []
    for name in dict.fromkeys(re.findall(r'\]\((references/[^\s)]+\.md)\)', main)):
        path = (source / name).resolve()
        if not path.is_relative_to(source / 'references'):
            raise ValueError('reference outside Skill')
        refs.append((name, path.read_text()))
    body = main + ''.join('\n\n---\n\n## 随专家加载的参考：' + name + '\n\n' + text for name, text in refs)
    if len(body.encode()) > 48 * 1024:
        raise ValueError('Skill exceeds 48 KiB package budget')
    target.mkdir(parents=True, exist_ok=True)
    (target / 'SKILL.md').write_text(body)
    for name, text in refs:
        destination = target / name
        destination.parent.mkdir(parents=True, exist_ok=True)
        destination.write_text(text)
    if (source / 'assets').is_dir():
        shutil.copytree(source / 'assets', target / 'assets', dirs_exist_ok=True)


if __name__ == '__main__':
    parser = argparse.ArgumentParser()
    parser.add_argument('source', type=Path)
    parser.add_argument('target', type=Path)
    args = parser.parse_args()
    prepare(args.source, args.target)
