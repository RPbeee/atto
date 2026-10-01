"""Validate distribution formats and release version gates without cross compiling."""
import hashlib
import importlib.util
from pathlib import Path
import subprocess
import sys
import tarfile
import tempfile
import unittest
import zipfile

spec = importlib.util.spec_from_file_location('release', Path(__file__).with_name('release.py'))
release = importlib.util.module_from_spec(spec)
spec.loader.exec_module(release)


class ReleaseTests(unittest.TestCase):
    def test_archive_contents_permissions_and_reproducibility(self):
        files = {'atto': (b'binary', 0o755), 'README.md': (b'usage', 0o644),
                 'LICENSES/dependency/LICENSE': (b'license text', 0o644)}
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            for extension in ('.tar.gz', '.zip'):
                first, second = root / ('one' + extension), root / ('two' + extension)
                release.archive(first, 'atto_test', files)
                release.archive(second, 'atto_test', files)
                self.assertEqual(hashlib.sha256(first.read_bytes()).digest(),
                                 hashlib.sha256(second.read_bytes()).digest())
                expected = ['atto_test/' + name for name in sorted(files)]
                if extension == '.zip':
                    with zipfile.ZipFile(first) as archive:
                        self.assertEqual(archive.namelist(), expected)
                        self.assertEqual(archive.read('atto_test/atto'), b'binary')
                        self.assertEqual(archive.getinfo('atto_test/atto').external_attr >> 16 & 0o777,
                                         0o755)
                else:
                    with tarfile.open(first) as archive:
                        self.assertEqual(archive.getnames(), expected)
                        binary = archive.getmember('atto_test/atto')
                        self.assertEqual(binary.mode, 0o755)
                        self.assertEqual(binary.mtime, 0)
                        self.assertEqual(archive.extractfile(binary).read(), b'binary')

    def test_invalid_or_mismatched_tag_stops_before_build(self):
        script = str(Path(__file__).with_name('release.py'))
        for version in ('bad', 'v0.0.0', '../v0.3.0', 'v01.3.0'):
            with self.subTest(version=version):
                process = subprocess.run([sys.executable, script, '--version', version],
                                         capture_output=True, text=True)
                self.assertEqual(process.returncode, 2)


if __name__ == '__main__':
    unittest.main()
