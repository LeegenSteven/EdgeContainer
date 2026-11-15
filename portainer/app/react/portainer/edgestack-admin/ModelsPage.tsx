import { useState } from 'react';
import { useRouter } from '@uirouter/react';
import { useQuery } from '@tanstack/react-query';
import { ColumnDef } from '@tanstack/react-table';
import { FieldArray, useFormikContext } from 'formik';

import axios from '@/portainer/services/axios/axios';

import { Widget } from '@@/Widget';
import { Button } from '@@/buttons';
import { Select } from '@@/form-components/Input/Select';
import { FormControl } from '@@/form-components/FormControl';

import { ModelPackage, Task, Preview, Operation, ListResponse } from './types';
import {
  useAdminList,
  useAdminChoices,
  useAdminItem,
  useAdminMutation,
  errorMessage,
  send,
  get,
  adminKey,
} from './api';
import {
  Editor,
  TextField,
  ChoiceField,
  CheckField,
  ErrorNotice,
  Notice,
  Records,
  Raw,
  Confirm,
  modelSlots,
  slotLabel,
  statusLabel,
  timestamp,
} from './components';
import { StringArrayField } from './EntityPages';

interface ModelInput extends ModelPackage {
  environmentPairs: { key: string; value: string }[];
}
interface Validation {
  id: string;
  taskId: string;
  status: string;
  observedAt?: string;
  modelId?: string;
  modelHash?: string;
  errorCode?: string;
}
const validationLabels: Record<string, string> = {
  draft: '待验证草稿',
  validating: '受控验证进行中',
  passed: '已取得可信验证',
  failed: '验证失败',
  legacy_scoped: '保留历史任务范围',
};

