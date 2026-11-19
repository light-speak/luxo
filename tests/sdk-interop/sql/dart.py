"""Build the SQL consumer from current Dart SDK sources; never fetch packages."""
import os
import json
from pathlib import Path
import shutil
import subprocess
import tempfile
from urllib.parse import urljoin
from evidence import save_evidence


def run(args):
    subprocess.run(args, check=True, timeout=120)


def build_and_verify(source, package, work, evidence):
    dart = os.environ.get('DART', 'dart')
    shutil.copy2(source / 'generate.dart', work / 'generate.dart')
    consumer = 'projection.dart' if os.environ.get('LUXO_SQL_PROJECTION') == '1' else 'consumer.dart'
    if os.environ.get('LUXO_SQL_PRECISION') == '1':
        consumer = 'precision.dart'
    if os.environ.get('LUXO_SQL_RELATIONS') == '1':
        consumer = 'Relations.dart'
    shutil.copy2(source / consumer, work / 'consumer.dart')
    if os.environ.get('LUXO_SQL_INFERRED') == '1':
        return build_inferred(source, package, work, evidence)
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


def build_inferred(source, package, work, evidence):
    dart = os.environ.get('DART', 'dart')
    library = work / 'lib'
    library.mkdir()
    shutil.copy2(work / 'consumer.dart', library / 'main.dart')
    generated = library / 'generated'
    run([dart, '--suppress-analytics', 'run', str(work / 'generate.dart'),
         os.environ['LUXO_SQL_SCHEMA_ENDPOINT'], str(generated)])
    config_path = package / '.dart_tool/package_config.json'
    config = json.loads(config_path.read_text())
    for entry in config['packages']:
        entry['rootUri'] = urljoin(config_path.as_uri(), entry['rootUri'])
    config['packages'].append({'name': 'sql_inferred', 'rootUri': work.as_uri() + '/',
                              'packageUri': 'lib/', 'languageVersion': '3.13'})
    (work / '.dart_tool').mkdir()
    (work / '.dart_tool/package_config.json').write_text(json.dumps(config))
    (work / 'pubspec.yaml').write_text('name: sql_inferred\nenvironment:\n  sdk: ">=3.13.0 <4.0.0"\n')
    for file in ['generate.dart', 'consumer.dart']:
        (work / file).unlink()
    run([dart, '--packages=' + str(work / '.dart_tool/package_config.json'),
         str(package / 'bin/build.dart'),
         '--project', str(work), '--client', 'lib/generated/client.dart',
         '--entry', 'lib/main.dart', '--compile', 'exe', '--output', 'consumer'])
    if os.environ.get('LUXO_SQL_PREPARE_ONLY') == '1':
        shutil.copy2(work / 'consumer', evidence / 'dart-consumer')
    else:
        run([str(work / 'consumer'), os.environ['LUXO_SQL_ENDPOINT'],
             os.environ['LUXO_SQL_CERTIFICATE'], 'inferred'])
    files = [*work.rglob('*'), *package.glob('lib/**/*.dart'),
             package / 'pubspec.yaml', package / 'pubspec.lock', config_path]
    save_evidence(files, evidence, 'dart-inferred', package.parents[2])
    shutil.copytree(generated, evidence / 'dart-generated')
    shutil.copy2(library / 'main.dart', evidence / 'dart-consumer.dart')


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
