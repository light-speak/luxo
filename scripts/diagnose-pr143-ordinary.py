#!/usr/bin/env python3
"""Exact-input ordinary-build identity experiment; never waive the stock gate."""
from collections import Counter
from hashlib import sha256
from pathlib import Path
from struct import Struct
import difflib
import json
import os
import re
import stat
import subprocess
import sys

CPU = "Intel(R) Xeon(R) 6975P-C"
PREFIX = "github.com/light-speak/luxo/pkg/lux/luvia."
ARMS = ("base", "head", "candidate-base", "candidate-head")
COMPARISONS = (("aa", "base", "base"), ("bb", "head", "head"),
               ("cc", "candidate-base", "candidate-base"), ("dd", "candidate-head", "candidate-head"),
               ("ab", "base", "head"), ("ac", "base", "candidate-head"),
               ("cd", "candidate-base", "candidate-head"))
HASHES = {'base': {'luvia.go': 'bfca1e822080fbc78f92415f2e3973df57e753a2a384e9b632af7d7955d2898f', 'middleware.go': '16104944640bd72d4e15e4fbae6c4920b8d005cc5c0cf11ca0d19ba5b9f88374', 'middleware_test.go': 'a284f6673c86a90ed916099ac82a6ffb0e9b190161c946989957839252b07034', 'go_inputs': 'b1d04a4be3ec9a90b7a1c0222f18a826f45c7f2640be47f6fe3150e298212e41'}, 'head': {'luvia.go': '9ff400bb74b75d1b8e34dfbf12fe07829f61c5c8cec32c626fdb4acd1645d794', 'middleware.go': '16104944640bd72d4e15e4fbae6c4920b8d005cc5c0cf11ca0d19ba5b9f88374', 'middleware_test.go': 'a284f6673c86a90ed916099ac82a6ffb0e9b190161c946989957839252b07034', 'go_inputs': 'c5021566fe04843f7f3864ce3b1433b74ffdbe6bf88d2770635753fb825410cd'}}

def executable_bytes(data, offset, length):
    assert 0 <= offset < len(data) and 0 < length <= len(data) - offset, (offset, length, len(data))
    return sha256(data[offset:offset + length]).hexdigest()

def executable_fingerprints(path):
    data = path.read_bytes()
    header = Struct("<16sHHIQQQIHHHHHH")
    program = Struct("<IIQQQQQQ")
    section = Struct("<IIQQQQIIQQ")
    ident, kind, machine, version, entry, phoff, shoff, _, _, phsize, phnum, shsize, shnum, strings = header.unpack_from(data)
    assert ident[:7] == b"\x7fELF\x02\x01\x01" and kind in (2, 3) and machine == 62 and version == 1
    assert phsize == program.size and shsize == section.size and 0 < strings < shnum
    segments = []
    for index in range(phnum):
        tag, flags, offset, address, _, length, resident, alignment = program.unpack_from(data, phoff + index * phsize)
        if tag == 1 and flags & 1:
            segments.append({"offset": offset, "address": address, "filesz": length, "memsz": resident,
                             "flags": flags, "alignment": alignment, "sha256": executable_bytes(data, offset, length)})
    assert segments
    sections = [section.unpack_from(data, shoff + index * shsize) for index in range(shnum)]
    start, size = sections[strings][4:6]
    names = data[start:start + size]
    text = [entry for entry in sections if names[entry[0]:].split(b"\0", 1)[0] == b".text"]
    assert len(text) == 1
    address, offset, length = text[0][3:6]
    return {"sha256": sha256(data).hexdigest(), "entry": entry,
            "text": {"offset": offset, "address": address, "size": length,
                     "sha256": executable_bytes(data, offset, length)},
            "executableLoadSegments": segments}

def symbol_fingerprints(binary, selected, elf):
    data = binary.read_bytes()
    segments = json.loads(elf.read_bytes())["executableLoadSegments"]
    result = {}
    for line in selected.read_text().splitlines():
        address, size, _, name = line.split(maxsplit=3)
        address, size = int(address, 16), int(size)
        segments_for_symbol = [segment for segment in segments if
            segment["address"] <= address and address + size <= segment["address"] + segment["filesz"]]
        assert len(segments_for_symbol) == 1 and size > 0 and name not in result, line
        segment = segments_for_symbol[0]
        offset = address - segment["address"] + segment["offset"]
        result[name] = {"address": address, "offset": offset, "size": size, "mod32": address % 32,
                        "mod64": address % 64, "sha256": executable_bytes(data, offset, size)}
    assert result
    return result

def instruction_without_relocation(encoding, instruction):
    raw = bytes.fromhex(encoding)
    if raw[:1] in (b"\xe8", b"\xe9") and len(raw) == 5 and instruction.endswith("(SB)"):
        return [raw[:1].hex(), instruction]
    if raw[:2] == b"\x83\x3d" and instruction.startswith("CMPL runtime.writeBarrier(SB),"):
        return [(raw[:2] + raw[-1:]).hex(), instruction]
    if instruction.startswith("LEAQ ") and "(IP), " in instruction and len(raw) == 7:
        return [raw[:-4].hex(), instruction.split("(IP), ", 1)[1]]
    if instruction.startswith("LEAQ ") and "(SB), " in instruction and len(raw) == 7:
        return [raw[:-4].hex(), instruction]
    return [raw.hex()]

