#!/usr/bin/env python3
"""Build reproducible standalone release archives with licenses and SHA-256 sums."""
import argparse
import gzip
import hashlib
import io
import json
import os
import platform
from pathlib import Path
import re
import subprocess
import tarfile
import tempfile
import zipfile

ROOT = Path(__file__).resolve().parent.parent
TARGETS = ('linux/amd64', 'linux/arm64', 'darwin/amd64', 'darwin/arm64',
           'windows/amd64', 'windows/arm64')


def run_go(*args, env=None):
    return subprocess.check_output([os.environ.get('GO', 'go'), *args], cwd=ROOT,
                                   env=env, text=True)


def module_licenses():
    output = run_go('list', '-buildvcs=false', '-deps', '-json', '.')
    decoder = json.JSONDecoder()
    modules, files, seen = [], {}, set()
    while output.strip():
        package, end = decoder.raw_decode(output.lstrip())
        output = output.lstrip()[end:]
        module = package.get('Module')
        if not module or module.get('Main') or module['Path'] in seen:
            continue
        seen.add(module['Path'])
        folder = Path(module['Dir'])
        licenses = sorted(p for p in folder.iterdir() if p.is_file() and
                          p.name.upper().startswith(('LICENSE', 'COPYING', 'NOTICE')))
        if not licenses:
            raise RuntimeError(f"license file missing for {module['Path']}")
        name = module['Path'].replace('/', '__') + '@' + module['Version']
        names = []
        for path in licenses:
            destination = 'LICENSES/' + name + '/' + path.name
            files[destination] = (path.read_bytes(), 0o644)
            names.append(destination)
        modules.append({'module': module['Path'], 'version': module['Version'],
                        'licenses': names})
    goroot = Path(run_go('env', 'GOROOT').strip())
    files['LICENSES/go/LICENSE'] = ((goroot / 'LICENSE').read_bytes(), 0o644)
    manifest = {'go': run_go('version').strip(), 'modules': modules}
    files['THIRD_PARTY_NOTICES.json'] = ((json.dumps(manifest, ensure_ascii=False,
                                                   indent=2) + '\n').encode(), 0o644)
    return files


def archive(path, prefix, files):
    if path.suffix == '.zip':
        with zipfile.ZipFile(path, 'w', compression=zipfile.ZIP_DEFLATED,
                             compresslevel=9) as out:
            for name, (data, mode) in sorted(files.items()):
                info = zipfile.ZipInfo(prefix + '/' + name, (1980, 1, 1, 0, 0, 0))
                info.create_system = 3
                info.external_attr = (0o100000 | mode) << 16
                info.compress_type = zipfile.ZIP_DEFLATED
                out.writestr(info, data)
    else:
        with path.open('wb') as raw:
            with gzip.GzipFile(filename='', fileobj=raw, mode='wb', mtime=0,
                               compresslevel=9) as compressed:
                with tarfile.open(fileobj=compressed, mode='w', format=tarfile.GNU_FORMAT) as out:
                    for name, (data, mode) in sorted(files.items()):
                        info = tarfile.TarInfo(prefix + '/' + name)
                        info.size, info.mode, info.mtime = len(data), mode, 0
                        out.addfile(info, io.BytesIO(data))


def build(version, targets, output):
    output.mkdir(parents=True, exist_ok=True)
    if any(output.iterdir()):
        raise ValueError('output directory must be empty; choose a new --output directory')
    common = module_licenses()
    for name in ('README.md', 'LICENSE', 'CHANGELOG.md', 'docs/SPEC.md'):
        path = ROOT / name
        if path.exists():
            common[name] = (path.read_bytes(), 0o644)
    with tempfile.TemporaryDirectory(prefix='atto-release-') as temporary:
        stage = Path(temporary)
        for target in targets:
            goos, goarch = target.split('/')
            filename = 'atto.exe' if goos == 'windows' else 'atto'
            binary = stage / filename
            env = dict(os.environ, GOOS=goos, GOARCH=goarch, CGO_ENABLED='0')
            env.pop('GOARM', None)
            print(f'Building {target}', flush=True)
            run_go('build', '-trimpath', '-buildvcs=false', '-ldflags',
                   '-s -w -X main.version=' + version, '-o', str(binary), '.', env=env)
            if target == 'linux/amd64' and platform.system() == 'Linux' and platform.machine() == 'x86_64':
                actual = subprocess.check_output([str(binary), '-version'], text=True).strip()
                if actual != 'atto ' + version:
                    raise RuntimeError(f'wrong embedded version: {actual}')
            files = dict(common)
            files[filename] = (binary.read_bytes(), 0o755)
            prefix = f'atto_{version}_{goos}_{goarch}'
            extension = '.zip' if goos == 'windows' else '.tar.gz'
            archive(stage / (prefix + extension), prefix, files)
        archives = sorted(p for p in stage.iterdir() if p.name.endswith(('.tar.gz', '.zip')))
        checksums = ''.join(hashlib.sha256(p.read_bytes()).hexdigest() + '  ' + p.name + '\n'
                            for p in archives)
        (stage / 'SHA256SUMS').write_text(checksums, encoding='utf-8')
        # Only completed distributions are copied to the output directory.
        for path in [*archives, stage / 'SHA256SUMS']:
            (output / path.name).write_bytes(path.read_bytes())
    print(f'Release artifacts: {output}', flush=True)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--version', required=True, help='version, with optional leading v')
    parser.add_argument('--targets', default=','.join(TARGETS))
    parser.add_argument('--output', type=Path, default=ROOT / 'dist')
    args = parser.parse_args()
    version = args.version.removeprefix('v')
    if not re.fullmatch(r'(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(?:-[0-9A-Za-z.-]+)?', version):
        parser.error('invalid semantic version')
    source = re.search(r'^var version = "([^"]+)"$', (ROOT / 'main.go').read_text(), re.MULTILINE)
    if source is None or source[1] != version:
        parser.error('tag must match the version in main.go')
    targets = args.targets.split(',')
    if len(set(targets)) != len(targets) or any(target not in TARGETS for target in targets):
        parser.error('unsupported or repeated target')
    build(version, targets, args.output.resolve())


if __name__ == '__main__':
    main()
