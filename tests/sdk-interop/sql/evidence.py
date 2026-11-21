"""封存项目内验收输入；外部依赖仅记录哈希，不复制工具链缓存。"""
import hashlib
import io
import json
from pathlib import Path
import tarfile


def save_evidence(files, output, label, root):
    root = root.resolve()
    output = output.resolve()
    output.relative_to(root)
    hashes, external = {}, []
    with tarfile.open(output / f'{label}-inputs.tar.gz', 'w:gz') as archive:
        for path in sorted(set(Path(p).absolute() for p in files)):
            if path.is_dir():
                continue
            data = path.read_bytes()
            hashes[str(path)] = hashlib.sha256(data).hexdigest()
            try:
                path.resolve().relative_to(root)
                member = path.relative_to(root).as_posix()
            except ValueError:
                external.append(str(path))
                continue
            info = tarfile.TarInfo(member)
            info.size = len(data)
            info.mode = 0o644
            archive.addfile(info, io.BytesIO(data))
    (output / f'{label}-sha256.json').write_text(json.dumps(hashes, indent=2))
    (output / f'{label}-inputs.json').write_text(json.dumps({
        'archive': f'{label}-inputs.tar.gz',
        'archive_sha256': hashlib.sha256((output / f'{label}-inputs.tar.gz').read_bytes()).hexdigest(),
        'root': str(root), 'external': external,
        'scope': 'Project inputs retained; external dependencies are hash-only, not a standalone toolchain.',
    }, indent=2))
