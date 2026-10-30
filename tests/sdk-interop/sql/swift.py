"""Build the macOS SQL consumer and runtime from current Swift sources."""
import os
from pathlib import Path
import subprocess
from evidence import save_evidence


def run(args):
    subprocess.run(args, check=True, timeout=180)


def main():
    source = Path(__file__).resolve().parent
    core = source.parents[2]
    evidence = Path(os.environ['LUXO_SQL_SCRATCH']).resolve()
    evidence.relative_to((core / '.tmp').resolve())
    sdk = Path(os.environ.get('LUXO_SWIFT_PATH', str(core.parent / 'luxo-swift'))).resolve()
    sources = sorted((sdk / 'Sources/LuxoClient').glob('*.swift'))
    if not sources:
        raise RuntimeError('Swift SDK sources are required')
    work = evidence / 'swift'
    work.mkdir()
    consumer = source / ('Projection.swift' if os.environ.get('LUXO_SQL_PROJECTION') == '1' else 'Consumer.swift')
    runtime = work / 'runtime'
    runtime.mkdir()
    flags = ['-O', '-swift-version', '6', '-strict-concurrency=complete',
             '-warnings-as-errors', '-parse-as-library']
    run(['swiftc', *flags, '-enable-testing', '-emit-library', '-emit-module',
         '-module-name', 'LuxoClient', '-emit-module-path', str(runtime / 'LuxoClient.swiftmodule'),
         *map(str, sources), '-o', str(runtime / 'libLuxoClient.dylib')])
    flags += ['-I', str(runtime), '-L', str(runtime), '-lLuxoClient',
              '-Xlinker', '-rpath', '-Xlinker', str(runtime)]
    generator = work / 'generator'
    run(['swiftc', *flags, str(source / 'Generate.swift'), '-o', str(generator)])
    run([str(generator), os.environ['LUXO_SQL_SCHEMA_ENDPOINT'], str(work / 'generated')])
    run(['swiftc', *flags, str(consumer),
         *map(str, sorted((work / 'generated').glob('*.swift'))), '-o', str(work / 'consumer')])
    run([str(work / 'consumer'), os.environ['LUXO_SQL_ENDPOINT'], os.environ['LUXO_SQL_CERTIFICATE']])
    files = [*sources, consumer, source / 'Generate.swift', *work.rglob('*')]
    save_evidence(files, evidence, 'swift', core.parent)


if __name__ == '__main__':
    main()
