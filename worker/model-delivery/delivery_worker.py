#!/usr/bin/env python3
"""Execute leased model-delivery jobs against fixed AgentDP and BuildKit adapters."""
from __future__ import annotations

import argparse
from datetime import datetime, timezone
import fcntl
import json
import math
import os
from pathlib import Path
import re
import sys
import threading
import time
from urllib.parse import urlencode
import uuid

from artifacts import (ImageBuilder, export_bundle, fetch_source, import_bundle, inspect_oci,
                       require_sha, verify_bundle)
from common import (ApiError, Commands, DeliveryError, Journal, JsonClient, TransportError,
                    canonical, digest, file_digest)


INTERNAL = '/api/edgestack/internal/delivery'
TERMINAL = {'succeeded', 'failed', 'cancelled', 'interrupted'}


def timestamp(value):
    try:
        match = re.fullmatch(
            r'([0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2})'
            r'(?:\.([0-9]{1,9}))?(Z|[+-](?:[01][0-9]|2[0-3]):[0-5][0-9])', value)
        if not match:
            raise ValueError('invalid RFC3339 timestamp')
        whole, fraction, zone = match.groups()
        # Python 3.10 accepts only 3/6 fractional digits. Go emits up to nine;
        # discard sub-microseconds so a lease deadline is never rounded later.
        normalized = whole + ('.' + fraction[:6].ljust(6, '0') if fraction else '')
        parsed = datetime.fromisoformat(normalized + ('+00:00' if zone == 'Z' else zone))
        if parsed.tzinfo is None:
            raise ValueError('missing time zone')
        return parsed.timestamp()
    except (AttributeError, TypeError, ValueError):
        raise DeliveryError('invalid_timestamp', 'Expected an ISO8601 timestamp with a timezone') from None


class ClaimGuard:
    def __init__(self, server, job_id, identity, claim):
        self.server, self.job_id, self.identity = server, job_id, identity
        self.expires = timestamp(claim['leaseExpiresAt'])
        self.interval = max(1, min(30, claim.get('heartbeatSeconds', 30)))
        self.lost = threading.Event()
        self.stopped = threading.Event()
        self.thread = None
        self.phase = 'waiting_for_device'

    def set_phase(self, phase):
        if phase not in ('waiting_for_device', 'native_validation', 'building', 'image_validation', 'policy_evaluation'):
            raise DeliveryError('invalid_phase', 'Delivery execution phase is not supported')
        self.check()
        self.phase = phase

    def check(self):
        if self.lost.is_set() or time.time() >= self.expires:
            raise DeliveryError('claim_lost', 'The delivery claim is no longer valid')

    def heartbeat(self):
        self.check()
        response = self.server.request(INTERNAL + '/jobs/' + self.job_id + '/heartbeat',
                                       {**self.identity, 'phase': self.phase})
        if (response.get('controlEpoch', self.identity['controlEpoch']) != self.identity['controlEpoch']
                or response.get('claimEpoch', self.identity['claimEpoch']) != self.identity['claimEpoch']):
            self.lost.set()
            self.check()
        self.expires = timestamp(response['leaseExpiresAt'])

    def start(self):
        self.heartbeat()
        self.thread = threading.Thread(target=self._loop, name='delivery-heartbeat', daemon=True)
        self.thread.start()
        return self

    def _loop(self):
        while not self.stopped.wait(self.interval):
            try:
                self.heartbeat()
            except Exception:
                self.lost.set()
                return

    def close(self):
        self.stopped.set()
        if self.thread:
            self.thread.join(timeout=35)


def object_path(value, keys):
    for key in keys.split('.'):
        if not isinstance(value, dict) or key not in value:
            raise DeliveryError('execution_identity_missing', 'Runtime proof is missing a configured identity field')
        value = value[key]
    return value


