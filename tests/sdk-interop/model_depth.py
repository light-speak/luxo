"""Generate Go and Kotlin from live Schema and verify model depth over TLS/WS."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import shutil
import signal
import sys

SOURCE = Path(__file__).resolve().parent
CORE = SOURCE.parents[1]
sys.path.insert(0, str(SOURCE / 'sql'))
from first_use import OwnedProcess, write_json
import runner


def command(args, destination, **kwargs):
    with destination.open('w') as log:
        runner.command(args, log, **kwargs)


def compile_kotlin(classpath, sources, output, log, scratch):
    command(['java', '-Djava.io.tmpdir=' + str(scratch), '-cp', classpath,
             'org.jetbrains.kotlin.cli.jvm.K2JVMCompiler', '-no-stdlib',
             '-no-reflect', '-Werror', '-jvm-target', '1.8', '-classpath', classpath,
             '-d', str(output), *map(str, sources)], log)


def validate_report(report):
    if report.get('assertions') != 320 or len(report.get('phases', [])) != 6:
        raise ValueError('consumer did not finish the complete depth matrix')
    phases = {(phase['protocol'], phase['mode']) for phase in report['phases']}
    if phases != {(protocol, mode) for protocol in ('h1', 'h2', 'ws') for mode in ('JSON', 'BINARY')}:
        raise ValueError('missing transport/mode phase')
    server = report['server']
    expected = {'depthOne': 52, 'depthMany': 48, 'depthEcho': 54, 'depthEchoMany': 36}
    if server.get('invoked') != expected or any(server.get(key) != value for key, value in
            {'active': 0, 'started': 8, 'canceled': 8, 'failed': 8, 'guardedJSON': 12, 'ws': 2, 'malformed': 16}.items()):
        raise ValueError('unexpected invocation/cancellation counts: ' + str(server))
    if server.get('hostile') != {'h1': 6, 'h2': 6, 'ws': 1, 'active': 0, 'malformed': 12, 'recovered': 6}:
        raise ValueError('missing actual hostile JSON delivery and recovery: ' + str(server))


def consume(root, classpath):
    java_scratch = root / 'jvm'
    java_scratch.mkdir()
    driver = root / 'Generate.kt'
    driver.write_text('import com.luxo.client.LuxoCodegen\n'
                      'fun main(args:Array<String>){LuxoCodegen.generate(args[0],'
                      '"model-depth",args[1],"probe",args[1]+"/schema.json")}\n')
    compile_kotlin(classpath, [driver], root / 'generator-classes',
                   root / 'compile-generator.log', java_scratch)
    server = OwnedProcess([str(root / 'server'), '-malformed-responses'], root / 'server-run', interactive=True)
    client = None
    try:
        ready = server.ready(10)
        write_json(root / 'ready.json', ready)
        certificate = root / 'certificate.pem'
        certificate.write_text(ready['certificate'])
        output = root / 'kotlin'
        output.mkdir()
        command(['java', '-Djava.io.tmpdir=' + str(java_scratch), '-cp',
                 str(root / 'generator-classes') + os.pathsep + classpath,
                 'GenerateKt', ready['plain'], str(output)], root / 'generate-sdk.log')
        consumer = CORE / 'packages/kotlin/src/test/fixtures/ModelDepthNetworkConsumer.kt.txt'
        (output / 'Consumer.kt').write_bytes(consumer.read_bytes())
        compile_kotlin(classpath, sorted(output.glob('*.kt')), output / 'classes',
                       root / 'compile-consumer.log', java_scratch)
        client = OwnedProcess(['java', '-Djava.io.tmpdir=' + str(java_scratch), '-cp',
            str(output / 'classes') + os.pathsep + classpath, 'probe.ConsumerKt',
            ready['url'], str(certificate)], root / 'client-run')
        if client.wait(120) != 0:
            raise RuntimeError('network consumer failed; see client-run/stderr.log')
        report = client.last_json()
        write_json(root / 'client-report.json', report)
        validate_report(report)
    finally:
        try:
            if client:
                client.close()
        finally:
            server.close()
            final = server.last_json()
            write_json(root / 'server-report.json', final)
    if final != report['server']:
        raise ValueError('server changed after the final consumer snapshot')


def save_and_clean(root):
    # Preserve bytecode identities before removing only this run's build products.
    artifacts = [root / 'server', root / 'generator-classes', root / 'kotlin/classes', root / 'jvm']
    hashes = {}
    for artifact in artifacts:
        artifact.resolve().relative_to(root.resolve())
        for file in ([artifact] if artifact.is_file() else sorted(artifact.rglob('*'))):
            if file.is_file():
                hashes[str(file.relative_to(root))] = hashlib.sha256(file.read_bytes()).hexdigest()
    write_json(root / 'build-artifacts.json', hashes)
    for artifact in artifacts:
        if artifact.is_dir():
            shutil.rmtree(artifact)
        elif artifact.is_file():
            artifact.unlink()


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--out', type=Path, required=True, help='new directory inside core/.tmp')
    args = parser.parse_args()
    scratch = CORE / '.tmp'
    if scratch.is_symlink():
        parser.error('refusing symlinked project scratch')
    root = args.out.resolve()
    if root == scratch.resolve():
        parser.error('use a dedicated output directory')
    root.relative_to(scratch.resolve())
    root.mkdir(parents=True, exist_ok=False)
    signal.signal(signal.SIGTERM, runner.interrupted)
    package = CORE / 'packages/kotlin'
    try:
        generated = root / 'generated'
        generated.mkdir()
        command(['go', 'run', './tests/sdk-interop', '-model-depth', '-out', str(generated)],
                root / 'generate-go.log', cwd=CORE)
        command(['go', 'build', '-o', str(root / 'server'), str(generated)],
                root / 'build-go.log', cwd=CORE)
        env = dict(os.environ, LUXO_SQL_CLASSPATH=str(root / 'classpath.txt'))
        command([str(package / 'gradlew'), '--offline', '--no-daemon', '--no-watch-fs',
                 '-Pkotlin.compiler.execution.strategy=in-process', '-I',
                 str(SOURCE / 'sql/classpath.gradle'), 'sqlConsumerClasspath'],
                root / 'gradle-classpath.log', cwd=package, env=env)
        consume(root, (root / 'classpath.txt').read_text().strip())
    finally:
        save_and_clean(root)
    print(json.dumps({'status': 'passed', 'evidence': str(root)}))


if __name__ == '__main__':
    main()
