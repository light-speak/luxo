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
    if os.environ.get('LUXO_SQL_PRECISION') == '1':
        consumer = source / 'Precision.kt'
    inferred = os.environ.get('LUXO_SQL_INFERRED') == '1'
    if inferred:
        consumer = source / 'Inferred.kt'
    if os.environ.get('LUXO_SQL_RELATIONS') == '1':
        consumer = source / 'Relations.kt'
    classpath = work / 'classpath.txt'
    env = dict(os.environ, LUXO_SQL_CLASSPATH=str(classpath))
    plugin_file = work / 'compiler-plugin.txt'
    if inferred:
        env['LUXO_SQL_COMPILER_PLUGIN'] = str(plugin_file)
    run([str(package / 'gradlew'), '--offline', '--no-daemon', '-Pkotlin.compiler.execution.strategy=in-process',
         '-I', str(source / 'classpath.gradle'), 'sqlConsumerClasspath'], cwd=package, env=env)
    cp = classpath.read_text().strip()
    run(['java', '-cp', cp, 'com.luxo.client.LuxoCodegenCli',
         os.environ['LUXO_SQL_SCHEMA_ENDPOINT'], str(work / 'generated'), 'probe'])
    plugin = Path(plugin_file.read_text().strip()) if inferred else None
    plugin_flags = ['-Xplugin=' + str(plugin)] if plugin else []
    run(['java', '-cp', cp, 'org.jetbrains.kotlin.cli.jvm.K2JVMCompiler',
         '-no-stdlib', '-no-reflect', '-Werror', '-classpath', cp, '-Xverify-ir=error',
         *plugin_flags, '-d', str(work / 'classes'), str(consumer), str(source / 'Trust.kt'),
         *map(str, sorted((work / 'generated').glob('*.kt')))])
    run(['java', '-cp', str(work / 'classes') + os.pathsep + cp, consumer.stem + 'Kt',
         os.environ['LUXO_SQL_ENDPOINT'], os.environ['LUXO_SQL_CERTIFICATE']])
    files = [consumer, source / 'Trust.kt', *work.rglob('*'), *package.glob('src/main/**/*.kt')]
    if plugin:
        files.extend([plugin, source / 'classpath.gradle', source / 'kotlin.py'])
    for item in cp.split(os.pathsep):
        path = Path(item)
        files.extend(path.rglob('*.class') if path.is_dir() else [path])
    save_evidence(files, evidence, 'kotlin', core.parent)


if __name__ == '__main__':
    main()