export function ModelsPage({ id, isNew }: { id?: string; isNew: boolean }) {
  const router = useRouter();
  const query = useAdminList<ModelPackage>('models');
  const item = useAdminItem<ModelPackage>('models', id);
  const tasks = useAdminChoices<Task>('tasks');
  const [taskId, setTaskId] = useState('');
  const [preview, setPreview] = useState<Preview>();
  const [publication, setPublication] = useState<boolean>();
  const [operation, setOperation] = useState<Operation>();
  const validations = useQuery({
    queryKey: [...adminKey, 'model-validations', id],
    queryFn: () =>
      get<ListResponse<Validation>>(
        `models/${encodeURIComponent(id || '')}/validations`
      ),
    enabled: !!id,
    refetchInterval: 5000,
  });
  const create = useAdminMutation((v: ModelInput) =>
    send('POST', 'models', modelPayload(v))
  );
  const validate = useAdminMutation(async () => {
    const task = tasks.items.find((t) => t.id === taskId);
    if (!task || !item.data) throw new Error('task_required');
    const p = await send<Preview>(
      'POST',
      `models/${encodeURIComponent(item.data.id)}/validation-preview`,
      { taskId, slot: item.data.slot, revision: task.revision }
    );
    setPreview(p);
  });
  const confirm = useAdminMutation(async () => {
    if (!item.data || !preview) return;
    const op = await send<Operation>(
      'POST',
      `models/${encodeURIComponent(item.data.id)}/validations`,
      { previewId: preview.id, confirm: true }
    );
    setOperation(op);
    setPreview(undefined);
  });
  const publish = useAdminMutation(async () => {
    if (!item.data || publication === undefined) return;
    await send(
      'POST',
      `models/${encodeURIComponent(item.data.id)}/publication`,
      { published: publication, metadataVersion: item.data.metadataVersion }
    );
    setPublication(undefined);
  });
  const active = useQuery({
    queryKey: [...adminKey, 'operation', operation?.id],
    queryFn: () => getOperation(operation?.id || ''),
    enabled: !!operation?.id,
    refetchInterval: (data) =>
      data && ['succeeded', 'failed'].includes(data.status) ? false : 3000,
  });
  const columns: ColumnDef<ModelPackage>[] = [
    {
      accessorKey: 'name',
      header: '名称',
      cell: ({ row }) => (
        <Button
          color="link"
          onClick={() =>
            router.stateService.go('portainer.business.models.item', {
              id: row.original.id,
            })
          }
          data-cy="model-details"
        >
          <Raw>{row.original.name}</Raw>
        </Button>
      ),
    },
    { accessorKey: 'version', header: '版本' },
    { id: 'slot', header: '模型用途', accessorFn: (m) => slotLabel(m.slot) },
    {
      id: 'publication',
      header: '发布',
      accessorFn: (m) => (m.published ? '已发布' : '未发布'),
    },
    {
      id: 'validation',
      header: '验证',
      accessorFn: (m) =>
        validationLabels[m.validationStatus || 'draft'] || '待确认',
    },
  ];
  return (
    <>
      <ErrorNotice error={query.error || item.error || tasks.error} />
      <Records
        name="models"
        title="不可变模型目录"
        items={query.items}
        columns={columns}
        loading={query.isLoading}
        actions={
          <Button
            onClick={() =>
              router.stateService.go('portainer.business.models.new')
            }
            data-cy="model-create"
          >
            登记新草稿包
          </Button>
        }
      />
      {query.hasNextPage && (
        <Button
          color="default"
          onClick={() => query.fetchNextPage()}
          data-cy="models-more"
        >
          继续载入
        </Button>
      )}
      {isNew && (
        <Editor
          title="登记不可变模型草稿"
          initialValues={emptyModel()}
          validate={validateModel}
          onSubmit={(v) => create.mutateAsync(v)}
          onCancel={() => router.stateService.go('portainer.business.models')}
        >
          <TextField name="id" label="新包标识" required />
          <TextField name="name" label="用户显示名称" required />
          <TextField name="version" label="模型版本" required />
          <TextField
            name="kind"
            label="模型类别"
            hint="与任务兼容类别一致，例如 voice 或 camera。"
          />
          <ChoiceField name="slot" label="模型用途" options={modelSlots} />
          <StringArrayField name="taskKinds" label="允许的任务用途标识" />
          <StringArrayField name="architectures" label="架构" />
          <TextField name="runtimeBackend" label="运行后端标识" required />
          <StringArrayField name="templateIds" label="兼容模板标识" />
          <TextField
            name="image"
            label="固定容器镜像"
            required
            hint="完整 image@sha256:…，不接受可变 tag 单独作为身份。"
          />
          <TextField name="modelId" label="权重身份" required />
          <TextField
            name="modelHash"
            label="权重 SHA-256"
            required
            hint="sha256: 后跟 64 位十六进制。"
          />
          <CheckField name="requiresRestart" label="此包切换需要重启任务" />
          <EnvironmentFields />
          <Notice>
            包内容登记后不可修改；先选择独立测试任务取得可信加载回执，再发布给通过兼容检查的任务。
          </Notice>
        </Editor>
      )}
      {item.data && (
        <Widget data-cy="model-detail">
          <Widget.Title
            title={
              <Raw>
                {item.data.name} · {item.data.version}
              </Raw>
            }
          />
          <Widget.Body>
            <p>
              用途：{slotLabel(item.data.slot)}；发布状态：
              {item.data.published ? '已发布' : '未发布'}；验证：
              {validationLabels[item.data.validationStatus || 'draft']}
            </p>
            <p>
              运行后端：<Raw>{item.data.runtimeBackend || '未登记'}</Raw>
              ；兼容模板：
              <Raw>{item.data.templateIds?.join('、') || '未登记'}</Raw>
            </p>
            <dl>
              <dt>不可变镜像</dt>
              <dd data-edgestack-raw className="break-all">
                {item.data.image}
              </dd>
              <dt>模型身份</dt>
              <dd data-edgestack-raw>{item.data.modelId}</dd>
              <dt>权重校验</dt>
              <dd data-edgestack-raw className="break-all">
                {item.data.modelHash}
              </dd>
            </dl>
            {item.data.legacyScoped && (
              <Notice>
                此为历史发布包，只保留原任务范围，不补造验证记录，不自动扩大范围。已保留任务：
                <Raw>{item.data.allowedTaskIds?.join('、') || '暂无'}</Raw>
              </Notice>
            )}
            <FormControl label="独立测试任务" inputId="model-test-task">
              <Select
                data-edgestack-raw
                id="model-test-task"
                value={taskId}
                onChange={(e) => setTaskId(e.target.value)}
                options={[
                  { value: '', label: '选择明确登记的测试任务' },
                  ...tasks.items
                    .filter((t) => t.testTask && !t.archived)
                    .map((t) => ({ value: t.id, label: t.name })),
                ]}
                data-cy="model-test-task"
              />
            </FormControl>
            <ErrorNotice error={validate.error} />
            <div className="flex gap-2">
              <Button
                disabled={!taskId || validate.isLoading}
                onClick={() => validate.mutate()}
                data-cy="model-validation-preview"
              >
                预览受控验证
              </Button>
              <Button
                color="default"
                disabled={
                  !item.data.published &&
                  item.data.validationStatus !== 'passed' &&
                  !item.data.legacyScoped
                }
                onClick={() => setPublication(!item.data?.published)}
                data-cy="model-publication"
              >
                {item.data.published ? '撤回用户候选' : '发布用户候选'}
              </Button>
              <Button
                color="default"
                onClick={() => {
                  item.refetch();
                  validations.refetch();
                }}
                data-cy="model-refresh"
              >
                刷新验证状态
              </Button>
            </div>
            <Notice>
              受控验证可能重启所选测试任务，保留卷、端口及网络。发布不会自动部署；撤回不卸载已经运行的模型。
            </Notice>
            {(operation || active.data) && (
              <p role="status">
                最近验证操作：
                {statusLabel(active.data?.status || operation?.status)}
                {active.data?.errorCode
                  ? `（${errorMessage({ response: { data: { errorCode: active.data.errorCode } } })}）`
                  : ''}
              </p>
            )}
            <ErrorNotice error={active.error || validations.error} />
            <h4>可信验证记录</h4>
            {validations.data?.items.length ? (
              validations.data.items.map((v) => (
                <p key={v.id} data-edgestack-raw>
                  {v.taskId} · {statusLabel(v.status)} · 最近确认{' '}
                  {timestamp(v.observedAt)}
                  {v.modelId ? ` · ${v.modelId}` : ''}
                </p>
              ))
            ) : (
              <Notice>尚未取得受控验证记录。</Notice>
            )}
          </Widget.Body>
        </Widget>
      )}
      {preview && (
        <Confirm
          title="确认受控模型验证"
          onClose={() => setPreview(undefined)}
          onConfirm={() => confirm.mutate()}
          busy={confirm.isLoading}
          error={confirm.error}
        >
          <PreviewDetails preview={preview} />
        </Confirm>
      )}
      {publication !== undefined && item.data && (
        <Confirm
          title={publication ? '发布模型包' : '撤回模型包'}
          onClose={() => setPublication(undefined)}
          onConfirm={() => publish.mutate()}
          busy={publish.isLoading}
          error={publish.error}
        >
          <p>
            <Raw>
              {item.data.name} · {item.data.version}
            </Raw>
          </p>
          <Notice>
            {publication
              ? '仅对可信验证覆盖的兼容任务或历史保留范围开放。'
              : '将从用户可选候选中撤回，已运行实例不会自动切换。'}
          </Notice>
        </Confirm>
      )}
    </>
  );
}