def normalized_disassembly(path):
    output = {}
    for section in path.read_text().split("TEXT ")[1:]:
        lines = section.splitlines()
        name = lines[0].split()[0].removesuffix("(SB)")
        instructions = []
        for line in filter(str.strip, lines[1:]):
            _, address, encoding, instruction = line.strip().split(maxsplit=3)
            instructions.append((int(address, 16), instruction_without_relocation(encoding, instruction.strip())))
        assert name not in output and instructions, name
        first = instructions[0][0]
        output[name] = {"address": first, "shape": [[address - first, *op] for address, op in instructions]}
    assert output, path
    return output

def nearby_text_map(source, output):
    rows = source.read_text().splitlines()
    names = [row.split(maxsplit=3)[-1] for row in rows]
    indexes = [index for index, name in enumerate(names) if name.startswith(PREFIX) or name == "context.(*valueCtx).Value"]
    assert indexes, names[:2]
    keep = {neighbor for index in indexes for neighbor in range(max(0, index - 2), min(len(rows), index + 3))}
    output.write_text("\n".join(rows[index] for index in sorted(keep)) + "\n")

def plan(evidence):
    leaves = [f"BenchmarkGatewayIdentity/{case}/{side}" for case in
              ("anonymous", "authenticated", "typed-nil") for side in ("after", "before")]
    rows = [{"Package": "pkg/lux/luvia", "Name": name + "-2"} for name in leaves]
    (evidence / 'expected.json').write_text(json.dumps({'Base': rows, 'Head': rows}) + '\n')
    selector = '|'.join('/'.join('^' + part + '$' for part in name.split('/')) for name in leaves)
    (evidence / 'batch.tsv').write_text('pkg/lux/luvia\tluvia-gateway-identity\t' + selector + '\n')
    matrix = {'arms': ARMS, 'comparisons': COMPARISONS, 'groups': 2, 'pairs': 10,
              'processes': len(COMPARISONS) * 2 * 10 * 2, 'benchtime': '200ms', 'cpu': CPU}
    (evidence / 'matrix.json').write_text(json.dumps(matrix, indent=2) + '\n')


def inventory(expected_path, raw, count):
    expected = Counter({row['Name']: count for row in json.loads(expected_path.read_bytes())['Base']})
    rows = re.findall(r'^(Benchmark\S+)\s+(\d+)\s+', raw, re.M)
    assert Counter(name for name, iterations in rows) == expected, 'wrong complete six-leaf inventory'
    if count == 1:
        assert all(iterations == '1' for name, iterations in rows), 'not a one-iteration proof'
    assert raw.splitlines().count('PASS') == count and not re.search(r'(^FAIL|--- FAIL)', raw, re.M)
    assert re.findall(r'^cpu: (.+)$', raw, re.M) == [CPU] * count, 'different or missing processor'


def verify_inputs(repo, rev, label, output):
    raw = subprocess.check_output(['git', '--no-replace-objects', '-C', str(repo), 'ls-tree', '-r', rev])
    rows = [row for row in raw.decode().splitlines() if row.split('\t', 1)[1].endswith(('.go', '.mod', '.sum'))
            or row.split('\t', 1)[1] in ('VERSION', 'scripts/setup-modules.sh')]
    manifest = ('\n'.join(rows) + '\n').encode()
    assert sha256(manifest).hexdigest() == HASHES[label]['go_inputs'], 'Go inputs differ from the sealed pair'
    output.write_bytes(manifest)


def apply_candidate(root, label, proof):
    pkg = root / 'pkg/lux/luvia'
    original = {}
    for name in ('luvia.go', 'middleware.go', 'middleware_test.go'):
        path = pkg / name
        assert stat.S_ISREG(path.lstat().st_mode), 'nonregular input'
        original[name] = path.read_bytes()
        assert sha256(original[name]).hexdigest() == HASHES[label][name], 'unverified input: ' + name
    assert not os.path.lexists(pkg / 'identity.go'), 'existing identity component'
    middleware, gateway = original['middleware.go'].decode(), original['luvia.go'].decode()
    first, last = middleware.index('type identityCtxKey struct{}'), middleware.index('// AuthMiddleware wraps')
    component = middleware[first:last]
    start = gateway.index('\trouter.IdentityExtractor = func(ctx context.Context) any {')
    end = gateway.index('\n\trouter.InternalRequestContext', start)
    closure = gateway[start:end]
    assert closure.endswith('\n\t}')
    body = closure.split('\n', 1)[1].rsplit('\n\t}', 1)[0]
    body = '\n'.join(line[1:] if line.startswith('\t') else line for line in body.splitlines())
    changed = {'middleware.go': middleware[:first] + middleware[last:],
               'luvia.go': gateway[:start] + '\trouter.IdentityExtractor = gatewayIdentity' + gateway[end:],
               'identity.go': 'package luvia\n\nimport (\n\t"context"\n\n\t"github.com/light-speak/luxo/pkg/lux/auth"\n)\n\n'
                              + component + 'func gatewayIdentity(ctx context.Context) any {\n' + body + '\n}\n'}
    patch = ''
    for name, content in changed.items():
        patch += ''.join(difflib.unified_diff(original.get(name, b'').decode().splitlines(True), content.splitlines(True),
                                            fromfile='a/pkg/lux/luvia/' + name if name in original else '/dev/null',
                                            tofile='b/pkg/lux/luvia/' + name))
        (pkg / name).write_text(content)
    assert (pkg / 'middleware_test.go').read_bytes() == original['middleware_test.go']
    proof.write_text(json.dumps({'before': {name: sha256(data).hexdigest() for name, data in original.items()},
                                'after': {name: sha256(data.encode()).hexdigest() for name, data in changed.items()}}, indent=2) + '\n')
    proof.with_suffix('.patch').write_text(patch)


