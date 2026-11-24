#!/usr/bin/env bash
set -euo pipefail

mode="${1:---plan-only}"
[[ $# -le 1 ]] || exit 2
case "$mode" in run|--plan-only|--inventory-only) ;; *) exit 2;; esac
script_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd -P)
repo=$(git -C "$script_dir" rev-parse --show-toplevel)
sampler="$script_dir/diagnose-hosted-perf.sh"
printf '%s  %s\n' 9e0a5f421f2e76674b819f81074a3d416a51174e0f93b858a63d20c27d64e072 "$sampler" | sha256sum -c -
source "$sampler"

base=925f30ee1dcd45882b29130b8ed02836ed88317e
head=dcf2ca3eb2193f2e060051dfe4d47c5277b19f5d
product=a9f4678407d23e4f945cf5ad92467fadf36d21b8
package=pkg/codegen
binary=pkg-codegen.test
inventory_filter='^BenchmarkGlobalReceiverName$|^BenchmarkMigrationStateValidation$'
intel='INTEL(R) XEON(R) PLATINUM 8573C'
amd='AMD EPYC 9V74 80-Core Processor'

mkdir -p "$repo/.tmp"
test ! -L "$repo/.tmp"
scratch=$(mktemp -d "$repo/.tmp/pr103-short-paths.XXXXXXXX")
root="$repo/.tmp/hosted-pr103-short-paths"
if [[ "$mode" != run ]]; then root="$scratch/evidence"; fi
created=false
cleanup_short_paths() {
  local rc=$?
  trap - EXIT
  [[ -d "$scratch" && ! -L "$scratch" && "$scratch" == "$repo"/.tmp/pr103-short-paths.* ]] || exit 99
  if (( rc != 0 )) && [[ "$mode" != run && -d "$root" ]]; then
    for log in "$root"/*compile*.log; do
      if [[ -f "$log" ]]; then tail -n 10 "$log" >&2; fi
    done
  fi
  rm -rf -- "$scratch" || exit 98
  if [[ "$mode" == run ]] && "$created"; then
    printf 'exit=%s\nowned_scratch_removed=true\n' "$rc" > "$root/cleanup.txt"
  fi
  exit "$rc"
}
trap cleanup_short_paths EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
mkdir "$root"
created=true
cp "${BASH_SOURCE[0]}" "$sampler" "$root/"
printf 'base=%s\nhead=%s\ntested_merge=%s\ntested_tree=%s\noriginal_run=45385956331\n' \
  "$base" "$head" ada810a475e80c3e58b6d1280d5347247246103c "$product" > "$root/revisions.txt"

scope=
observed=
if [[ "$mode" == run ]]; then
  [[ "$(go version)" == 'go version go1.27.1 linux/amd64' ]]
  [[ "$(go env GOOS GOARCH GOAMD64 CGO_ENABLED)" == $'linux\namd64\nv1\n1' ]]
  collect_host > "$root/environment.txt"
  observed=$(awk '/^Model name:/ {sub(/^Model name:[[:space:]]*/, ""); print}' "$root/environment.txt")
  test -n "$observed"
  case "$(printf '%s' "$observed" | LC_ALL=C tr '[:lower:]' '[:upper:]')" in
    "$intel") scope=intel;;
    'AMD EPYC 9V74 80-CORE PROCESSOR') scope=amd;;
  esac
  jq -n --arg cpu "$observed" --arg scope "$scope" --arg intel "$intel" --arg amd "$amd" \
    '{actualCPU:$cpu, scope:$scope, matchingFormalModel:($scope != ""), supported:[$intel,$amd]}' \
    > "$root/runner-cpu.json"
  cat "$root/runner-cpu.json"
  if [[ -z "$scope" ]]; then exit 3; fi
fi

original_objects=$(git -C "$repo" rev-parse --path-format=absolute --git-path objects)
mkdir "$scratch/objects"
export GIT_OBJECT_DIRECTORY="$scratch/objects" GIT_ALTERNATE_OBJECT_DIRECTORIES="$original_objects"
test "$(git -C "$repo" rev-parse "$base^{tree}")" = a697ac4025e43a75343715bb5690e55c11e00063
test "$(git -C "$repo" merge-tree --write-tree "$base" "$head")" = "$product"
git -C "$repo" diff --exit-code "$base" "$product" -- \
  "$package/identifiers.go" "$package/migrate_state.go" "$package/global_value_bench_test.go" \
  "$package/migrate_state_bench_test.go" scripts/benchgate scripts/benchplan scripts/benchrun
git -C "$repo" diff --name-status "$base" "$product" > "$root/product-files.tsv"

