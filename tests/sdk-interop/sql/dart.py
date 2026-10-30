"""Build the SQL consumer from current Dart SDK sources; never fetch packages."""
import os
from pathlib import Path
import shutil
import subprocess
import tempfile
from evidence import save_evidence


def run(args):
    subprocess.run(args, check=True, timeout=120)


def build_and_verify(source, package, work, evidence):
    dart = os.environ.get('DART', 'dart')
    shutil.copy2(source / 'generate.dart', work / 'generate.dart')
    consumer = 'projection.dart' if os.environ.get('LUXO_SQL_PROJECTION') == '1' else 'consumer.dart'
    shutil.copy2(source / consumer, work / 'consumer.dart')
    run([dart, '--suppress-analytics', 'run', str(work / 'generate.dart'),
         os.environ['LUXO_SQL_SCHEMA_ENDPOINT'], str(work / 'generated')])
    run([dart, '--suppress-analytics', 'analyze', str(work)])
    run([dart, '--suppress-analytics', 'compile', 'exe', str(work / 'consumer.dart'),
         '-o', str(work / 'consumer')])
    run([str(work / 'consumer'), os.environ['LUXO_SQL_ENDPOINT'],
         os.environ['LUXO_SQL_CERTIFICATE']])
    files = [*work.rglob('*'), *package.glob('lib/**/*.dart'),
             package / 'pubspec.yaml', package / 'pubspec.lock',
             package / '.dart_tool/package_config.json']
    save_evidence(files, evidence, 'dart', package.parents[2])
    shutil.copytree(work / 'generated', evidence / 'dart-generated')
    shutil.copy2(work / 'consumer.dart', evidence / 'dart-consumer.dart')


def main():
    source = Path(__file__).resolve().parent
    core = source.parents[2]
    evidence = Path(os.environ['LUXO_SQL_SCRATCH']).resolve()
    evidence.relative_to((core / '.tmp').resolve())
    package = core / 'packages/dart'
    if not (package / '.dart_tool/package_config.json').is_file():
        raise RuntimeError('prepare the existing Dart package dependencies first')
    scratch = package / '.tmp'
    if scratch.is_symlink():
        raise RuntimeError('refusing symlinked package scratch')
    scratch.mkdir(exist_ok=True)
    work = Path(tempfile.mkdtemp(prefix='sql-consumer-', dir=scratch))
    try:
        build_and_verify(source, package, work, evidence)
    finally:
        if work.resolve().parent != scratch.resolve():
            raise RuntimeError('refusing cleanup outside package scratch')
        shutil.rmtree(work)


if __name__ == '__main__':
    main()
