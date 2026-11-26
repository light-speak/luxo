"""Build benchmark-only overlays retaining the original fixture files."""

from hashlib import sha256
from pathlib import Path
import json


FIXTURES = {
    'identity': ('identity_reader_bench_test.go', '20d45a1c2a71419d6c2c44e33b49e691cf15f3522f6e1041541dbce5097a3c5f',
                 'BenchmarkIdentityConditionValidity'),
    'handler': ('handler_panic_bench_test.go', '01122e4a09e5c6d6a20018f47566fb801ad4888db504fd9d2c932be56146e302',
                'BenchmarkHandlerPanicFormatting'),
    'websocket': ('websocket_subscriptions_bench_test.go', '5abe6b7846bb541fa73512dd924d2eec3b4a66c1ed64f5839ee658f43492b584',
                  'BenchmarkWSConnectionRegistrations'),
}


def write_overlay(package, destination, profile):
    keep, expected, _ = FIXTURES[profile]
    source = package / keep
    files = sorted(package.glob('*_test.go'))
    if source not in files or any(not p.is_file() or p.is_symlink() for p in files):
        raise RuntimeError(f'{profile}: missing fixture or irregular test input')
    if sha256(source.read_bytes()).hexdigest() != expected:
        raise RuntimeError(f'{profile}: original benchmark fixture changed')
    replacements = {str(path): '' for path in files if path != source}
    destination.write_text(json.dumps({'Replace': replacements}, sort_keys=True) + '\n')
    return source


def selected_profiles(old_profiles):
    inventory = {profile: [] for profile in FIXTURES}
    for item in sum(old_profiles.values(), []):
        benchmark = item.split('/')[0]
        targets = [profile for profile, (_, _, root) in FIXTURES.items() if root == benchmark]
        if len(targets) != 1:
            raise RuntimeError(f'cannot map measured leaf {item} to a fixture')
        inventory[targets[0]].append(item)
    if {key: len(items) for key, items in inventory.items()} != {'identity': 12, 'handler': 1, 'websocket': 1}:
        raise RuntimeError('changed three-family measurement inventory')
    return inventory


if __name__ == '__main__':
    import sys

    _, package, destination, profile = sys.argv
    write_overlay(Path(package).resolve(strict=True), Path(destination), profile)
