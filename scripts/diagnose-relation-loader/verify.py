#!/usr/bin/env python3
"""Read complete fixture inventories and verify hot text against the ELF dump."""

from hashlib import sha256
import json
from pathlib import Path
import re
import sys

PREFIX = 'github.com/light-speak/luxo/'
BENCH = 'BenchmarkRelationLoaderGeneration'
LEAVES = [f'{BENCH}/{size}/{mode}-2' for size in ('1', '128') for mode in ('metadata', 'source')]
REQUIRED = [
    'pkg/codegen.BenchmarkRelationLoaderGeneration.func1.1',
    'pkg/codegen.BenchmarkRelationLoaderGeneration.func1.2',
    'pkg/codegen.(*GeneratorContext).collectDataLoaderInputs',
    'pkg/codegen.(*GeneratorContext).analyzeRelations',
    'pkg/codegen.relationRemoteKeyNullable',
    'internal/inferred.ImplicitRelationKeys',
]
FIXTURE = 'pkg/codegen/dataloader_nullable_bench_test.go'
ORIGINAL = '4f58c514dc0f2ac9c310a3142e961c15cbaffa47b2dc745192ea3b30be95729e'
PROJECTION = '9e1e181a3e49000a4543842e444ac9387b0a30503a524eb218cd7f651276957d'


def check(value, description):
    if not value:
        raise ValueError(description)


def json_output(value):
    print(json.dumps(value, sort_keys=True, indent=2))


def inventory(log):
    lines = log.read_text().splitlines()
    names = [line.split()[0] for line in lines if line.startswith('Benchmark')]
    check(lines[-1] == 'PASS' and sorted(names) == sorted(LEAVES), f'incomplete benchmark inventory: {log}')


def projection(source, directory):
    source, directory = source.resolve(strict=True), directory.resolve(strict=True)
    fixture = source / FIXTURE
    tests = {str(path) for path in fixture.parent.glob('*_test.go')}
    replacements = json.loads((directory / 'overlay.json').read_bytes())['Replace']
    proof = json.loads(directory.with_suffix('.projection.json').read_bytes())
    check(tests == set(replacements), 'projection modifies a path outside the existing test files')
    check(replacements[str(fixture)] == str(directory / fixture.name) and
          all(not target for path, target in replacements.items() if path != str(fixture)), 'projection permits additional test fixtures')
    check(sha256(fixture.read_bytes()).hexdigest() == proof['originalSHA256'] == ORIGINAL, 'original fixture changed')
    check(sha256(Path(replacements[str(fixture)]).read_bytes()).hexdigest() == proof['projectionSHA256'] == PROJECTION,
          'projected fixture differs from the verified #156 input')


def instruction_groups(disassembly):
    groups = {}
    for line in disassembly.read_text().splitlines():
        if line.startswith('TEXT '):
            name = line.split()[1].removesuffix('(SB)')
            check(name not in groups, f'duplicate disassembly group: {name}')
            groups[name] = []
        elif line.strip():
            fields = line.split()
            check(len(fields) >= 4 and fields[1].startswith('0x'), f'invalid objdump instruction: {line}')
            groups[name].append((int(fields[1], 16), bytes.fromhex(fields[2]), ' '.join(fields[3:])))
    return groups


def hot_address(address, size, instructions, text, text_start):
    check(size > 0 and instructions and instructions[0][0] == address, 'symbol address does not match disassembly')
    expected = address
    for pc, encoded, _ in instructions:
        offset = pc - text_start
        check(pc == expected and offset >= 0 and encoded and text[offset:offset + len(encoded)] == encoded,
              f'disassembly opcode differs from .text at {pc:#x}')
        expected += len(encoded)
    check(expected <= address + size <= text_start + len(text), 'disassembly outside its symbol range')
    shape = '\n'.join(re.sub(r'(?<![\w])0x[0-9a-f]{6,}\b', '<address>', op) for _, _, op in instructions)
    return {'start': hex(address), 'textOffset': address - text_start, 'size': size, 'mod64': address % 64,
            'mod4096': address % 4096, 'instructions': len(instructions),
            'opcodesSHA256': sha256(text[address - text_start:expected - text_start]).hexdigest(),
            'addressMaskedOperandsSHA256': sha256(shape.encode()).hexdigest()}


def elf(binary, section, evidence):
    nm = {}
    for line in (evidence / 'symbols.txt').read_text().splitlines():
        address, length, kind, name = line.split()
        check(kind in 'Tt' and name not in nm, f'invalid ELF symbol: {line}')
        nm[name] = (int(address, 16), int(length))
    groups = instruction_groups(evidence / 'hot.txt')
    sections = (evidence / 'sections.txt').read_text()
    entries = re.findall(r'^\s*\[\s*\d+\]\s+\.text\s+PROGBITS\s+([0-9a-f]+)\s+([0-9a-f]+)\s+([0-9a-f]+)\s+.*\bAX\b', sections, re.M)
    check(len(entries) == 1, 'ELF .text has no unique executable section')
    start, offset, size = [int(value, 16) for value in entries[0]]
    image, text = binary.read_bytes(), section.read_bytes()
    check(len(text) == size and image[offset:offset + size] == text, 'ELF image and extracted .text differ')
    hot = {}
    for suffix in REQUIRED:
        name = PREFIX + suffix
        check(name in nm and name in groups, f'missing required hot symbol: {name}')
        hot[suffix] = hot_address(*nm[name], groups[name], text, start)
    for closure, callee in (('1', 'collectDataLoaderInputs'), ('2', 'generateDataLoaderFile')):
        name = f'{PREFIX}pkg/codegen.{BENCH}.func1.{closure}'
        check(any(op.startswith('CALL ') and op.endswith(f'.{callee}(SB)') for _, _, op in groups[name]),
              f'relation loader closure does not call {callee}')
    json_output({'elfSHA256': sha256(image).hexdigest(), 'textSHA256': sha256(text).hexdigest(), 'symbols': hot})


if __name__ == '__main__':
    command, *paths = sys.argv[1:]
    if command == 'expected':
        rows = [{'Package': 'pkg/codegen', 'Name': name} for name in sorted(LEAVES)]
        json_output({'Base': rows, 'Head': rows})
    else:
        validators = {'inventory': inventory, 'projection': projection, 'elf': elf}
        validators[command](*map(Path, paths))