async function getOperation(id: string) {
  return (
    await axios.get<Operation>(
      `/edgestack/operations/${encodeURIComponent(id)}`
    )
  ).data;
}

export function PreviewDetails({ preview }: { preview: Preview }) {
  return (
    <>
      <p>
        任务：<Raw>{preview.taskId}</Raw>；部署版本：{preview.revision}
      </p>
      <p>
        原模型：<Raw>{preview.previousPackageId || '未登记'}</Raw> →{' '}
        <Raw>{preview.packageId}</Raw>
      </p>
      <p>
        目标：Stack {preview.target.stackId} /{' '}
        <Raw>{preview.target.serviceId}</Raw>
      </p>
      <ul>
        {preview.effects.map((effect, i) => (
          <li key={i}>{effectLabel(effect)}</li>
        ))}
      </ul>
      <Notice>
        预览有效至 {timestamp(preview.expiresAt)}，确认前 Server
        会重新检查权限、兼容性与目标版本。
      </Notice>
    </>
  );
}

function effectLabel(effect: string) {
  const mapping: Record<string, string> = {
    'Update only the registered service image and approved model environment values':
      '仅更新已登记服务的镜像与获准模型环境变量',
    'Restart the task; retain all volumes, ports, networks, and unrelated environment values':
      '重启此任务，保留所有数据卷、端口、网络及无关环境变量',
    'Wait for a trusted runtime receipt before marking the model loaded':
      '收到可信运行时回执后，才标记模型已加载',
  };
  return mapping[effect] || effect;
}

