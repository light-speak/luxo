#!/usr/bin/env python3
"""Record the exact test dependencies and overlay before the original compile."""

from hashlib import sha256
import json
import os
from pathlib import Path
import subprocess
import sys


args = sys.argv[1:]
if args[:3] != ['test', '-trimpath', '-c'] or args[-2] != '-o' or not args[-3].startswith('./pkg/'):
    raise SystemExit('unexpected benchrun compiler arguments')
compiler = os.environ['LUXO_WITNESS_REAL_GO']
dest = Path(os.environ['LUXO_WITNESS_PROFILE_LOG'])
name = Path(args[-1]).stem
flags = args[3:-3]
overlays = {}
for flag in flags:
    if not flag.startswith('-overlay='):
        raise SystemExit(f'unexpected benchmark compiler flag: {flag}')
    for old, new in json.loads(Path(flag.split('=', 1)[1]).read_text())['Replace'].items():
        path = Path(old).relative_to(Path.cwd())
        if not path.name.endswith('_test.go'):
            raise SystemExit(f'benchmark overlay changed production source: {path}')
        overlays[str(path)] = sha256(Path(new).read_bytes()).hexdigest() if new else ''
(dest / (name + '.overlay.json')).write_text(json.dumps(overlays, sort_keys=True) + '\n')
template = '{{.ImportPath}}\t{{if .Module}}{{.Module.Path}}\t{{.Module.Version}}{{end}}\t{{join .GoFiles ","}}'
with (dest / (name + '.dependencies.tsv')).open('wb') as stream:
    subprocess.run([compiler, 'list', '-deps', '-test', '-f', template, *flags, args[-3]], stdout=stream, check=True)
os.execv(compiler, [compiler, *args])