class AgentDP:
    """Only catalogue, session, plan, confirmation, job and verification routes are used."""
    def __init__(self, client, guard, journal, job, state, local_recipe, device_alias):
        self.client, self.guard, self.journal = client, guard, journal
        self.job, self.state, self.recipe, self.device = job, state, local_recipe, device_alias

    def save(self):
        self.journal.save(self.job['id'], self.state)

    def prepare(self):
        self.guard.check()
        if not self.state.get('sessionId'):
            if self.state.get('sessionIntent'):
                raise DeliveryError('agent_session_unknown', 'Read back the previous session creation before retrying')
            self.state['sessionIntent'] = True
            self.save()
            session = self.client.request('/v1/sessions', {'language': 'zh'})
            self.state['sessionId'] = session['session_id']
            self.save()
        session_id = self.state['sessionId']
        if not self.state.get('planId'):
            marker = self.state.get('planMarker') or ('edgestack-delivery:' + self.job['id'] + ':' +
                                                      str(self.state.get('attemptClaimEpoch', 0)))
            self.state['planMarker'] = marker
            if self.state.get('planIntent'):
                plans = self.client.request('/v1/device-plans?' + urlencode({'session_id': session_id}))['plans']
                matches = [plan for plan in plans if plan.get('requested_by') == marker
                           and plan.get('session_id') == session_id and plan.get('device') == self.device
                           and plan.get('deployment') == self.recipe['agentdpDeployment']]
                if len(matches) != 1:
                    raise DeliveryError('agent_plan_unknown', 'Previous plan creation needs explicit reconciliation')
                plan = matches[0]
            else:
                self.state['planIntent'] = True
                self.save()
                plan = self.client.request('/v1/device-plans', {
                    'session_id': session_id, 'device': self.device,
                    'deployment': self.recipe['agentdpDeployment'], 'requested_by': marker,
                })
            self.state['planId'] = plan['id']
            self.save()
        plan_id = self.state['planId']
        self.guard.check()
        plan = self.client.request('/v1/device-plans/' + plan_id)
        if plan.get('session_id') != session_id or plan.get('device') != self.device:
            raise DeliveryError('agent_plan_identity_mismatch', 'AgentDP plan no longer matches the validation device')
        existing_job = self.state.get('nativeJobId')
        if existing_job and plan.get('job_id') != existing_job:
            raise DeliveryError('agent_plan_identity_mismatch', 'AgentDP plan no longer identifies the journaled job')
        if not existing_job:
            if plan.get('job_id'):
                self.state['nativeJobId'] = plan['job_id']
            else:
                if plan.get('state') not in ('ready', 'confirmed'):
                    raise DeliveryError('agent_plan_blocked', 'AgentDP prechecks did not produce a confirmable plan')
                self.state['confirmIntent'] = True
                self.save()
                # EdgeContainer has already authorized this dispatch. AgentDP binds this exact session/plan.
                response = self.client.request('/v1/device-plans/' + plan_id + '/confirm', {'session_id': session_id})
                self.state['nativeJobId'] = response['job']['id']
            self.save()
        return self.state['nativeJobId']

    def verify_native(self):
        self.guard.set_phase('waiting_for_device')
        job_id = self.prepare()
        deadline = time.monotonic() + self.recipe.get('nativeTimeoutSeconds', 1800)
        while True:
            self.guard.check()
            job = self.client.request('/v1/device-jobs/' + job_id)
            self.guard.set_phase('waiting_for_device' if job.get('state') in
                                 ('queued', 'pending', 'scheduled', 'waiting_for_lease', 'waiting_for_device')
                                 else 'native_validation')
            if job.get('state') in TERMINAL:
                break
            if time.monotonic() >= deadline:
                raise DeliveryError('native_result_pending', 'The original AgentDP job still needs a confirmed outcome')
            time.sleep(2)
        raw = (job.get('result') or {}).get('raw') or {}
        lease = job.get('lease') or {}
        release = lease.get('release_info') or {}
        self.state['nativeOutcome'] = {'jobId': job_id, 'state': job.get('state'),
                                       'stopped': bool(lease.get('lease_id')) and release.get('remote_stop_confirmed') is True}
        self.save()
        proof = self.client.request('/v1/device-jobs/' + job_id + '/verify')
        if (job.get('state') != 'succeeded' or proof.get('verdict') != 'succeeded'
                or raw.get('simulated') is not False):
            raise DeliveryError('native_verification_failed', 'AgentDP did not verify a successful real native execution')
        if not lease.get('lease_id') or release.get('remote_stop_confirmed') is not True:
            raise DeliveryError('native_stop_unconfirmed', 'AgentDP did not prove its board process stopped before releasing its lease')
        model_id = object_path(raw, self.recipe['nativeModelIdPath'])
        model_hash = object_path(raw, self.recipe['nativeModelHashPath'])
        if isinstance(model_hash, str) and re.fullmatch('[0-9a-f]{64}', model_hash):
            model_hash = 'sha256:' + model_hash
        if model_id != self.job['source']['modelId'] or model_hash != self.job['source']['sha256']:
            raise DeliveryError('native_model_mismatch', 'Native execution used a different model identity')
        self.state['nativeVerified'] = {'jobId': job_id, 'modelId': model_id, 'modelHash': model_hash}
        self.save()