function emptyModel(): ModelInput {
  return {
    id: '',
    name: '',
    description: '',
    metadataVersion: 0,
    version: '',
    kind: 'voice',
    slot: 'asr-encoder',
    taskKinds: ['voice-test'],
    architectures: ['arm64'],
    published: false,
    immutable: true,
    image: '',
    modelId: '',
    modelHash: '',
    environment: {},
    environmentPairs: [],
    requiresRestart: true,
    runtimeBackend: '',
    templateIds: [],
  };
}
export function validateModel(value: ModelInput) {
  const errors: Record<string, string> = {};
  if (!/^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,127}$/.test(value.id))
    errors.id = '请输入有效的新包标识。';
  if (!value.name.trim()) errors.name = '请输入名称。';
  if (!value.version.trim()) errors.version = '请输入版本。';
  if (!/@sha256:[a-fA-F0-9]{64}$/.test(value.image))
    errors.image = '请输入固定镜像 digest。';
  if (!/^sha256:[a-fA-F0-9]{64}$/.test(value.modelHash))
    errors.modelHash = '请输入有效权重 SHA-256。';
  if (!value.modelId.trim()) errors.modelId = '请输入模型身份。';
  if (!value.runtimeBackend?.trim())
    errors.runtimeBackend = '请输入已登记的运行后端。';
  if (!value.templateIds?.length) errors.templateIds = '至少登记一个兼容模板。';
  if (!value.taskKinds?.length) errors.taskKinds = '至少登记一种任务用途。';
  if (!value.architectures?.length)
    errors.architectures = '至少登记一种设备架构。';
  return errors;
}
export function modelPayload(value: ModelInput) {
  const { environmentPairs } = value;
  return {
    id: value.id,
    name: value.name,
    version: value.version,
    kind: value.kind,
    slot: value.slot,
    taskKinds: value.taskKinds,
    architectures: value.architectures,
    runtimeBackend: value.runtimeBackend,
    templateIds: value.templateIds,
    image: value.image,
    modelId: value.modelId,
    modelHash: value.modelHash,
    requiresRestart: value.requiresRestart,
    published: false,
    immutable: true,
    environment: Object.fromEntries(
      environmentPairs
        .filter((p) => p.key.trim())
        .map((p) => [p.key.trim(), p.value])
    ),
  };
}
function EnvironmentFields() {
  const { values } = useFormikContext<ModelInput>();
  return (
    <FieldArray name="environmentPairs">
      {({ push, remove }) => (
        <>
          <h4>已批准的模型环境变量</h4>
          {values.environmentPairs.map((_, i) => (
            <div key={i} className="mb-2 rounded border p-3">
              <TextField name={`environmentPairs.${i}.key`} label="变量名" />
              <TextField name={`environmentPairs.${i}.value`} label="固定值" />
              <Button
                color="default"
                onClick={() => remove(i)}
                data-cy={`model-env-remove-${i}`}
              >
                移除
              </Button>
            </div>
          ))}
          <Button
            color="default"
            onClick={() => push({ key: '', value: '' })}
            data-cy="model-env-add"
          >
            添加变量
          </Button>
          <Notice>
            仅填写模型启动配置，不填写密钥。任务允许的环境变量名仍由独立授权限制。
          </Notice>
        </>
      )}
    </FieldArray>
  );
}
