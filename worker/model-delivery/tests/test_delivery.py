import hashlib
import io
import json
import os
from pathlib import Path
import shutil
import subprocess
import sys
import tarfile
import tempfile
import time
import unittest
from unittest import mock
from urllib.error import HTTPError

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))
from artifacts import (ImageBuilder, export_bundle, fetch_source, inspect_oci,
                       recipe_context_digest, runtime_bundle_digest, verify_bundle)
from common import ApiError, Commands, DeliveryError, Journal, JsonClient, TransportError, canonical, digest, file_digest
from delivery_worker import AgentDP, ClaimGuard, Worker, validate_probe


class Guard:
    def check(self):
        pass

    def set_phase(self, phase):
        pass


def fixture_job():
    recipe = {'id': 'prepared-yolo-v1', 'version': '1', 'baseImage': 'runtime@sha256:' + 'a' * 64,
              'architecture': 'arm64', 'taskKind': 'camera', 'slot': 'vision', 'templateId': 'yolo',
              'runtimeBackend': 'rknn', 'runtimeProfileDigest': 'sha256:' + 'b' * 64,
              'validation': {'maxLatencyMs': 100, 'maxMemoryBytes': 1024, 'maxErrorRate': .1, 'minQuality': .9}}
    return {'id': 'job-1', 'kind': 'prepare_model', 'recipeId': recipe['id'], 'recipe': recipe,
            'recipeDigest': digest(recipe), 'deviceId': 'device-a', 'validationTaskId': 'test-a',
            'source': {'uri': 'file:///models/model.rknn', 'format': 'rknn',
                       'modelId': 'yolo-test', 'sha256': 'sha256:' + 'c' * 64}}


def make_oci(path, architecture='arm64', corrupt=False):
    blobs = {}
    def add(body, media_type):
        sha = hashlib.sha256(body).hexdigest()
        blobs['blobs/sha256/' + sha] = body
        return {'mediaType': media_type, 'digest': 'sha256:' + sha, 'size': len(body)}
    config = add(canonical({'os': 'linux', 'architecture': architecture}),
                 'application/vnd.oci.image.config.v1+json')
    layer = add(b'actual-layer-bytes', 'application/vnd.oci.image.layer.v1.tar')
    manifest = add(canonical({'schemaVersion': 2, 'config': config, 'layers': [layer]}),
                   'application/vnd.oci.image.manifest.v1+json')
    files = {'oci-layout': canonical({'imageLayoutVersion': '1.0.0'}),
             'index.json': canonical({'schemaVersion': 2, 'manifests': [manifest]}), **blobs}
    if corrupt:
        files['blobs/sha256/' + layer['digest'][7:]] = b'tampered'
    with tarfile.open(path, 'w') as archive:
        for name, body in files.items():
            member = tarfile.TarInfo(name)
            member.size = len(body)
            archive.addfile(member, io.BytesIO(body))
    return manifest['digest']


class DeliveryTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.root = Path(self.temp.name)

    def tearDown(self):
        self.temp.cleanup()

    def test_journal_freezes_target_and_model_across_claim_recovery(self):
        path = self.root / 'journal.db'
        journal = Journal(path)
        job = fixture_job()
        state = journal.bind(job)
        state['nativeJobId'] = 'native-original'
        journal.save(job['id'], state)
        journal.close()
        journal = Journal(path)
        self.assertEqual(journal.bind(job)['nativeJobId'], 'native-original')
        job['source']['sha256'] = 'sha256:' + 'd' * 64
        with self.assertRaisesRegex(DeliveryError, 'inputs changed'):
            journal.bind(job)
        journal.close()

    def test_prepared_source_requires_hash_and_approved_origin(self):
        models = self.root / 'models'
        models.mkdir()
        model = models / 'model.rknn'
        model.write_bytes(b'prepared-real-model')
        source = {'uri': model.as_uri(), 'format': 'rknn', 'sha256': file_digest(model)}
        recipe = {'sourcePrefixes': [models.as_uri()]}
        local = {'inputFormats': ['rknn'], 'sourceRoots': [str(models)]}
        target = self.root / 'out/model.rknn'
        fetch_source(source, recipe, local, target)
        self.assertEqual(target.read_bytes(), model.read_bytes())
        source['sha256'] = 'sha256:' + '0' * 64
        with self.assertRaisesRegex(DeliveryError, 'identity'):
            fetch_source(source, recipe, local, target)
        source.update(uri='https://approved.example.evil/model.rknn')
        with self.assertRaisesRegex(DeliveryError, 'outside'):
            fetch_source(source, {'sourcePrefixes': ['https://approved.example']}, local, target)
        source.update(format='onnx')
        with self.assertRaisesRegex(DeliveryError, 'does not convert'):
            fetch_source(source, recipe, local, target)

    def test_native_lost_confirmation_resumes_same_plan_and_single_job(self):
        journal = Journal(self.root / 'journal.db')
        job = fixture_job()
        state = journal.bind(job)
        local = {'agentdpDeployment': 'approved-yolo', 'nativeModelIdPath': 'modelId',
                 'nativeModelHashPath': 'modelHash'}
        class Agent:
            def __init__(self):
                self.submitted = False
                self.confirmations = 0
            def request(self, path, data=None):
                if path == '/v1/sessions':
                    return {'session_id': 'sess-a'}
                if path == '/v1/device-plans':
                    return {'id': 'plan-a'}
                if path.endswith('/confirm'):
                    self.confirmations += 1
                    self.submitted = True
                    raise TransportError('transport_unknown', 'lost reply')
                if path == '/v1/device-plans/plan-a':
                    return {'id': 'plan-a', 'state': 'submitted' if self.submitted else 'ready',
                            'session_id': 'sess-a', 'device': 'rk-test',
                            'job_id': 'native-a' if self.submitted else None}
                if path.endswith('/verify'):
                    return {'verdict': 'succeeded'}
                if path == '/v1/device-jobs/native-a':
                    return {'state': 'succeeded', 'lease': {'lease_id': 'native-lease', 'release_info': {'remote_stop_confirmed': True}},
                            'result': {'raw': {'simulated': False,
                            'modelId': job['source']['modelId'], 'modelHash': job['source']['sha256']}}}
                raise AssertionError(path)
        client = Agent()
        adapter = AgentDP(client, Guard(), journal, job, state, local, 'rk-test')
        with self.assertRaises(TransportError):
            adapter.verify_native()
        reloaded = journal.get(job['id'])
        AgentDP(client, Guard(), journal, job, reloaded, local, 'rk-test').verify_native()
        self.assertEqual(client.confirmations, 1)
        self.assertEqual(reloaded['nativeJobId'], 'native-a')
        journal.close()

    def test_ambiguous_plan_is_not_created_again(self):
        journal = Journal(self.root / 'journal.db')
        job = fixture_job()
        state = journal.bind(job)
        state.update(sessionId='session-a', planIntent=True)
        client = mock.Mock()
        client.request.return_value = {'plans': []}
        adapter = AgentDP(client, Guard(), journal, job, state, {'agentdpDeployment': 'approved'}, 'rk-test')
        with self.assertRaisesRegex(DeliveryError, 'reconciliation'):
            adapter.prepare()
        self.assertEqual(client.request.call_count, 1)
        self.assertIsNone(client.request.call_args.kwargs.get('data'))
        journal.close()

    def test_expired_claim_fences_subprocess_before_execution(self):
        guard = ClaimGuard(mock.Mock(), 'job-a', {}, {'leaseExpiresAt': '2000-01-01T00:00:00Z'})
        with mock.patch('common.subprocess.Popen') as process:
            with self.assertRaisesRegex(DeliveryError, 'claim'):
                Commands(guard).run([sys.executable, '-c', 'print(1)'])
            process.assert_not_called()

    def test_control_epoch_change_revokes_claim(self):
        client = mock.Mock()
        client.request.return_value = {'controlEpoch': 'new', 'claimEpoch': 1,
                                       'leaseExpiresAt': '2099-01-01T00:00:00Z'}
        guard = ClaimGuard(client, 'job-a', {'controlEpoch': 'old', 'claimEpoch': 1},
                           {'leaseExpiresAt': '2099-01-01T00:00:00Z'})
        with self.assertRaisesRegex(DeliveryError, 'claim'):
            guard.heartbeat()

    def test_heartbeat_phase_is_progress_only_and_keeps_claim_identity(self):
        client = mock.Mock()
        client.request.return_value = {'leaseExpiresAt': '2099-01-01T00:00:00Z'}
        identity = {'workerId': 'worker-a', 'claimToken': 'private', 'claimEpoch': 4, 'controlEpoch': 'ctl'}
        guard = ClaimGuard(client, 'job-a', identity, {'leaseExpiresAt': '2099-01-01T00:00:00Z'})
        guard.set_phase('building')
        guard.heartbeat()
        self.assertEqual(client.request.call_args.args[1], {**identity, 'phase': 'building'})
        self.assertNotIn('status', client.request.call_args.args[1])
        with self.assertRaises(DeliveryError):
            guard.set_phase('published')

    def test_native_handoff_waits_for_policy_controlled_stop_without_submitting_device_work(self):
        server = mock.Mock()
        server.request.side_effect = [ApiError(409, {'errorCode': 'validation_resource_busy'}),
                                      {'status': 'ready'}]
        journal = Journal(self.root / 'journal.db')
        worker = Worker({'workerId': 'worker-a', 'recipes': []}, server=server, agent=mock.Mock(), journal=journal)
        identity = {'claimToken': 'private', 'claimEpoch': 2, 'controlEpoch': 'ctl', 'workerId': 'worker-a'}
        with mock.patch('delivery_worker.time.sleep'):
            worker.prepare_native(fixture_job(), identity, Guard(), {})
        self.assertEqual(server.request.call_count, 2)
        for call in server.request.call_args_list:
            self.assertTrue(call.args[0].endswith('/execution-leases/stop-validation-task'))
            self.assertEqual(call.args[1], {'jobId': fixture_job()['id'], 'claim': identity})
        worker.agent.request.assert_not_called()
        server.request.side_effect = [ApiError(409, {'errorCode': 'execution_epoch_changed'})]
        with mock.patch('delivery_worker.time.sleep') as sleep, self.assertRaises(ApiError):
            worker.prepare_native(fixture_job(), identity, Guard(), {})
        sleep.assert_not_called()
        journal.close()

    def test_plain_http_machine_errors_preserve_stop_fencing_code(self):
        client = JsonClient('http://127.0.0.1:1')
        client.opener = mock.Mock()
        client.opener.open.side_effect = HTTPError('http://127.0.0.1:1', 409, 'Conflict', {}, io.BytesIO(b'execution_epoch_changed\n'))
        with self.assertRaises(ApiError) as caught:
            client.request('/fixed', {})
        self.assertEqual(caught.exception.code, 'execution_epoch_changed')

    def test_native_success_without_proven_stop_cannot_advance_to_container(self):
        journal = Journal(self.root / 'journal.db')
        job = fixture_job()
        state = journal.bind(job)
        client = mock.Mock()
        client.request.side_effect = [
            {'state': 'succeeded', 'lease': {'lease_id': 'held', 'release_info': {'remote_stop_confirmed': False}},
             'result': {'raw': {'simulated': False, 'modelId': job['source']['modelId'], 'modelHash': job['source']['sha256']}}},
            {'verdict': 'succeeded'}]
        native = AgentDP(client, Guard(), journal, job, state, {}, 'rk-test')
        native.prepare = lambda: 'original-native-job'
        with self.assertRaisesRegex(DeliveryError, 'stopped'):
            native.verify_native()
        self.assertFalse(journal.get(job['id'])['nativeOutcome']['stopped'])
        journal.close()

    def test_new_admin_claim_retries_failed_stage_without_replaying_ambiguous_native(self):
        journal = Journal(self.root / 'journal.db')
        job = fixture_job()
        state = journal.bind(job)
        state.update(attemptClaimEpoch=1, result={'status': 'failed', 'errorCode': 'native_result_pending'},
                     nativeJobId='unknown-outcome', planId='original-plan', buildIntent=True,
                     validationIntent=True, validationOperationId='failed-operation', containerProof={'old': True})
        worker = Worker({'workerId': 'worker-a', 'recipes': []}, server=mock.Mock(), agent=mock.Mock(), journal=journal)
        worker.resume_attempt(job, state, 2)
        self.assertNotIn('result', state)
        self.assertEqual(state['nativeJobId'], 'unknown-outcome')
        self.assertEqual(state['planId'], 'original-plan')
        self.assertNotIn('buildIntent', state)
        self.assertNotIn('validationIntent', state)
        self.assertEqual(state['attemptHistory'][0]['validationOperationId'], 'failed-operation')
        state.update(result={'status': 'failed'}, nativeOutcome={'state': 'failed', 'stopped': True})
        worker.resume_attempt(job, state, 3)
        self.assertNotIn('nativeJobId', state)
        self.assertEqual(state['attemptHistory'][-1]['nativeJobId'], 'unknown-outcome')
        journal.close()

    def test_validation_waits_original_operation_and_rejects_changed_operation(self):
        journal = Journal(self.root / 'journal.db')
        job = fixture_job()
        state = journal.bind(job)
        server = mock.Mock()
        server.request.side_effect = [{'operationId': 'original', 'status': 'dispatching'},
                                      {'operationId': 'replacement', 'status': 'awaiting_observation'}]
        worker = Worker({'workerId': 'worker-a', 'recipes': []}, server=server, agent=mock.Mock(), journal=journal)
        commands = mock.Mock()
        with mock.patch('delivery_worker.time.sleep'), self.assertRaisesRegex(DeliveryError, 'different validation operation'):
            worker.validate_container(job, state, {}, Guard(), commands,
                                      {'containerProbeArgv': ['/approved/probe']}, 'repo@sha256:' + 'f' * 64, 'sha256:' + 'e' * 64)
        commands.run.assert_not_called()
        self.assertEqual(server.request.call_count, 2)
        self.assertTrue(server.request.call_args_list[1].args[0].endswith('/validation/status'))
        journal.close()

    def test_completed_validation_report_is_reused_verbatim_after_finalization_failure(self):
        journal = Journal(self.root / 'journal.db')
        job = fixture_job()
        job['validationOperationId'] = 'passed-operation'
        state = journal.bind(job)
        report = {'jobId': job['id'], 'image': 'repo@sha256:' + 'f' * 64,
                  'releaseDigest': 'sha256:' + 'e' * 64, 'modelHash': job['source']['sha256'], 'digest': 'sha256:' + 'd' * 64}
        state.update(attemptClaimEpoch=1, result={'status': 'failed', 'errorCode': 'backend_unavailable'},
                     validationReport=report, validationOperationId='passed-operation')
        server = mock.Mock()
        server.request.return_value = {'status': 'passed', 'report': report}
        worker = Worker({'workerId': 'worker-a', 'recipes': []}, server=server, agent=mock.Mock(), journal=journal)
        worker.resume_attempt(job, state, 2)
        commands = mock.Mock()
        actual = worker.validate_container(job, state, {'claimEpoch': 2}, Guard(), commands,
                                           {}, report['image'], report['releaseDigest'])
        self.assertEqual(actual, report)
        self.assertEqual(server.request.call_args.args[1]['report'], report)
        commands.run.assert_not_called()
        journal.close()

    def test_lost_result_ack_restart_reuses_result_id_without_execution(self):
        job = fixture_job()
        journal = Journal(self.root / 'journal.db')
        state = journal.bind(job)
        state['result'] = {'resultId': 'durable-result', 'status': 'failed', 'errorCode': 'native_failed'}
        journal.save(job['id'], state)
        journal.save('__active_claim__', {'claim': {'job': job, 'claimToken': 'private', 'claimEpoch': 7,
                                                    'controlEpoch': 'epoch', 'leaseExpiresAt': '2000-01-01T00:00:00Z'}})
        server = mock.Mock()
        server.request.return_value = {'ok': True}
        worker = Worker({'workerId': 'worker-a', 'recipes': []}, server=server, agent=mock.Mock(), journal=journal)
        with mock.patch.object(worker, 'execute') as execute:
            self.assertTrue(worker.run_once())
            execute.assert_not_called()
        self.assertEqual(server.request.call_args.args[1]['resultId'], 'durable-result')
        self.assertEqual(journal.get('__active_claim__'), {})
        journal.close()

    def test_real_subprocess_failure_and_missing_backend_do_not_succeed(self):
        commands = Commands()
        self.assertEqual(commands.run([sys.executable, '-c', 'print("actual output")']).strip(), b'actual output')
        with self.assertRaisesRegex(DeliveryError, 'exited with code 7'):
            commands.run([sys.executable, '-c', 'raise SystemExit(7)'])
        with self.assertRaisesRegex(DeliveryError, 'unavailable'):
            commands.run([str(self.root / 'missing-buildctl')])

    def test_oci_corruption_and_wrong_platform_are_rejected(self):
        image = self.root / 'image.tar'
        expected = make_oci(image)
        self.assertEqual(inspect_oci(image, 'linux/arm64'), expected)
        with self.assertRaisesRegex(DeliveryError, 'platform'):
            inspect_oci(image, 'linux/amd64')
        make_oci(image, corrupt=True)
        with self.assertRaisesRegex(DeliveryError, 'checksum'):
            inspect_oci(image, 'linux/arm64')

    def test_buildkit_command_is_real_pinned_and_registry_is_read_back(self):
        context = self.root / 'recipe'
        context.mkdir()
        (context / 'Dockerfile').write_text('ARG BASE_IMAGE\nFROM ${BASE_IMAGE}\nCOPY payload /models\n')
        model = self.root / 'model'
        model.write_bytes(b'model')
        work = self.root / 'work'
        work.mkdir()
        calls = []
        manifest = {}
        class Backend:
            def run(self, argv, **kwargs):
                calls.append(argv)
                if argv[0] == 'buildctl':
                    image_sha = make_oci(work / 'image.oci.tar')
                    with tarfile.open(work / 'image.oci.tar') as archive:
                        manifest['raw'] = archive.extractfile('blobs/sha256/' + image_sha[7:]).read()
                    return b''
                if argv[1] == 'inspect':
                    return manifest['raw']
                return b''
        config = {'buildkitAddress': 'unix:///run/buildkit.sock', 'registryRepository': 'registry.example.test/models'}
        builder = ImageBuilder(config, Backend())
        local = {'contextDir': str(context), 'contextDigest': recipe_context_digest(context),
                 'runtimeBundleDigest': runtime_bundle_digest()}
        image, sha = builder.build(fixture_job(), local, model, work)
        reference = builder.publish(image, sha, 'job-a')
        self.assertEqual(reference, config['registryRepository'] + '@' + sha)
        self.assertIn('build-arg:BASE_IMAGE=' + fixture_job()['recipe']['baseImage'], calls[0])
        self.assertIn('--preserve-digests', calls[1])
        self.assertEqual(calls[2][-1], 'docker://registry.example.test/models:delivery-job-a')

    def test_probe_cannot_echo_fake_success_or_missing_metrics(self):
        job = fixture_job()
        proof = {'deviceId': job['deviceId'], 'taskId': job['validationTaskId'], 'image': 'repo@sha256:' + 'd' * 64,
                 'modelHash': job['source']['sha256'], 'runtimeProfileDigest': job['recipe']['runtimeProfileDigest'],
                 'nonce': 'probe-nonce', 'simulated': False, 'healthy': True, 'latencyMs': 10,
                 'memoryBytes': 512, 'errorRate': 0, 'quality': .99,
                 'observedAt': time.strftime('%Y-%m-%dT%H:%M:%SZ', time.gmtime())}
        validate_probe(proof, job, proof['image'], 'probe-nonce')
        proof['simulated'] = True
        with self.assertRaises(DeliveryError):
            validate_probe(proof, job, proof['image'], 'probe-nonce')
        proof['simulated'] = False
        del proof['quality']
        with self.assertRaises(DeliveryError):
            validate_probe(proof, job, proof['image'], 'probe-nonce')

    def test_signed_bundle_checks_independent_key_and_tampered_image(self):
        image = self.root / 'image.tar'
        sha = make_oci(image)
        calls = []
        class Crypto:
            def run(self, argv, **kwargs):
                calls.append(argv)
                if argv[1] == 'pkey':
                    return bytes.fromhex('302a300506032b6570032100') + b'p' * 32
                if '-sign' in argv:
                    Path(argv[argv.index('-out') + 1]).write_bytes(b's' * 64)
                return b''
        backend = Crypto()
        bundle = self.root / 'bundle.tar'
        export_bundle(image, {'platform': 'linux/arm64', 'imageDigest': sha}, bundle,
                      'private.key', backend)
        record = verify_bundle(bundle, 'trusted-public.pem', backend)
        self.assertEqual(record['imageDigest'], sha)
        self.assertIn('trusted-public.pem', calls[-1])
        tampered = self.root / 'tampered.tar'
        with tarfile.open(bundle) as source, tarfile.open(tampered, 'w') as output:
            for member in source.getmembers():
                data = source.extractfile(member).read()
                if member.name == 'image.oci.tar':
                    data += b'tampered'
                member.size = len(data)
                output.addfile(member, io.BytesIO(data))
        with self.assertRaisesRegex(DeliveryError, 'checksum'):
            verify_bundle(tampered, 'trusted-public.pem', backend)

    def test_real_ed25519_backend_rejects_wrong_key(self):
        executable = os.environ.get('MODEL_DELIVERY_TEST_OPENSSL') or shutil.which('openssl')
        if not executable:
            self.skipTest('OpenSSL 3 is not installed')
        version = subprocess.run([executable, 'version'], capture_output=True, text=True, check=True).stdout
        if not version.startswith('OpenSSL 3.'):
            self.skipTest('This host has no OpenSSL 3 Ed25519 signing backend')
        commands = Commands()
        private, public, other = self.root / 'key.pem', self.root / 'public.pem', self.root / 'other.pem'
        for key in (private, other):
            commands.run([executable, 'genpkey', '-algorithm', 'ED25519', '-out', str(key)])
        commands.run([executable, 'pkey', '-in', str(private), '-pubout', '-out', str(public)])
        other_public = self.root / 'other-public.pem'
        commands.run([executable, 'pkey', '-in', str(other), '-pubout', '-out', str(other_public)])
        image, bundle = self.root / 'image.tar', self.root / 'bundle.tar'
        sha = make_oci(image)
        export_bundle(image, {'platform': 'linux/arm64', 'imageDigest': sha}, bundle, private, commands, executable)
        self.assertEqual(verify_bundle(bundle, public, commands, executable)['imageDigest'], sha)
        with self.assertRaises(DeliveryError):
            verify_bundle(bundle, other_public, commands, executable)


if __name__ == '__main__':
    unittest.main()
