"""Real BuildKit/OCI publication and signed offline archives."""
from __future__ import annotations

import hashlib
import json
from pathlib import Path, PurePosixPath
import re
import shutil
import tarfile
import tempfile
from urllib.parse import unquote, urlsplit

from common import DeliveryError, atomic_bytes, canonical, file_digest, opener


SHA = re.compile(r'^sha256:[0-9a-f]{64}$')


def require_sha(value):
    if not isinstance(value, str) or not SHA.fullmatch(value):
        raise DeliveryError('invalid_digest', 'Expected a SHA-256 identity')
    return value


def within(path, root):
    try:
        return path.resolve().relative_to(root.resolve())
    except ValueError:
        raise DeliveryError('path_forbidden', 'Path is outside the administrator-approved root') from None


def fetch_source(source, recipe, local_recipe, destination, guard=None):
    """Prepared model bytes only. Conversion adapters must be explicitly installed."""
    if source.get('format') != 'rknn' or source.get('format') not in local_recipe['inputFormats']:
        raise DeliveryError('source_format_unsupported', 'This recipe does not convert the requested model format')
    expected = require_sha(source.get('sha256'))
    uri = source.get('uri')
    def allowed_prefix(prefix):
        approved = urlsplit(prefix)
        return (parsed.scheme == approved.scheme and parsed.netloc == approved.netloc
                and (parsed.path == approved.path or parsed.path.startswith(approved.path.rstrip('/') + '/')))

    if not isinstance(uri, str):
        raise DeliveryError('source_forbidden', 'Model source is outside the approved recipe sources')
    parsed = urlsplit(uri)
    decoded_path = unquote(parsed.path)
    if '\\' in decoded_path or '\x00' in decoded_path or any(part in ('.', '..') for part in decoded_path.split('/')):
        raise DeliveryError('source_forbidden', 'Model source paths must be normalized')
    if not any(allowed_prefix(prefix) for prefix in recipe['sourcePrefixes']):
        raise DeliveryError('source_forbidden', 'Model source is outside the approved recipe sources')
    maximum = local_recipe.get('maxModelBytes', 4 * 1024 ** 3)
    if parsed.username or parsed.password or parsed.fragment:
        raise DeliveryError('source_forbidden', 'Model source must not embed credentials or fragments')
    if parsed.scheme == 'file' and parsed.netloc in ('', 'localhost'):
        source_file = Path(unquote(parsed.path))
        roots = [Path(root) for root in local_recipe.get('sourceRoots', [])]
        if not any(source_file.resolve().is_relative_to(root.resolve()) for root in roots):
            raise DeliveryError('source_forbidden', 'Local model source is outside approved source roots')
        handle = source_file.open('rb')
    elif parsed.scheme == 'https' and parsed.hostname:
        handle = opener(local_recipe.get('sourceCaFile')).open(uri, timeout=60)
    else:
        raise DeliveryError('source_forbidden', 'Only approved local files and HTTPS model sources are supported')
    destination = Path(destination)
    destination.parent.mkdir(parents=True, exist_ok=True)
    temporary = destination.with_suffix('.partial')
    hasher, size = hashlib.sha256(), 0
    try:
        with handle, temporary.open('wb') as output:
            for block in iter(lambda: handle.read(1024 * 1024), b''):
                if guard:
                    guard.check()
                size += len(block)
                if size > maximum:
                    raise DeliveryError('source_too_large', 'Prepared model exceeds its recipe size budget')
                hasher.update(block)
                output.write(block)
        if not size or 'sha256:' + hasher.hexdigest() != expected:
            raise DeliveryError('source_hash_mismatch', 'Prepared model bytes do not match the requested identity')
        temporary.replace(destination)
    finally:
        temporary.unlink(missing_ok=True)
    return size


def recipe_context_digest(root):
    root = Path(root)
    files = []
    for path in sorted(root.rglob('*')):
        if path.is_symlink() or not (path.is_file() or path.is_dir()):
            raise DeliveryError('recipe_context_unsafe', 'Recipe contexts must contain regular files and directories')
        if path.is_file():
            files.append({'path': path.relative_to(root).as_posix(), 'sha256': file_digest(path)})
    return 'sha256:' + hashlib.sha256(canonical(files)).hexdigest()


