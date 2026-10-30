"""Build both TS module formats and their SQL consumers without downloads."""
import os
from pathlib import Path
import shutil
import subprocess
from evidence import save_evidence


def run(args, **kwargs):
    subprocess.run(args, check=True, timeout=120, **kwargs)


def main():
    source = Path(__file__).resolve().parent
    core = source.parents[2]
    evidence = Path(os.environ['LUXO_SQL_SCRATCH']).resolve()
    evidence.relative_to((core / '.tmp').resolve())
    work = evidence / 'ts'
    work.mkdir()
    client = core / 'packages/client'
    plugin = core / 'packages/vite-plugin'
    run(['pnpm', 'build'], cwd=client)
    run([str(plugin / 'node_modules/.bin/tsdown'), 'src/codegen.ts', '--no-config',
         '--no-dts', '--format', 'esm', '--deps.never-bundle', '@luxojs/client',
         '--out-dir', str(work / 'generator')], cwd=plugin)
    run(['node', str(source / 'generate.mjs')])
    consumer = 'projection.mjs' if os.environ.get('LUXO_SQL_PROJECTION') == '1' else 'consumer.mjs'
    shutil.copy2(source / consumer, work / 'consumer.mjs')
    env = dict(os.environ, NODE_EXTRA_CA_CERTS=os.environ['LUXO_SQL_CERTIFICATE'])
    for mode in ('cjs', 'esm'):
        run(['node', str(work / 'consumer.mjs'), mode, os.environ['LUXO_SQL_ENDPOINT']], env=env)
    files = [*work.rglob('*'), *client.glob('src/*.ts'), *plugin.glob('src/*.ts')]
    save_evidence(files, evidence, 'ts', core.parent)


if __name__ == '__main__':
    main()
