import { createPayload, editable, validateEntity } from './EntityPages';
import { errorMessage } from './api';
import { statusLabel } from './components';
import { modelPayload, validateModel } from './ModelsPage';
import { Task } from './types';

const task: Task = {
  id: 'example-task',
  name: '示例任务',
  metadataVersion: 7,
  deviceId: 'example-device',
  kind: 'voice-test',
  state: 'registered',
  revision: 4,
  target: { endpointId: 3, stackId: 8, serviceId: 'voice' },
  models: [
    {
      slot: 'asr-encoder',
      packageId: 'example-model',
      state: 'loaded',
      observedAt: '2026-11-15T01:00:00Z',
    },
  ],
  dataLocations: [],
  capabilities: ['voice-test'],
  modelEnvKeys: ['MODEL_WINDOW'],
  userIds: [2],
  teamIds: [1],
  testTask: true,
  runtimeBackend: 'rknn',
  templateId: 'voice-v1',
};

it('keeps deployment identity and model receipts outside metadata PATCH', () => {
  const patch = editable('tasks', { ...task, name: '新名称' }, task);
  expect(patch).toMatchObject({
    metadataVersion: 7,
    name: '新名称',
    userIds: [2],
  });
  for (const key of [
    'target',
    'models',
    'revision',
    'deviceId',
    'capabilities',
    'modelEnvKeys',
    'testTask',
    'runtimeBackend',
    'templateId',
  ])
    expect(patch).not.toHaveProperty(key);
  expect(task.models[0].observedAt).toBe('2026-11-15T01:00:00Z');
});

it('only sends configuration fields that actually changed', () => {
  const patch = editable(
    'tasks',
    { ...task, modelEnvKeys: ['MODEL_WINDOW', 'MODEL_BACKEND'] },
    task
  );
  expect(patch).toHaveProperty('modelEnvKeys', [
    'MODEL_WINDOW',
    'MODEL_BACKEND',
  ]);
  expect(patch).not.toHaveProperty('capabilities');
});

it('treats legacy null and an empty capability list as the same metadata-only edit', () => {
  const initial = {
    ...task,
    capabilities: null as unknown as string[],
    modelEnvKeys: undefined,
  };
  const patch = editable(
    'tasks',
    { ...initial, name: '更新名称', capabilities: [], modelEnvKeys: [] },
    initial
  );
  expect(patch).not.toHaveProperty('capabilities');
  expect(patch).not.toHaveProperty('modelEnvKeys');
});

it('never submits derived mount or runtime evidence as editable location metadata', () => {
  const value = {
    ...task,
    dataLocations: [
      {
        name: '业务数据',
        kind: 'business',
        containerPath: '/app/data',
        verification: 'verified',
        hostSource: '/private/runtime-source',
        observedAt: '2026-11-15T01:00:00Z',
      },
    ],
  };
  const patch = editable('tasks', value, task);
  expect(patch).toHaveProperty('dataLocations', [
    { name: '业务数据', kind: 'business', containerPath: '/app/data' },
  ]);
});

it('does not expose source binding or observed status as editable fields', () => {
  const source = {
    id: 'source',
    name: '语音来源',
    metadataVersion: 2,
    deviceId: 'device',
    taskId: 'task',
    type: 'voice',
    enabled: false,
    status: 'healthy',
    lastSeenAt: '2026-11-15T01:00:00Z',
  };
  expect(editable('sources', source)).toEqual({
    metadataVersion: 2,
    name: '语音来源',
    description: '',
    archived: false,
    enabled: false,
  });
});

it('serializes numeric native target IDs from HTML selectors', () => {
  const result = createPayload({
    ...task,
    target: { ...task.target, stackId: '8' as unknown as number },
  }) as Task;
  expect(result.target.stackId).toBe(8);
  expect(result.target.endpointId).toBe(3);
});

it('requires registered identities and names before mutation', () => {
  expect(validateEntity({ ...task, id: '../unsafe', name: ' ' })).toMatchObject(
    { id: expect.any(String), name: expect.any(String) }
  );
});

it('requires immutable model and weight digests', () => {
  const values = {
    id: 'new-package',
    name: '新模型',
    version: '1',
    image: 'repo/model:latest',
    modelHash: 'label',
    modelId: 'weights',
  } as Parameters<typeof validateModel>[0];
  expect(validateModel(values)).toMatchObject({
    image: expect.any(String),
    modelHash: expect.any(String),
  });
});

it('sends only immutable package fields, with plural compatible template IDs', () => {
  const values = {
    id: 'new-package',
    name: '模型',
    version: '1',
    kind: 'voice',
    slot: 'llm',
    metadataVersion: 0,
    description: '仅管理资料',
    image: 'example.invalid/model@sha256:' + 'a'.repeat(64),
    modelHash: 'sha256:' + 'b'.repeat(64),
    modelId: 'example-weights',
    taskKinds: ['voice'],
    architectures: ['arm64'],
    templateIds: ['voice-v1'],
    environmentPairs: [{ key: 'MODEL_WINDOW', value: '4' }],
  } as Parameters<typeof modelPayload>[0];
  const payload = modelPayload(values);
  expect(payload).toHaveProperty('templateIds', ['voice-v1']);
  expect(payload).toHaveProperty('environment', { MODEL_WINDOW: '4' });
  expect(payload).not.toHaveProperty('metadataVersion');
  expect(payload).not.toHaveProperty('description');
  expect(payload).not.toHaveProperty('templateId');
});

it('shows a reload action message for CAS conflict without reflecting response content', () => {
  expect(
    errorMessage({
      response: {
        status: 409,
        data: { errorCode: 'metadata_version_conflict' },
      },
    })
  ).toContain('刷新');
  expect(
    errorMessage({
      response: {
        status: 502,
        data: { message: 'provider response contains candidate-secret' },
      },
    })
  ).not.toContain('candidate-secret');
});

it('explains retained cloud settings and pending trusted runtime evidence in Chinese', () => {
  expect(
    errorMessage({ response: { data: { errorCode: 'provider_rate_limited' } } })
  ).toContain('未保存配置');
  expect(
    errorMessage({
      response: { data: { errorCode: 'model_runtime_unverified' } },
    })
  ).toContain('固定镜像');
  expect(statusLabel('passed')).toBe('已通过可信验证');
});

it('does not equate registration or unknown runtime with healthy business service', () => {
  expect(statusLabel('registered')).toBe('已登记');
  expect(statusLabel('running')).toBe('任务实例运行中');
  expect(statusLabel('unrecognized')).toBe('待确认');
});