def validate_probe(proof, job, image, nonce):
    expected = {'deviceId': job.get('validationDeviceId', job['deviceId']), 'taskId': job['validationTaskId'], 'image': image,
                'modelHash': job['source']['sha256'], 'runtimeProfileDigest': job['recipe']['runtimeProfileDigest'],
                'nonce': nonce}
    if (not isinstance(proof, dict) or any(proof.get(k) != v for k, v in expected.items())
            or proof.get('simulated') is not False or proof.get('healthy') is not True):
        raise DeliveryError('container_probe_identity_mismatch', 'Container probe did not prove the requested runtime identity')
    for key in ('latencyMs', 'memoryBytes', 'errorRate', 'quality'):
        value = proof.get(key)
        if type(value) not in (int, float) or not math.isfinite(value) or value < 0:
            raise DeliveryError('container_metrics_missing', 'Container probe must return actual finite measurements')
    if proof['errorRate'] > 1 or proof['quality'] > 1 or abs(time.time() - timestamp(proof.get('observedAt'))) > 3600:
        raise DeliveryError('container_metrics_invalid', 'Container metrics are out of range or stale')
    if type(proof['memoryBytes']) is not int or proof['memoryBytes'] <= 0:
        raise DeliveryError('container_metrics_invalid', 'Measured memory must be a positive integer byte count')
    gates = job['recipe']['validation']
    if (proof['latencyMs'] > gates['maxLatencyMs'] or proof['memoryBytes'] > gates['maxMemoryBytes']
            or proof['errorRate'] > gates['maxErrorRate'] or proof['quality'] < gates['minQuality']):
        raise DeliveryError('container_gates_failed', 'The deployed image did not pass the configured validation gates')


