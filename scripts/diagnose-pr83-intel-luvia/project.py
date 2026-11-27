"""Build an exact test-source overlay for one of the three #83 diagnostic roots."""
import hashlib
import json
from pathlib import Path
import re
import subprocess

HERE = Path(__file__).resolve().parent


def git(repo, *args):
    return subprocess.check_output(["git", "--no-optional-locks", "--no-replace-objects", "-C", repo, *args])


def sha(data):
    return hashlib.sha256(data).hexdigest()


def project(checkout, destination, spec, family):
    config = spec["families"][family]
    package = checkout / config["package"]
    revision = git(checkout, "rev-parse", "HEAD").decode().strip()
    if revision not in spec["revisions"]:
        raise ValueError(f"unexpected A/H commit: {revision}")
    all_paths = tuple(sorted(package.glob("*_test.go")))
    if not all_paths or any(not p.is_file() or p.is_symlink() for p in all_paths):
        raise ValueError("unsupported test fixture type")
    inventory = {}
    entries = git(checkout, "ls-tree", "-z", revision, config["package"] + "/")
    for item in filter(None, entries.split(b"\0")):
        meta, relative = item.split(b"\t", 1)
        mode, kind, digest = meta.split()
        filename = Path(relative.decode()).name
        if filename.endswith("_test.go"):
            if kind != b"blob" or mode not in (b"100644", b"100755"):
                raise ValueError(f"invalid tracked fixture type: {filename}")
            inventory[filename] = digest
    if {p.name for p in all_paths} != inventory.keys():
        raise ValueError("test tree differs from the bound checkout")
    for p in all_paths:
        data = p.read_bytes()
        if re.search(rb"(?m)^func\s+(TestMain|init)\s*\(", data):
            raise ValueError(f"unexpected test setup: {p.name}")
        checksum = hashlib.sha1(f"blob {len(data)}\0".encode() + data).hexdigest().encode()
        if checksum != inventory[p.name]:
            raise ValueError(f"non-exact input file: {p.name}")
    if git(checkout, "status", "--porcelain", "--untracked-files=normal"):
        raise ValueError("tracked or untracked checkout differences")

    destination.mkdir(exist_ok=False)
    files = {str(p): "" for p in all_paths}
    retained = {}
    for name, entry in config["keep"].items():
        original = package / name
        if sha(original.read_bytes()) != entry["inputSHA256"]:
            raise ValueError(f"fixture source changed: {name}")
        if entry["projectedSHA256"] is None:
            retained[name] = entry["inputSHA256"]
            del files[str(original)]
            continue
        generated = (HERE / "fixtures" / family / (name + ".txt")).read_bytes()
        if sha(generated) != entry["projectedSHA256"]:
            raise ValueError(f"bound diagnostic projection changed: {name}")
        generated_path = destination / name
        generated_path.write_bytes(generated)
        retained[name] = sha(generated)
        files[str(original)] = str(generated_path)
    result = destination / "overlay.json"
    result.write_text(json.dumps({"Replace": files}, indent=2) + "\n")
    return {"removed": sum(value == "" for value in files.values()), "projected": {name: retained[name] for name, entry in config["keep"].items()
                                                 if entry["projectedSHA256"] is not None}}, result