def runtime_bundle_files():
    root = Path(__file__).resolve().parent
    shared = root.parent / 'business-events'
    files = {name: root / name for name in ('runtime_service.py', 'common.py', 'runtime_health.py')}
    files.update({name: shared / name for name in ('runtime_observer.py', 'model_receipt.py', 'adapter.py',
                                                  'voice_asgi.py', 'edgestack_events.py', 'asr_compat.py', 'rk_device_tree.py')})
    return files


def runtime_bundle_digest():
    return 'sha256:' + hashlib.sha256(canonical([
        {'path': name, 'sha256': file_digest(path)} for name, path in sorted(runtime_bundle_files().items())
    ])).hexdigest()


def regular_members(archive):
    result = {}
    for member in archive.getmembers():
        name = member.name.rstrip('/')
        path = PurePosixPath(name)
        if (not name or path.is_absolute() or '\\' in name or any(p in ('.', '..') for p in name.split('/'))
                or not (member.isfile() or member.isdir()) or name in result):
            raise DeliveryError('archive_unsafe', 'Archive contains an unsafe or duplicate member')
        result[name] = member
    return result


def inspect_oci(path, platform):
    """Hash-check OCI blobs and the platform without extracting untrusted paths."""
    with tarfile.open(path, 'r:*') as archive:
        members = regular_members(archive)
        total = sum(item.size for item in members.values())
        if total > 64 * 1024 ** 3:
            raise DeliveryError('archive_too_large', 'OCI archive exceeds the worker limit')

        def read_json(name):
            member = members.get(name)
            if member is None or not member.isfile() or member.size > 4 * 1024 * 1024:
                raise DeliveryError('oci_invalid', 'OCI descriptor is missing or too large')
            return json.load(archive.extractfile(member))

        for name, member in members.items():
            if not name.startswith('blobs/'):
                continue
            if member.isdir():
                continue
            if not re.fullmatch(r'blobs/sha256/[0-9a-f]{64}', name):
                raise DeliveryError('oci_invalid', 'Unsupported OCI blob identity')
            hasher = hashlib.sha256()
            with archive.extractfile(member) as stream:
                for block in iter(lambda: stream.read(1024 * 1024), b''):
                    hasher.update(block)
            if hasher.hexdigest() != name.rsplit('/', 1)[-1]:
                raise DeliveryError('oci_hash_mismatch', 'OCI blob checksum mismatch')

        def descriptor(value):
            sha = require_sha(value.get('digest'))
            name = 'blobs/sha256/' + sha[7:]
            member = members.get(name)
            if member is None or member.size != value.get('size'):
                raise DeliveryError('oci_invalid', 'OCI descriptor size or blob is missing')
            return name

        if read_json('oci-layout') != {'imageLayoutVersion': '1.0.0'}:
            raise DeliveryError('oci_invalid', 'Unsupported OCI layout')
        index = read_json('index.json')
        manifests = index.get('manifests', [])
        if len(manifests) != 1:
            raise DeliveryError('oci_platform_unsupported', 'Delivery recipes build one platform per artifact')
        manifest = read_json(descriptor(manifests[0]))
        config = read_json(descriptor(manifest.get('config', {})))
        if '%s/%s' % (config.get('os'), config.get('architecture')) != platform:
            raise DeliveryError('oci_platform_mismatch', 'OCI image platform does not match the approved recipe')
        if not manifest.get('layers'):
            raise DeliveryError('oci_invalid', 'OCI image has no layers')
        for layer in manifest['layers']:
            descriptor(layer)
        return require_sha(manifests[0]['digest'])


