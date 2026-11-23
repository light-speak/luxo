#!/usr/bin/env bash
set -euo pipefail

transport_write_plan() {
  python3 - "$transport_dir" "$transport_suite" "$transport_head" "$evidence" <<'PYTHON'
from hashlib import sha256
import json
from pathlib import Path
import sys

directory, suite, head, evidence = sys.argv[1:]
directory, evidence = Path(directory), Path(evidence)
scope = json.loads((directory / "diagnose-pr79-pr81-scope.json").read_text())[suite]
if scope["head"] != head:
    raise SystemExit("transport scope head mismatch")
cases, rows = [], []
for number, case in enumerate(scope["cases"], 1):
    label = f"{number:02}"
    package = case["package"]
    cases.append([label, package, case["variant"], case["leaves"][0], case["filter"]])
    (evidence / f"{label}.names").write_text("\n".join(case["leaves"]) + "\n")
    rows.extend({"Package": package, "Name": leaf} for leaf in case["leaves"])
planned = {(row["Package"], row["Name"]) for row in rows}
historical = {(row["package"], row["leaf"]) for row in scope["historical"]}
controls = {(row["package"], row["leaf"]) for row in scope["controls"]}
counts = (8, 10, 2) if suite == "pr79" else (4, 4, 1)
if ((len(cases), len(historical), len(controls)) != counts or len(planned) != len(rows)
        or (historical & controls) or planned != (historical | controls)):
    raise SystemExit("transport case set must match every failed leaf and the real-path controls")
(evidence / "cases.tsv").write_text("".join("\t".join(case) + "\n" for case in cases))
(evidence / "expected.json").write_text(json.dumps({"Base": rows, "Head": rows}, indent=2) + "\n")
(evidence / "scope.json").write_text(json.dumps(scope, indent=2) + "\n")
if suite == "pr79":
    source = (directory / "diagnose-pr79-pr81-tls.go.txt").read_bytes()
    if sha256(source).hexdigest() != scope["tlsFixtureSHA256"]:
        raise SystemExit("TLS control SHA-256 mismatch")
    (evidence / "gateway-tls.go.txt").write_bytes(source)
PYTHON
  printf '%s\n' "$transport_harness_revision" > "$evidence/adapter-revision.txt"
  printf '%s\n' "$transport_adapted" > "$evidence/adapter.sh"
}

transport_record_sources() {
  python3 - "$1" "$2" "$evidence" <<'PYTHON'
from hashlib import sha256
import json
from pathlib import Path
import sys

revision, source, evidence = sys.argv[1:]
source, evidence = Path(source), Path(evidence)
scope = json.loads((evidence / "scope.json").read_text())
common = ["go.mod", "go.sum", "scripts/setup-modules.sh", "scripts/benchrun/main.go",
          "scripts/benchgate/approved-costs.json"] + scope["unchanged"]
for category, files in (("unchanged", common), ("changed", scope["changed"])):
    rows = []
    for file in files:
        digest = sha256((source / file).read_bytes()).hexdigest()
        rows.append(f"{digest}  {file}\n")
    (evidence / f"{category}-sources-{revision}.sha256").write_text("".join(rows))
PYTHON
}

transport_record_layout() {
  local revision="$1" package binary name
  local pattern='^github[.]com/light-speak/luxo/pkg/lux/.*[.](Benchmark(RequestHead|ReserveData|HandlerPanicFormatting|RPCHandlerCompletion|ObservationExport|AuthMiddlewareIsolation|HistoricalGroupedResponse|TraceEncodingComparison|RPCErrorPanicIsolation|RPC_Echo|DiagnosticGatewayTLS)([.]func[0-9]+)?|startGatewayServerWithBodyLimits|ConfigureHTTPServer|ConfigureHTTPServer[.]func1|readResponse|callRequest|releaseReply|exchangeRequest)$'
  for package in "${transport_packages[@]}"; do
    name="${package##*/}"
    binary="$scratch/bin/$revision/pkg-lux-$name.test"
    go tool nm -size "$binary" | awk -v pattern="$pattern" '$4 ~ pattern' > "$evidence/symbols-$revision-$name.nm"
    go tool objdump -s "$pattern" "$binary" > "$evidence/symbols-$revision-$name.objdump"
    test -s "$evidence/symbols-$revision-$name.nm"
    test -s "$evidence/symbols-$revision-$name.objdump"
  done
  if [[ "$transport_suite" == pr79 ]]; then
    compile_diagnostic_fixture "$revision" tls pkg/lux/luvia gateway_tls_diagnostic_test.go gateway-tls.go.txt
  fi
}

