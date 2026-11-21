import hashlib
import json
from pathlib import Path
import tempfile
import tarfile
import unittest
from evidence import save_evidence


class EvidenceTests(unittest.TestCase):
    def test_inputs_survive_cleanup_and_mutation(self):
        scratch = Path(__file__).resolve().parents[3] / '.tmp'
        with tempfile.TemporaryDirectory(dir=scratch) as directory:
            root = Path(directory)
            output = root / 'report'; output.mkdir()
            source = root / 'source.dart'; source.write_text('original')
            binary = root / 'consumer'; binary.write_bytes(b'compiled')
            save_evidence([source, binary, source], output, 'dart', root)
            source.write_text('changed'); binary.unlink()
            hashes = json.loads((output / 'dart-sha256.json').read_text())
            with tarfile.open(output / 'dart-inputs.tar.gz') as archive:
                self.assertEqual(sorted(archive.getnames()), ['consumer', 'source.dart'])
                for member in archive:
                    data = archive.extractfile(member).read()
                    self.assertEqual(hashlib.sha256(data).hexdigest(), hashes[str(root / member.name)])

    def test_external_files_are_identified_without_archiving(self):
        scratch = Path(__file__).resolve().parents[3] / '.tmp'
        with tempfile.TemporaryDirectory(dir=scratch) as directory:
            base = Path(directory); root = base / 'root'; root.mkdir()
            external = base / 'dependency.jar'; external.write_bytes(b'jar')
            link = root / 'dependency.jar'; link.symlink_to(external)
            save_evidence([link], root, 'kotlin', root)
            report = json.loads((root / 'kotlin-inputs.json').read_text())
            self.assertEqual(report['external'], [str(link)])
            with tarfile.open(root / 'kotlin-inputs.tar.gz') as archive:
                self.assertEqual(archive.getnames(), [])

    def test_missing_input_is_not_silently_ignored(self):
        scratch = Path(__file__).resolve().parents[3] / '.tmp'
        with tempfile.TemporaryDirectory(dir=scratch) as directory:
            root = Path(directory)
            with self.assertRaises(FileNotFoundError):
                save_evidence([root / 'missing'], root, 'dart', root)