class Worker:
    def __init__(self, config, server=None, agent=None, journal=None):
        self.config = config
        self.server = server or JsonClient(**config['server'])
        self.agent = agent or JsonClient(**config['agentdp'])
        self.journal = journal or Journal(Path(config['stateDir']) / 'jobs.sqlite3')
        self.recipes = {item['id']: item for item in config['recipes'] if item.get('enabled') is True}

    def checkpoint(self, job, state, **updates):
        state.update(updates)
        self.journal.save(job['id'], state)

    def resume_attempt(self, job, state, claim_epoch):
        previous = state.get('result') or {}
        if previous.get('status') == 'failed' and state.get('attemptClaimEpoch') != claim_epoch:
            # A fresh Server claim for this failed job is an explicit administrator
            # retry. Keep every external ID in history; never infer remote stopping.
            history = state.setdefault('attemptHistory', [])
            history.append({key: state[key] for key in ('attemptClaimEpoch', 'result', 'sessionId', 'planId',
                                                       'nativeJobId', 'nativeOutcome', 'validationOperationId',
                                                       'validationReport') if key in state})
            state.pop('result', None)
            state.pop('resultAcknowledged', None)
            outcome = state.get('nativeOutcome') or {}
            if (not state.get('nativeVerified') and outcome.get('stopped') is True
                    and outcome.get('state') in ('failed', 'cancelled', 'interrupted')):
                for key in ('sessionIntent', 'sessionId', 'planIntent', 'planId', 'planMarker',
                            'confirmIntent', 'nativeJobId', 'nativeOutcome'):
                    state.pop(key, None)
            if state.get('validationOperationId') and not job.get('validationOperationId'):
                for key in ('validationIntent', 'validationOperationId', 'validationNonce',
                            'containerProof', 'validationReport'):
                    state.pop(key, None)
            # Local builds do not execute on the board. A new authorized attempt
            # may repeat an incomplete build or push of the same frozen bytes.
            if not state.get('imageDigest'):
                state.pop('buildIntent', None)
            if not state.get('image'):
                state.pop('publishIntent', None)
        self.checkpoint(job, state, attemptClaimEpoch=claim_epoch)

    def prepare_native(self, job, identity, guard, local):
        guard.set_phase('waiting_for_device')
        deadline = time.monotonic() + local.get('nativeTimeoutSeconds', 1800)
        while True:
            guard.check()
            try:
                response = self.server.request('/api/edgestack/internal/execution-leases/stop-validation-task',
                                               {'jobId': job['id'], 'claim': identity})
                if response.get('status') != 'ready':
                    raise DeliveryError('validation_handoff_unconfirmed', 'Server did not prove the isolated validation task is stopped')
                return
            except (ApiError, TransportError) as error:
                if isinstance(error, ApiError):
                    if error.status != 409 or error.code not in ('validation_resource_busy', 'validation_stop_unconfirmed'):
                        raise
                # The endpoint is an idempotent, policy-controlled stop of this
                # job's isolated test task. Never stop another serving workload.
                if time.monotonic() >= deadline:
                    raise DeliveryError('validation_handoff_pending', 'The isolated task needs administrator handoff or reconciliation') from error
                time.sleep(3)

    def execute(self, job, state, identity, guard):
        if job.get('kind') != 'prepare_model' or not re.fullmatch(r'[A-Za-z0-9_-]{1,128}', job['id']):
            raise DeliveryError('job_unsupported', 'The worker only accepts model preparation jobs')
        local = self.recipes.get(job['recipeId'])
        recipe_digest = job.get('recipeDigest') or digest({k: v for k, v in job['recipe'].items() if k != 'digest'})
        if local is None or recipe_digest != local['serverRecipeDigest']:
            raise DeliveryError('recipe_not_approved', 'Worker recipe approval does not match the frozen server recipe')
        device = self.config['devices'].get(job.get('validationDeviceId', job['deviceId']))
        if not device or device.get('validationTaskId') != job['validationTaskId'] or not device.get('agentdpDevice'):
            raise DeliveryError('device_mapping_unavailable', 'An approved device and validation task mapping is required')
        workspace = Path(self.config['workDir']) / job['id']
        workspace.mkdir(parents=True, exist_ok=True)
        model = workspace / 'model.rknn'
        if not model.exists() or file_digest(model) != require_sha(job['source']['sha256']):
            fetch_source(job['source'], job['recipe'], local, model, guard)
        if not state.get('nativeVerified'):
            self.prepare_native(job, identity, guard, local)
        AgentDP(self.agent, guard, self.journal, job, state, local, device['agentdpDevice']).verify_native()
        guard.set_phase('building')
        commands = Commands(guard)
        builder = ImageBuilder(self.config, commands)
        image_archive = workspace / 'image.oci.tar'
        if state.get('buildIntent'):
            if not image_archive.is_file():
                raise DeliveryError('build_outcome_unknown', 'Previous BuildKit execution needs reconciliation')
            image_digest = inspect_oci(image_archive, 'linux/' + job['recipe']['architecture'])
        else:
            self.checkpoint(job, state, buildIntent=True)
            image_archive, image_digest = builder.build(job, local, model, workspace)
        if state.get('imageDigest') and state['imageDigest'] != image_digest:
            raise DeliveryError('image_changed', 'The build output changed after its identity was recorded')
        self.checkpoint(job, state, imageDigest=image_digest)
        image = self.config['registryRepository'] + '@' + image_digest
        if state.get('publishIntent'):
            builder.verify_remote(image, image_digest)
        else:
            self.checkpoint(job, state, publishIntent=True)
            image = builder.publish(image_archive, image_digest, job['id'])
        self.checkpoint(job, state, image=image)
        release = {'jobId': job['id'], 'imageDigest': image_digest, 'image': image,
                   'platform': 'linux/' + job['recipe']['architecture'], 'modelId': job['source']['modelId'],
                   'modelHash': job['source']['sha256'], 'recipeDigest': recipe_digest,
                   'runtimeProfileDigest': job['recipe']['runtimeProfileDigest']}
        release_digest = digest(release)
        report = self.validate_container(job, state, identity, guard, commands, local, image, release_digest)
        guard.set_phase('policy_evaluation')
        bundle = Path(self.config['bundleDir']) / (job['id'] + '.tar')
        bundle_sha = export_bundle(image_archive, {**release, 'releaseDigest': release_digest,
                                                  'report': report}, bundle, self.config['signingKeyFile'], commands,
                                   self.config.get('openssl', 'openssl'))
        verify_bundle(bundle, self.config['trustedPublicKeyFile'], commands, self.config.get('openssl', 'openssl'))
        artifact = {'id': 'artifact-' + job['id'], 'jobId': job['id'], 'modelId': job['source']['modelId'],
                    'modelHash': job['source']['sha256'], 'image': image, 'platform': release['platform'],
                    'recipeDigest': recipe_digest, 'runtimeProfileDigest': release['runtimeProfileDigest'],
                    'releaseDigest': release_digest, 'reportDigest': report['digest'], 'registryVerified': True,
                    'offlineBundle': {'uri': bundle.resolve().as_uri(), 'sha256': bundle_sha},
                    'files': [{'path': 'image.oci.tar', 'sha256': file_digest(image_archive),
                               'sizeBytes': image_archive.stat().st_size}]}
        # Server computes artifact/report canonical identities across JSON number/time representations.
        return {'status': 'artifact_ready', 'artifact': artifact, 'report': report}

    def complete_validation(self, path, identity, report, guard, deadline):
        payload = {**identity, 'report': report}
        while True:
            guard.check()
            if time.monotonic() >= deadline:
                raise DeliveryError('validation_completion_timeout', 'Server validation did not become ready before the deadline')
            try:
                response = self.server.request(path + '/complete', payload)
            except (ApiError, TransportError) as error:
                if isinstance(error, ApiError):
                    if error.status != 503 or error.code not in ('model_runtime_unverified', 'delivery_validation_health_failed'):
                        raise
                elif error.code != 'transport_unknown':
                    raise
                guard.check()
                remaining = deadline - time.monotonic()
                if remaining <= 0:
                    raise DeliveryError('validation_completion_timeout', 'Server validation did not become ready before the deadline') from error
                time.sleep(min(2, remaining))
                continue
            guard.check()
            if time.monotonic() >= deadline:
                raise DeliveryError('validation_completion_timeout', 'Server validation did not become ready before the deadline')
            return response

    def validate_container(self, job, state, identity, guard, commands, local, image, release_digest):
        guard.set_phase('image_validation')
        path = INTERNAL + '/jobs/' + job['id'] + '/validation'
        deadline = time.monotonic() + local.get('validationTimeoutSeconds', 600)
        if state.get('validationReport'):
            saved = state['validationReport']
            if (saved.get('jobId') != job['id'] or saved.get('image') != image
                    or saved.get('releaseDigest') != release_digest or saved.get('modelHash') != job['source']['sha256']):
                raise DeliveryError('validation_report_changed', 'Stored validation report differs from the frozen release')
            response = self.complete_validation(path, identity, saved, guard, deadline)
            if response.get('status') != 'passed' or response.get('report') != saved:
                raise DeliveryError('server_validation_mismatch', 'Server did not preserve the completed validation report')
            return saved
        model = {'id': 'delivery-' + job['id'], 'name': job['source']['modelId'],
                 'version': str(job['recipe']['version']) + '-' + job['source']['sha256'][7:19],
                 'kind': job['recipe']['taskKind'], 'slot': job['recipe']['slot'],
                 'taskKinds': [job['recipe']['taskKind']], 'architectures': [job['recipe']['architecture']],
                 'runtimeBackend': job['recipe']['runtimeBackend'], 'templateIds': [job['recipe']['templateId']],
                 'image': image, 'modelId': job['source']['modelId'], 'modelHash': job['source']['sha256'],
                 'immutable': True, 'published': False, 'environment': job['recipe'].get('modelEnvironment', {})}
        if not local.get('containerProbeArgv'):
            raise DeliveryError('validation_probe_unavailable', 'The approved recipe has no container validation probe')
        guard.check()
        if state.get('validationIntent'):
            operation = self.server.request(path + '/status', identity)
        else:
            self.checkpoint(job, state, validationIntent=True)
            operation = self.server.request(path + '/start', {**identity, 'package': model})
        if not operation.get('operationId'):
            raise DeliveryError('validation_outcome_unknown', 'The original validation operation needs reconciliation')
        if state.get('validationOperationId') and state['validationOperationId'] != operation['operationId']:
            raise DeliveryError('validation_operation_changed', 'Server returned a different validation operation')
        self.checkpoint(job, state, validationOperationId=operation['operationId'])
        while operation.get('status') not in ('awaiting_observation', 'succeeded'):
            guard.check()
            if operation.get('status') not in ('dispatching', 'queued', 'pending'):
                raise DeliveryError('validation_dispatch_failed', 'The original validation operation failed or was cancelled')
            if time.monotonic() >= deadline:
                raise DeliveryError('validation_dispatch_timeout', 'The original validation operation still needs reconciliation')
            time.sleep(2)
            operation = self.server.request(path + '/status', identity)
            if operation.get('operationId') != state['validationOperationId']:
                raise DeliveryError('validation_operation_changed', 'Server returned a different validation operation')
        nonce = state.get('validationNonce') or uuid.uuid4().hex
        self.checkpoint(job, state, validationNonce=nonce)
        proof = state.get('containerProof')
        if proof is None:
            values = {'deviceId': job.get('validationDeviceId', job['deviceId']), 'taskId': job['validationTaskId'], 'image': image,
                      'modelHash': job['source']['sha256'], 'runtimeProfileDigest': job['recipe']['runtimeProfileDigest'],
                      'nonce': nonce}
            argv = [argument.format_map(values) for argument in local['containerProbeArgv']]
            # A fixed administrator-installed probe waits for the image, performs real inference and emits JSON.
            raw = commands.run(argv, timeout=local.get('validationTimeoutSeconds', 600))
            try:
                proof = json.loads(raw)
            except ValueError:
                raise DeliveryError('validation_probe_invalid', 'Container validation probe did not return JSON') from None
            validate_probe(proof, job, image, nonce)
            self.checkpoint(job, state, containerProof=proof)
        validate_probe(proof, job, image, nonce)
        recipe_digest = job.get('recipeDigest') or digest({k: v for k, v in job['recipe'].items() if k != 'digest'})
        report = {'id': 'report-' + job['id'], 'jobId': job['id'], 'recipeDigest': recipe_digest,
                  'releaseDigest': release_digest, 'image': image, 'modelHash': job['source']['sha256'],
                  'runtimeProfileDigest': job['recipe']['runtimeProfileDigest'], 'deviceId': job.get('validationDeviceId', job['deviceId']),
                  'taskId': job['validationTaskId'], 'status': 'passed', 'nativeVerified': True,
                  'containerVerified': True, 'simulated': False,
                  **{key: proof[key] for key in ('latencyMs', 'memoryBytes', 'errorRate', 'quality', 'observedAt')}}
        guard.check()
        result = self.complete_validation(path, identity, report, guard, deadline)
        authoritative = result.get('report') or {}
        if result.get('status') != 'passed' or not authoritative.get('digest'):
            raise DeliveryError('server_validation_unverified', 'Server did not independently verify the deployed model scope')
        require_sha(authoritative['digest'])
        for key, value in report.items():
            actual = authoritative.get(key)
            if key == 'observedAt':
                actual, value = timestamp(actual), timestamp(value)
            if actual != value:
                raise DeliveryError('server_validation_mismatch', 'Server validation changed the bound report identity')
        self.checkpoint(job, state, validationReport=authoritative)
        return authoritative

    def run_once(self):
        active = self.journal.get('__active_claim__')
        claim = active.get('claim')
        if claim:
            job = claim['job']
            identity = {key: claim[key] for key in ('claimToken', 'claimEpoch', 'controlEpoch')}
            identity['workerId'] = self.config['workerId']
            state = self.journal.bind(job)
            try:
                # A result already committed by Server remains idempotent after a lost acknowledgement.
                if state.get('result'):
                    self.server.request(INTERNAL + '/jobs/' + job['id'] + '/result',
                                        {**identity, **state['result']})
                    self.checkpoint(job, state, resultAcknowledged=True)
                    self.journal.save('__active_claim__', {})
                    return True
                renewed = self.server.request(INTERNAL + '/jobs/' + job['id'] + '/heartbeat', identity)
                claim['leaseExpiresAt'] = renewed['leaseExpiresAt']
            except ApiError as error:
                if error.status not in (403, 409):
                    raise
                # Epoch revocation cannot authorize more device work from this old claim.
                self.journal.save('__active_claim__', {})
                claim = None
        if claim is None:
            claim = self.server.request(INTERNAL + '/jobs/claim', {
                'workerId': self.config['workerId'], 'recipeIds': sorted(self.recipes)})
        job = claim.get('job')
        if not job:
            return False
        self.journal.save('__active_claim__', {'claim': claim})
        identity = {key: claim[key] for key in ('claimToken', 'claimEpoch', 'controlEpoch')}
        identity['workerId'] = self.config['workerId']
        guard = ClaimGuard(self.server, job['id'], identity, claim)
        try:
            guard.start()
            state = self.journal.bind(job)
            self.resume_attempt(job, state, claim['claimEpoch'])
            result = state.get('result')
            if result is None:
                try:
                    result = self.execute(job, state, identity, guard)
                except DeliveryError as error:
                    if error.code == 'claim_lost':
                        raise
                    result = {'status': 'failed', 'errorCode': error.code}
                self.checkpoint(job, state, result={**result, 'resultId': uuid.uuid4().hex})
                result = state['result']
            guard.check()
            while True:
                guard.check()
                try:
                    self.server.request(INTERNAL + '/jobs/' + job['id'] + '/result', {**identity, **result})
                    break
                except TransportError:
                    # Retry the identical resultId and proof, never the device or build operation.
                    time.sleep(3)
            self.checkpoint(job, state, resultAcknowledged=True)
            self.journal.save('__active_claim__', {})
            return True
        finally:
            guard.close()


