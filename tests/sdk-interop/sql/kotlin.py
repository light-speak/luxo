"""Build the Kotlin SQL consumer against the current local SDK, offline."""
import os
from pathlib import Path
import subprocess
from evidence import save_evidence


def run(args, **kwargs):
    subprocess.run(args, check=True, timeout=180, **kwargs)


def main():
    source = Path(__file__).resolve().parent
    core = source.parents[2]
    evidence = Path(os.environ['LUXO_SQL_SCRATCH']).resolve()
    evidence.relative_to((core / '.tmp').resolve())
    package = core / 'packages/kotlin'
    work = evidence / 'kotlin'
    work.mkdir()
    consumer = source / ('Projection.kt' if os.environ.get('LUXO_SQL_PROJECTION') == '1' else 'Consumer.kt')
    classpath = work / 'classpath.txt'
    env = dict(os.environ, LUXO_SQL_CLASSPATH=str(classpath))
    run([str(package / 'gradlew'), '--offline', '-Pkotlin.compiler.execution.strategy=in-process',
         '-I', str(source / 'classpath.gradle'), 'sqlConsumerClasspath'], cwd=package, env=env)
    cp = classpath.read_text().strip()
    run(['java', '-cp', cp, 'com.luxo.client.LuxoCodegenCli',
         os.environ['LUXO_SQL_SCHEMA_ENDPOINT'], str(work / 'generated'), 'probe'])
    run(['java', '-cp', cp, 'org.jetbrains.kotlin.cli.jvm.K2JVMCompiler',
         '-no-stdlib', '-no-reflect', '-Werror', '-classpath', cp, '-Xverify-ir=error',
         '-d', str(work / 'classes'), str(consumer),
         *map(str, sorted((work / 'generated').glob('*.kt')))])
    run(['java', '-cp', str(work / 'classes') + os.pathsep + cp, consumer.stem + 'Kt',
         os.environ['LUXO_SQL_ENDPOINT'], os.environ['LUXO_SQL_CERTIFICATE']])
    files = [consumer, *work.rglob('*'), *package.glob('src/main/**/*.kt')]
    for item in cp.split(os.pathsep):
        path = Path(item)
        files.extend(path.rglob('*.class') if path.is_dir() else [path])
    save_evidence(files, evidence, 'kotlin', core.parent)


if __name__ == '__main__':
    main()
