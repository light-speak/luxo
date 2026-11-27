"""Keep the signed JSONList benchmark declarations at their original positions."""

from hashlib import sha256
import json


def project(source, manifest):
    lines = source.splitlines(keepends=True)
    if (len(source) != manifest["sourceSize"] or len(lines) != manifest["sourceLines"] or
            not all(line.endswith(b"\n") for line in lines) or
            sha256(source).hexdigest() != manifest["sourceSHA256"]):
        raise ValueError("JSONList fixture source or dimensions changed")
    retained = set()
    last = 0
    for first, stop in manifest["retainedLines"]:
        if not last < first <= stop <= len(lines):
            raise ValueError("invalid compile fixture line span")
        retained.update(range(first, stop + 1))
        last = stop
    projected = b"".join(line if index in retained else b" " * (len(line)-1) + b"\n"
                         for index, line in enumerate(lines, 1))
    if len(projected) != len(source) or sha256(projected).hexdigest() != manifest["projectionSHA256"]:
        raise ValueError("unexpected JSONList fixture projection")
    return projected


def overlay(source, package, location, manifest):
    original = source / manifest["path"]
    if original.parent != source / package:
        raise ValueError("JSONList fixture is outside its benchmark package")
    projected = location / original.name
    projected.write_bytes(project(original.read_bytes(), manifest))
    entries = {path: "" for path in (source/package).glob("*_test.go")}
    if original not in entries:
        raise ValueError("missing original JSONList fixture")
    entries[original] = str(projected)
    overlay_path = location / "overlay.json"
    overlay_path.write_text(json.dumps({"Replace": {str(path): replacement for path, replacement in entries.items()}},
                                       sort_keys=True) + "\n")
    receipt = {str(path.relative_to(source)): manifest["projectionSHA256"] if path == original else ""
               for path in entries}
    return overlay_path, receipt
