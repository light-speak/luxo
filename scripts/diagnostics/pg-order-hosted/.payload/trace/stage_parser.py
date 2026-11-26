import json
import re

LEAVES = ("concurrent/up-existing-index", "transaction/up-missing-history")


def expected_queries(arm, leaf):
    if leaf == LEAVES[0]:
        return ({"A": "lock catalog checksum timestamp index index-guard",
                 "B": "lock catalog-session checksum timestamp index index-guard"}[arm]
                + " begin history-insert commit unlock").split()
    return ({"A": "lock catalog init", "B": "lock catalog-session init"}[arm]
            + " begin update history-insert commit unlock").split()


def interval_union(events):
    covered = end = 0
    for start, stop in sorted((e["offset_ns"], e["offset_ns"] + e["duration_ns"]) for e in events):
        covered += max(0, stop - max(start, end))
        end = max(end, stop)
    return covered


def validate_stage_trace(raw, arm):
    parts = {}
    for line in raw.splitlines():
        if not line.startswith("migration-stage "):
            continue
        row = json.loads(line.removeprefix("migration-stage "))
        key = row["leaf"], row["cycle"], row["phase"]
        if key in parts or row["duration_ns"] < 0:
            raise RuntimeError(f"duplicate or invalid phase {key}")
        parts[key] = row
        events = sorted(row["events"], key=lambda e: e["offset_ns"])
        for e in events:
            if (e["category"] not in ("query", "prepare", "pool") or e["step"] == "unknown"
                or e.get("error") or e.get("pid", 0) <= 0 or e["offset_ns"] < 0
                or e["duration_ns"] < 0 or e["offset_ns"] + e["duration_ns"] > row["duration_ns"]):
                raise RuntimeError(f"invalid SQL or timing evidence at {key}: {e}")
        if key[2] == "up":
            queries = [e["step"] for e in events if e["category"] == "query"]
            if queries != expected_queries(arm, key[0]) or not any(e["category"] == "pool" for e in events):
                raise RuntimeError(f"unexpected Runner.Up stages at {key}: {queries}")
        if key[2] == "open" and not any(e["category"] == "pool" for e in events):
            raise RuntimeError(f"missing acquisition record at {key}")
        if key[2] in ("inspect", "verify-reset") and events:
            raise RuntimeError(f"observer/reset joined the measured SQL trace at {key}")
        row["wall_outside_tracer_ns"] = row["duration_ns"] - interval_union(events)
    required = {(leaf, cycle, phase) for leaf in LEAVES for cycle in range(13)
                for phase in ("open", "inspect", "up", "verify-reset")
                if phase != "open" or leaf == LEAVES[1] or cycle == 0}
    if set(parts) != required or not re.search(r"^PASS\s*$", raw, re.MULTILINE):
        raise RuntimeError("stage trace does not contain exactly the required 92 phases and terminal PASS")
    return list(parts.values())