verify_fixtures() {
  python3 - "$@" <<'PY'
import csv
from hashlib import sha256
import json
from pathlib import Path
import re
import subprocess
import sys

action, root, *args = sys.argv[1:]
root = Path(root)
package = "pkg/codegen"
module = "github.com/light-speak/luxo/" + package
receiver = "BenchmarkGlobalReceiverName"
migration = "BenchmarkMigrationStateValidation"
receiver_file = "global_value_bench_test.go"
migration_file = "migrate_state_bench_test.go"
source_pins = {
    receiver_file: "7280547b02504a4e07437808232485a13425c0d8ead9c968c95fda47ec6252e9",
    migration_file: "08bf4e4e370167fd7f5d91cc0e420ecb09302576a76dbb9e767202316cf8ce50",
}
receivers = [f"{receiver}/{name}-2" for name in ("User", "_Record", "用户")]
migrations = [
    f"{migration}/{size}/{action}-2"
    for size in ("empty", "small", "large")
    for action in ("validate", "load/reference", "load/strict", "save/reference", "save/strict")
]
cases = {"intel": (receivers[1], receivers), "amd": (migrations[0], [migrations[0], migrations[5]])}
baseline_evidence = {
    "intel": ("03", 14917795901, "26fc97aea69750a853d11f6735b683f08c1083e3113b342c5b1e11b14ad57892"),
    "amd": ("05", 14915589045, "58e733b94d162a06beeda3a996c83a71e6073555b60879a8382d91b54bf07338"),
}
conditions = ("default", "isolated")

def write_json(path, value):
    path.write_text(json.dumps(value, indent=2) + "\n")

def samples(path, wanted, one=False, cpu=None):
    lines = path.read_text().splitlines()
    assert len(lines) >= 5 and lines[2] == f"pkg: {module}" and lines[-1] == "PASS", path
    assert lines[3].startswith("cpu: "), path
    if cpu:
        assert lines[:2] == ["goos: linux", "goarch: amd64"] and lines[3][5:].strip().casefold() == cpu.casefold(), path
    rows = [line.split() for line in lines if line.startswith("Benchmark")]
    assert sorted(row[0] for row in rows) == sorted(wanted), path
    for row in rows:
        assert len(row) == 8 and row[3::2] == ["ns/op", "B/op", "allocs/op"]
        assert int(row[1]) > 0 and (not one or row[1] == "1"), path

if action == "plan":
    repo, base, head = args
    for role in (base, head):
        for filename, digest in source_pins.items():
            data = subprocess.check_output(["git", "-C", repo, "show", f"{role}:{package}/{filename}"])
            assert sha256(data).hexdigest() == digest
    for scope, (historical, names) in cases.items():
        path = root / scope
        path.mkdir()
        pattern = "|".join(
            "/".join("^" + re.escape(part) + "$" for part in name.removesuffix("-2").split("/"))
            for name in names
        )
        (path / "filter.txt").write_text(pattern + "\n")
        rows = [{"Package": package, "Name": name} for name in names]
        write_json(path / "expected.json", {"Base": rows, "Head": rows})
        shard, artifact, digest = baseline_evidence[scope]
        write_json(path / "scope.json", {"historical": historical, "timed": names, "smokeLeaves": 18,
                   "originalShard": shard, "originalArtifact": artifact, "originalArtifactSHA256": digest})
    (root / "inventory.names").write_text("\n".join(sorted(receivers + migrations)) + "\n")
elif action == "overlay":
    source, destination = map(Path, args)
    directory = source / package
    original = (directory / receiver_file).read_bytes()
    migration_bytes = (directory / migration_file).read_bytes()
    assert sha256(original).hexdigest() == source_pins[receiver_file]
    assert sha256(migration_bytes).hexdigest() == source_pins[migration_file]
    declaration_index = original.index(b"func BenchmarkGlobalReceiverName(")
    header = b'package codegen\n\nimport "testing"\n'
    preceding_lines = original[:declaration_index].count(b"\n")
    declaration = original[declaration_index:]
    assert preceding_lines == 45
    assert sha256(declaration).hexdigest() == "695b3f5c8acbe1b61d3c944209054b6e8f6768927aaef47c425f840327b5ebda"
    projected = header + b"\n" * (preceding_lines - header.count(b"\n")) + declaration
    assert sha256(projected).hexdigest() == "330fc5c6785cae1fd2e89810a1ec945e9384669a9efaa3af47cc5e3a2bf74da3"
    temporary = destination.with_suffix(".go")
    temporary.write_bytes(projected)
    replacements = {
        str(path): "" for path in sorted(directory.glob("*_test.go"))
        if path.name not in source_pins and path.is_file() and not path.is_symlink()
    }
    assert len(replacements) == 198
    replacements[str(directory / receiver_file)] = str(temporary)
    write_json(destination, {"Replace": replacements})
elif action == "inventory":
    needed = [args[0]] if args[0] else cases
    cpu = args[1] or None
    for condition in conditions:
        for role in ("base", "head"):
            path = root / condition
            samples(path / f"inventory-{role}.raw", receivers + migrations, one=True, cpu=cpu)
            for scope in needed:
                samples(path / f"selected-{role}-{scope}.raw", cases[scope][1], one=True, cpu=cpu)
    write_json(root / "inventory-proof.json", {"leavesPerRoleAndCondition": 18, "roles": 2, "conditions": 2})
elif action == "raw":
    scope, cpu = args
    condition_order = []
    for offset, group in enumerate(("primary", "confirmation")):
        for pair in range(1, 11):
            odd = (pair + offset) % 2 == 1
            for comparison in (("ab", "aa") if odd else ("aa", "ab")):
                for condition in (conditions if odd else conditions[::-1]):
                    condition_order.append([group, f"{pair:02}", comparison, condition])
    with (root / "condition-order.tsv").open() as stream:
        header, *rows = csv.reader(stream, delimiter="\t")
    assert header == ["group", "pair", "comparison", "condition"] and rows == condition_order
    total = 0
    for condition in conditions:
        path = root / condition
        expected = []
        for offset, group in enumerate(("primary", "confirmation")):
            for pair in range(1, 11):
                odd = (pair + offset) % 2 == 1
                for comparison in (("ab", "aa") if odd else ("aa", "ab")):
                    for side in (("left", "right") if odd else ("right", "left")):
                        raw = f"raw/{group}.{pair:02}.{comparison}.01.{side}.txt"
                        role = "head" if comparison == "ab" and side == "right" else "base"
                        expected.append([group, f"{pair:02}", comparison, "01", side, role, condition, raw])
                        samples(path / raw, cases[scope][1], cpu=cpu)
        with (path / "order.tsv").open() as stream:
            header, *rows = csv.reader(stream, delimiter="\t")
        assert header == ["group", "pair", "comparison", "case", "side", "revision", "variant", "raw"]
        assert rows == expected
        assert {p.relative_to(path).as_posix() for p in (path / "raw").iterdir()} == {row[-1] for row in rows}
        total += len(rows)
    assert total == 160
    write_json(root / "raw-inventory.json", {"arms": total, "benchmarkRows": total * len(cases[scope][1])})
elif action == "result":
    scope, = args
    historical = cases[scope][0].removeprefix("Benchmark").removesuffix("-2")
    def verdict(condition):
        path = root / condition
        lines = (path / "verdicts.tsv").read_text().splitlines()
        assert lines[0] == "comparison\tbenchgate_exit"
        exits = dict(row.split("\t") for row in lines[1:])
        expression = re.compile(rf"^confirmed performance regression: {re.escape(module)}/(\S+)-2 (sec/op|B/op|allocs/op) ")
        rejects = [match.groups() for line in (path / "ab.benchgate.log").read_text().splitlines()
                   if (match := expression.match(line))]
        assert (exits["ab"] == "0") == (not rejects)
        return exits, rejects
    ordinary, control = verdict("default")
    isolated, treatment = verdict("isolated")
    reproduced = (historical, "sec/op") in control
    controls = [item for item in control if item != (historical, "sec/op")]
    result = {
        "historical": historical,
        "stableAA": ordinary["aa"] == isolated["aa"] == "0",
        "ordinaryReproduced": reproduced,
        "ordinaryRejects": control,
        "isolatedRejects": treatment,
        "ordinaryControlRejects": controls,
        "scope": "Linkage diagnostic and original threshold replay on the matching host.",
    }
    write_json(root / "diagnosis.json", result)
    print((root / "diagnosis.json").read_text(), end="")
    sys.exit(1 if not result["stableAA"] or treatment or controls else 0 if reproduced else 4)
else:
    sys.exit("unknown fixture action")
PY
}