class ImageBuilder:
    def __init__(self, config, commands):
        self.config = config
        self.commands = commands

    def build(self, job, local_recipe, model_file, workspace):
        recipe = job['recipe']
        base = recipe['baseImage']
        if '@sha256:' not in base or not SHA.fullmatch('sha256:' + base.rsplit('@sha256:', 1)[-1]):
            raise DeliveryError('base_image_unpinned', 'Recipe base images must be pinned by digest')
        context = Path(local_recipe['contextDir']).resolve()
        if recipe_context_digest(context) != local_recipe['contextDigest']:
            raise DeliveryError('recipe_context_changed', 'The approved recipe build context changed')
        workspace = Path(workspace)
        build_context = workspace / 'context'
        if build_context.exists():
            shutil.rmtree(build_context)
        shutil.copytree(context, build_context)
        if runtime_bundle_digest() != local_recipe.get('runtimeBundleDigest'):
            raise DeliveryError('runtime_adapter_changed', 'The installed runtime adapter differs from its approval digest')
        runtime = build_context / 'delivery_runtime'
        if runtime.exists():
            raise DeliveryError('recipe_context_unsafe', 'The runtime adapter destination must be reserved')
        runtime.mkdir()
        for name, path in runtime_bundle_files().items():
            shutil.copyfile(path, runtime / name)
        atomic_bytes(runtime / 'candidate.json', canonical({
            'modelId': job['source']['modelId'], 'modelHash': job['source']['sha256'],
            'slot': recipe['slot']}))
        model_path = local_recipe.get('modelPath', 'payload/model.rknn')
        if PurePosixPath(model_path).is_absolute() or '..' in PurePosixPath(model_path).parts:
            raise DeliveryError('recipe_context_unsafe', 'The configured model destination is invalid')
        target = build_context / model_path
        target.parent.mkdir(parents=True, exist_ok=True)
        shutil.copyfile(model_file, target)
        output = workspace / 'image.oci.tar'
        output.unlink(missing_ok=True)
        argv = [self.config.get('buildctl', 'buildctl'), '--addr', self.config['buildkitAddress'],
                'build', '--frontend', 'dockerfile.v0', '--local', 'context=' + str(build_context),
                '--local', 'dockerfile=' + str(build_context), '--opt', 'filename=Dockerfile',
                '--opt', 'platform=linux/' + recipe['architecture'], '--opt', 'build-arg:BASE_IMAGE=' + base,
                '--output', 'type=oci,dest=' + str(output)]
        self.commands.run(argv, timeout=local_recipe.get('buildTimeoutSeconds', 1800))
        manifest_digest = inspect_oci(output, 'linux/' + recipe['architecture'])
        return output, manifest_digest

    def publish(self, oci_file, manifest_digest, job_id):
        repository = self.config['registryRepository']
        if (not re.fullmatch(r'[A-Za-z0-9.:-]+/[a-z0-9/_-]+', repository)
                or not re.fullmatch(r'[A-Za-z0-9_-]{1,128}', job_id)):
            raise DeliveryError('invalid_registry_repository', 'Registry repository or job identity is invalid')
        target = repository + ':delivery-' + job_id
        argv = [self.config.get('skopeo', 'skopeo'), 'copy', '--preserve-digests']
        auth = self.config.get('registryAuthFile')
        if auth:
            argv += ['--authfile', auth]
        argv += ['oci-archive:' + str(oci_file), 'docker://' + target]
        self.commands.run(argv, timeout=self.config.get('registryTimeoutSeconds', 1800))
        self.verify_remote(target, manifest_digest)
        return repository + '@' + manifest_digest

    def verify_remote(self, image, expected):
        argv = [self.config.get('skopeo', 'skopeo'), 'inspect', '--raw']
        if self.config.get('registryAuthFile'):
            argv += ['--authfile', self.config['registryAuthFile']]
        raw = self.commands.run(argv + ['docker://' + image], timeout=120)
        if 'sha256:' + hashlib.sha256(raw).hexdigest() != expected:
            raise DeliveryError('registry_digest_mismatch', 'The LAN registry did not return the built manifest')


def check_ed25519_key(path, commands, openssl, public=False):
    arguments = [openssl, 'pkey'] + (['-pubin'] if public else [])
    encoded = commands.run(arguments + ['-in', str(path), '-pubout', '-outform', 'DER'], timeout=30)
    # SubjectPublicKeyInfo for OID 1.3.101.112 has one 32-byte Ed25519 public key.
    if len(encoded) != 44 or not encoded.startswith(bytes.fromhex('302a300506032b6570032100')):
        raise DeliveryError('signature_key_invalid', 'The configured key must use Ed25519')