def outcome(controls, stock, deployed, auxiliary):
    if not controls or any(status not in (0, 1) for status in (stock, deployed, auxiliary)):
        return 4, 'invalid-or-unstable-experiment'
    if stock:
        return 1, 'original-stock-rejected-candidate-' + ('rejected' if deployed or auxiliary else 'passed-no-adoption')
    if deployed or auxiliary:
        return 1, 'ordinary-candidate-rejected'
    return 0, 'all-ordinary-comparisons-passed-no-adoption'


def summarize(evidence):
    rows = (evidence / 'gates.tsv').read_text().splitlines()
    gates = dict(line.split('\t') for line in rows)
    assert len(gates) == len(rows), 'duplicate gate receipt'
    expected = {f'{name}.{group}{reverse}' for name in ('aa', 'bb', 'cc', 'dd') for group in (1, 2) for reverse in ('', '.reverse')}
    expected |= {'ab-combined', 'ac-combined', 'cd-combined'}
    assert set(gates) == expected, 'missing or extra gate'
    for name, value in gates.items():
        status = int(value)
        text = (evidence / (name + '.gate.txt')).read_text()
        if status == 0:
            assert text.startswith('performance gate passed:'), 'unverified gate success'
        elif status == 1:
            assert re.search(r'^performance gate rejected [1-9][0-9]* regression\(s\)$', text, re.M), 'not a performance rejection'
    result = outcome(all(gates[name] == '0' for name in expected if '-combined' not in name),
                     int(gates['ab-combined']), int(gates['ac-combined']), int(gates['cd-combined']))
    (evidence / 'outcome.txt').write_text(result[1] + '\n')
    return result[0]


def layout(evidence):
    report = {}
    for arm in ARMS:
        for suffix in ('', '-consumer'):
            stem = arm + suffix
            symbols = json.loads((evidence / (stem + '.symbols.json')).read_bytes())
            name = PREFIX + ('gatewayIdentity' if arm.startswith('candidate-') else 'newGateway.func1')
            assert name in symbols, 'missing actual production extractor'
            assembly = normalized_disassembly(evidence / (stem + '.objdump.txt'))
            assert name in assembly
            build = (evidence / (stem + '.build.txt')).read_text()
            assert '-ldflags' not in build and '-gcflags' not in build, 'nonstandard compilation'
            assert '\tbuild\tCGO_ENABLED=1\n' in build and '\tbuild\tGOAMD64=v1\n' in build
            assert '\tbuild\tGOOS=linux\n' in build and '\tbuild\tGOARCH=amd64\n' in build
            (evidence / (stem + '.normalized.json')).write_text(json.dumps(assembly, indent=2, sort_keys=True) + '\n')
            report[stem] = {'extractor': name, 'identityEntryMod64': symbols[name]['mod64'],
                            'instructionShapeSHA': sha256(json.dumps(assembly[name]['shape']).encode()).hexdigest()}
    (evidence / 'layout.json').write_text(json.dumps(report, indent=2) + '\n')


if __name__ == '__main__':
    action, *args = sys.argv[1:]
    if action == 'plan': plan(Path(args[0]))
    elif action == 'inventory': inventory(Path(args[0]), Path(args[1]).read_text(), int(args[2]))
    elif action == 'inputs': verify_inputs(Path(args[0]), args[1], args[2], Path(args[3]))
    elif action == 'apply': apply_candidate(Path(args[0]), args[1], Path(args[2]))
    elif action == 'layout': layout(Path(args[0]))
    elif action == 'summarize': raise SystemExit(summarize(Path(args[0])))
    elif action == 'elf': print(json.dumps(executable_fingerprints(Path(args[0])), indent=2))
    elif action == 'symbols': print(json.dumps(symbol_fingerprints(*map(Path, args)), indent=2, sort_keys=True))
    elif action == 'map': nearby_text_map(*map(Path, args))
    else: raise SystemExit('unknown action: ' + action)