verify_fixtures plan "$root" "$repo" "$base" "$product"
if [[ "$mode" == --plan-only ]]; then
  cat "$root/revisions.txt" "$root/"*/scope.json
  exit 0
fi
export TMPDIR="$scratch/tmp" GOWORK=off GOTOOLCHAIN=local LC_ALL=C
mkdir -p "$TMPDIR" "$scratch/bin"
test -z "$(go env GOFLAGS GOEXPERIMENT | tr -d '\n')"
for role in base head; do
  ref="$base"
  if [[ "$role" == head ]]; then ref="$product"; fi
  directory="$scratch/source/$role"
  mkdir -p "$directory/.tmp" "$scratch/bin/$role-isolated"
  git -C "$repo" archive "$ref" | tar -xf - -C "$directory"
  sh "$directory/scripts/setup-modules.sh" "$directory" > "$root/workspace-$role.log"
  cp "$directory/go.work" "$root/go-$role.work.txt"
done
(
  cd "$scratch/source/base"
  for tool in benchrun benchplan; do go build -o "$scratch/bin/$tool" "./scripts/$tool"; done
  if [[ "$mode" == run ]]; then
    go build -o "$scratch/bin/benchgate" ./scripts/benchgate
    GOBIN="$scratch/bin" go install "golang.org/x/perf/cmd/benchstat@$benchstat_version"
    go version -m "$scratch/bin/benchstat"
  fi
) > "$root/tools.log" 2>&1