def load_config(path):
    value = json.loads(Path(path).read_text())
    for key in ('workerId', 'server', 'agentdp', 'stateDir', 'workDir', 'bundleDir', 'recipes', 'devices',
                'registryRepository', 'buildkitAddress', 'signingKeyFile', 'trustedPublicKeyFile'):
        if not value.get(key):
            raise DeliveryError('configuration_incomplete', 'Missing worker configuration field ' + key)
    for recipe in value['recipes']:
        if recipe.get('enabled') is not True:
            continue
        for key in ('id', 'serverRecipeDigest', 'contextDir', 'contextDigest', 'runtimeBundleDigest', 'inputFormats',
                    'agentdpDeployment', 'nativeModelIdPath', 'nativeModelHashPath', 'containerProbeArgv'):
            if not recipe.get(key):
                raise DeliveryError('configuration_incomplete', 'Missing enabled recipe field ' + key)
        require_sha(recipe['serverRecipeDigest'])
        require_sha(recipe['contextDigest'])
        require_sha(recipe['runtimeBundleDigest'])
        if (recipe['inputFormats'] != ['rknn'] or not isinstance(recipe['containerProbeArgv'], list)
                or not all(isinstance(arg, str) and '\x00' not in arg for arg in recipe['containerProbeArgv'])):
            raise DeliveryError('configuration_invalid', 'Prepared recipes require RKNN inputs and a fixed probe argument array')
    return value