def export_bundle(image_archive, manifest, output, signing_key, commands, openssl='openssl'):
    check_ed25519_key(signing_key, commands, openssl)
    output = Path(output)
    output.parent.mkdir(parents=True, exist_ok=True)
    with tempfile.TemporaryDirectory(dir=str(output.parent)) as directory:
        directory = Path(directory)
        body = {**manifest, 'schemaVersion': 1, 'imageArchiveSha256': file_digest(image_archive)}
        record = directory / 'manifest.json'
        signature = directory / 'manifest.ed25519'
        atomic_bytes(record, canonical(body))
        commands.run([openssl, 'pkeyutl', '-sign', '-rawin', '-inkey', str(signing_key),
                      '-in', str(record), '-out', str(signature)], timeout=30)
        if signature.stat().st_size != 64:
            raise DeliveryError('signature_invalid', 'The configured signing key is not Ed25519')
        temporary = directory / 'bundle.tar'
        with tarfile.open(temporary, 'w') as archive:
            archive.add(record, arcname='manifest.json', recursive=False)
            archive.add(signature, arcname='manifest.ed25519', recursive=False)
            archive.add(image_archive, arcname='image.oci.tar', recursive=False)
        temporary.replace(output)
    return file_digest(output)


def verify_bundle(path, trusted_public_key, commands, openssl='openssl'):
    """The verification key is supplied by the administrator, never by the bundle."""
    check_ed25519_key(trusted_public_key, commands, openssl, public=True)
    with tarfile.open(path, 'r:*') as archive:
        members = regular_members(archive)
        if set(members) != {'manifest.json', 'manifest.ed25519', 'image.oci.tar'}:
            raise DeliveryError('bundle_invalid', 'Offline bundle must contain its manifest, signature and OCI archive')
        if members['manifest.json'].size > 1024 * 1024 or members['manifest.ed25519'].size != 64:
            raise DeliveryError('bundle_invalid', 'Offline bundle metadata exceeds its limit')
        if members['image.oci.tar'].size > 64 * 1024 ** 3:
            raise DeliveryError('bundle_invalid', 'Offline image exceeds its limit')
        with tempfile.TemporaryDirectory() as directory:
            directory = Path(directory)
            for name, member in members.items():
                with archive.extractfile(member) as source, (directory / name).open('wb') as target:
                    shutil.copyfileobj(source, target, length=1024 * 1024)
            commands.run([openssl, 'pkeyutl', '-verify', '-rawin', '-pubin', '-inkey', str(trusted_public_key),
                          '-in', str(directory / 'manifest.json'), '-sigfile', str(directory / 'manifest.ed25519')], timeout=30)
            manifest = json.loads((directory / 'manifest.json').read_bytes())
            if (manifest.get('schemaVersion') != 1
                    or file_digest(directory / 'image.oci.tar') != manifest.get('imageArchiveSha256')):
                raise DeliveryError('bundle_hash_mismatch', 'Signed offline image checksum mismatch')
            actual = inspect_oci(directory / 'image.oci.tar', manifest['platform'])
            if actual != manifest.get('imageDigest'):
                raise DeliveryError('bundle_hash_mismatch', 'Signed image identity does not match the OCI archive')
            return manifest


def import_bundle(path, trusted_public_key, image_builder, commands, openssl='openssl'):
    manifest = verify_bundle(path, trusted_public_key, commands, openssl)
    with tempfile.TemporaryDirectory() as directory:
        image = Path(directory) / 'image.oci.tar'
        with tarfile.open(path, 'r:*') as archive, image.open('wb') as destination:
            shutil.copyfileobj(archive.extractfile('image.oci.tar'), destination, length=1024 * 1024)
        # Verify the copied bytes too. The bundle might have changed between the two reads.
        if file_digest(image) != manifest['imageArchiveSha256']:
            raise DeliveryError('bundle_hash_mismatch', 'Bundle changed during import')
        return {'image': image_builder.publish(image, manifest['imageDigest'], manifest['jobId']),
                'manifest': manifest}