conditions=(default isolated)
scopes=(intel amd)
if [[ -n "$scope" ]]; then scopes=("$scope"); fi
for condition in "${conditions[@]}"; do mkdir "$root/$condition"; done
for role in base head; do
  directory="$scratch/source/$role"
  overlay="$scratch/$role-overlay.json"
  verify_fixtures overlay "$root" "$directory" "$overlay"
  cp "$overlay" "${overlay%.json}.go" "$root/isolated/"
  "$scratch/bin/benchrun" compile "$directory" "$scratch/bin/$role" "$package" > "$root/$role-default-compile.log" 2>&1
  (
    cd "$directory"
    GOWORK="$PWD/go.work" go test -trimpath -c -overlay="$overlay" "./$package" \
      -o "$scratch/bin/$role-isolated/$binary"
  ) > "$root/$role-isolated-compile.log" 2>&1
  for condition in "${conditions[@]}"; do
    evidence="$root/$condition"
    role_binary="$role"
    if [[ "$condition" == isolated ]]; then role_binary="$role-isolated"; fi
    record_binaries "$role_binary"
    exe="$scratch/bin/$role_binary/$binary"
    go tool nm -size "$exe" | grep -E '/codegen\.(Benchmark(GlobalReceiverName|MigrationStateValidation)|receiverName|validateSchemaState)' \
      > "$evidence/$role-symbols.nm"
    go tool objdump -s '/codegen\.(Benchmark(GlobalReceiverName|MigrationStateValidation)|receiverName|validateSchemaState)' \
      "$exe" > "$evidence/$role-symbols.objdump"
    "$scratch/bin/benchrun" run "$directory" "$scratch/bin/$role_binary" "$package" \
      -test.run='^$' -test.bench="$inventory_filter" -test.benchmem -test.benchtime=1x -test.count=1 -test.cpu=2 \
      -test.timeout=90s > "$evidence/inventory-$role.raw"
    for current in "${scopes[@]}"; do
      "$scratch/bin/benchrun" run "$directory" "$scratch/bin/$role_binary" "$package" \
        -test.run='^$' -test.bench="$(cat "$root/$current/filter.txt")" -test.benchmem \
        -test.benchtime=1x -test.count=1 -test.cpu=2 -test.timeout=90s > "$evidence/selected-$role-$current.raw"
    done
  done
done
verify_fixtures inventory "$root" "$scope" "$observed"
printf '%s\n' 'Default and isolated builds retained all 18 leaves on both roles.'
if [[ "$mode" == --inventory-only ]]; then exit 0; fi

for condition in "${conditions[@]}"; do
  evidence="$root/$condition"
  cp "$root/$scope/expected.json" "$evidence/expected.json"
  mkdir "$evidence/raw"
  printf 'group\tpair\tcomparison\tcase\tside\trevision\tvariant\traw\n' > "$evidence/order.tsv"
done
filter=$(cat "$root/$scope/filter.txt")
printf 'group\tpair\tcomparison\tcondition\n' > "$root/condition-order.tsv"
for group in primary confirmation; do
  offset=0
  if [[ "$group" == confirmation ]]; then offset=1; fi
  for pair in {1..10}; do
    printf -v sample '%02d' "$pair"
    if (((pair + offset) % 2 == 1)); then
      first=left
      comparisons=(ab aa)
      order=(default isolated)
    else
      first=right
      comparisons=(aa ab)
      order=(isolated default)
    fi
    for comparison in "${comparisons[@]}"; do
      other=base
      if [[ "$comparison" == ab ]]; then other=head; fi
      for condition in "${order[@]}"; do
        evidence="$root/$condition"
        printf '%s\t%s\t%s\t%s\n' "$group" "$sample" "$comparison" "$condition" >> "$root/condition-order.tsv"
        run_pair "$group" "$sample" "$comparison" "$other" "$first" 01 "$package" "$condition" "$filter"
      done
    done
  done
done
verify_fixtures raw "$root" "$scope" "$observed"
for condition in "${conditions[@]}"; do
  evidence="$root/$condition"
  analyze_groups
done
verify_fixtures result "$root" "$scope"