def main(argv=None):
    os.umask(0o077)
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--config', required=True, help='Administrator-owned JSON configuration')
    sub = parser.add_subparsers(dest='command', required=True)
    worker = sub.add_parser('run')
    worker.add_argument('--once', action='store_true')
    for name in ('verify-bundle', 'import-bundle'):
        command = sub.add_parser(name)
        command.add_argument('bundle')
    arguments = parser.parse_args(argv)
    config = load_config(arguments.config)
    if arguments.command != 'run':
        commands = Commands()
        if arguments.command == 'verify-bundle':
            result = verify_bundle(arguments.bundle, config['trustedPublicKeyFile'], commands,
                                   config.get('openssl', 'openssl'))
        else:
            result = import_bundle(arguments.bundle, config['trustedPublicKeyFile'], ImageBuilder(config, commands),
                                   commands, config.get('openssl', 'openssl'))
        print(json.dumps(result, ensure_ascii=False))
        return
    state_dir = Path(config['stateDir'])
    state_dir.mkdir(parents=True, exist_ok=True)
    with (state_dir / 'worker.lock').open('a') as lock:
        try:
            fcntl.flock(lock.fileno(), fcntl.LOCK_EX | fcntl.LOCK_NB)
        except BlockingIOError:
            raise DeliveryError('worker_busy', 'Another process owns this worker journal') from None
        executor = Worker(config)
        try:
            while True:
                try:
                    executor.run_once()
                except DeliveryError as error:
                    print(json.dumps({'errorCode': error.code}), file=sys.stderr, flush=True)
                    if arguments.once:
                        raise
                if arguments.once:
                    return
                time.sleep(config.get('pollSeconds', 5))
        finally:
            executor.journal.close()


if __name__ == '__main__':
    try:
        main()
    except (DeliveryError, OSError, ValueError, KeyError) as error:
        # Keep endpoints, source paths, tokens and arbitrary subprocess errors out of routine logs.
        print(json.dumps({'errorCode': getattr(error, 'code', 'configuration_or_io_error')}), file=sys.stderr)
        sys.exit(1)