transport_main() {
  local transport_suite="${1:?Usage: diagnose-pr79-pr81.sh pr79|pr81 [--inventory-only]}"
  shift
  local transport_head transport_cases
  local -a transport_packages
  case "$transport_suite" in
    pr79)
      transport_head=e04c586e491b8b215308d10f7228636e38547f5b
      transport_cases=8
      transport_packages=(pkg/lux/h2transport pkg/lux/luvia pkg/lux/rpc)
      ;;
    pr81)
      transport_head=33ce49477379143a8e3bfcd34a3628ec6a9bc626
      transport_cases=4
      transport_packages=(pkg/lux/luvia pkg/lux/rpc)
      ;;
    *) printf 'Unknown transport suite %s\n' "$transport_suite" >&2; return 2 ;;
  esac
  local repository transport_dir transport_adapted TMPDIR
  local transport_harness_revision=6181fbeaca473f468fb414ed18bb28225f00cdf2
  repository="$(git rev-parse --show-toplevel)"
  transport_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)"
  TMPDIR="$(pwd -P)/.tmp"
  mkdir -p "$TMPDIR"
  test ! -L "$TMPDIR"
  export TMPDIR
  # Keep the pinned sampler, archive setup, per-process validator and gate.
  transport_adapted="$(
    git -C "$repository" show "$transport_harness_revision:scripts/diagnose-pr71-attempt2.sh" |
      python3 -c '
from hashlib import sha256
import sys

original = sys.stdin.buffer.read()
if sha256(original).hexdigest() != "b14e338e9970193e8be84a0debd7b6686126842b6ae99f79ce0b529f26bda1b8":
    raise SystemExit("transport adapter input SHA-256 mismatch")
script = original.decode()

def replace(old, new):
    global script
    if script.count(old) != 1:
        raise SystemExit(f"transport adapter marker changed: {old!r}")
    script = script.replace(old, new)

def omit(start, end):
    first = script.index(start)
    last = script.index(end, first)
    replace(script[first:last], "")

omit("pr71_attempt2_write_plan() {", "pr71_attempt2_verify_process() {")
omit("pr71_attempt2_record_layout() {", "pr71_attempt2_archive() {")
begin = script.index("  (\n    cd \"$source\"\n    sha256sum")
end = script.index("  sh \"$source/scripts/setup-modules.sh\"", begin)
replace(script[begin:end], "  transport_record_sources \"$revision\" \"$source\"\n")
replace("  case \"$case_id\" in 01|02|03) package=pkg/lux/api ;; 04) package=pkg/lux/rpc ;; *) return 1 ;; esac",
        "  package=\"$(awk -F \"\\t\" -v id=\"$case_id\" \x27$1==id {print $2; found++} END {if (found != 1) exit 1}\x27 \"$evidence/cases.tsv\")\" || return 1")
replace("local revision case_id package variant name filter output", "local revision case_id package variant name filter output binaries")
replace("      \"$scratch/bin/benchrun\" run \"$scratch/source/$revision\" \"$scratch/bin/$revision\" \"$package\"",
        "      binaries=\"$scratch/bin/$revision\"\n"
        "      if [[ \"$variant\" != default ]]; then binaries+=\"-$variant\"; fi\n"
        "      \"$scratch/bin/benchrun\" run \"$scratch/source/$revision\" \"$binaries\" \"$package\"")
replace("Exact 1x inventory passed for all eight leaves on both revisions.",
        "Exact 1x inventory passed for every transport leaf on both revisions.")
replace("[[ \"$case_id\" =~ ^0[1-4]$ ]]", "[[ \"$case_id\" =~ ^[0-9][0-9]$ ]]")
replace("[[ \"$count\" -eq 320 ]]", "[[ \"$count\" -eq $((80 * transport_cases)) ]]")
replace("evidence=\"$launch/.tmp/hosted-pr71-attempt2\"", "evidence=\"$launch/.tmp/hosted-pr79-pr81-$transport_suite\"")
replace("head=e79e2a2930213e317eb6e0f1a77e27f22c4502e7", "head=\"$transport_head\"")
replace(".tmp/.hosted-pr71-attempt2.XXXXXX", ".tmp/.hosted-transport.XXXXXX")
start = script.index("One runner, original default-link compiler flags")
stop = script.index("\nPROTOCOL\n", start)
replace(script[start:stop], """One native runner per suite. Exact base and head archives and original default linking.
Every originally rejected timing leaf appears in scope.json with its original shard and CPU.
The TLS controls use isolated test binaries with the same fixture in both revisions.
TLS startup uses a valid credential pair. A fresh TLS connection negotiates h2 and serves a request.
The LRPC echo control performs ordinary unary exchanges through the real client and pooled response path.
A/A uses the base binary twice. A/B compares the base and PR binaries.
Two groups of ten alternating pairs. Original 200ms, benchmem, CPU=2, benchstat alpha=.01.
The original time and allocation thresholds apply. An A/A failure makes the result inconclusive.
The targeted sample keeps original failure leaves and production gates remain definitive.""")
replace("  pr71_attempt2_write_plan\n", "  transport_write_plan\n")
replace("  cmp \"$evidence/sources-base.sha256\" \"$evidence/sources-head.sha256\"",
        "  cmp \"$evidence/unchanged-sources-base.sha256\" \"$evidence/unchanged-sources-head.sha256\"")
replace("      for package in pkg/lux/api pkg/lux/rpc; do", "      for package in \"${transport_packages[@]}\"; do")
replace("    pr71_attempt2_record_layout \"$revision\"", "    transport_record_layout \"$revision\"")
print(script, end="")
'
  )"
  source /dev/stdin <<< "$transport_adapted"
  pr71_attempt2_main "$@"
}

transport_main "$@"
