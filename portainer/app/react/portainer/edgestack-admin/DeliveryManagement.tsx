import { useEffect, useRef, useState } from 'react';

import axios from '@/portainer/services/axios/axios';

import { Widget } from '@@/Widget';
import { Button } from '@@/buttons';
import { Input } from '@@/form-components/Input';
import { Select } from '@@/form-components/Input/Select';

type Row = Record<string, unknown> & { id: string; metadataVersion?: number };
type Tab = 'requests' | 'jobs' | 'recipes' | 'policies' | 'execution';
const titles: Record<Tab, string> = {
  requests: '模型申请',
  jobs: '镜像构建',
  recipes: '运行配方',
  policies: '自动发布策略',
  execution: '设备租约',
};
const statusLabels: Record<string, string> = {
  resident: '正在占用设备',
  released: '租约已释放',
  waiting_for_device: '等待设备空闲',
  submitted: '等待管理员适配',
  queued: '等待执行',
  claimed: '正在执行',
  native_validation: '正在核验模型',
  building: '正在构建镜像',
  artifact_ready: '镜像已保存',
  image_validation: '正在验证镜像',
  published: '已发布可用',
  failed: '未完成',
  needs_review: '需要核对',
};
const initialRecipe = {
  id: '',
  metadataVersion: 0,
  version: '1',
  enabled: false,
  engine: 'rknn',
  taskKind: 'camera',
  architecture: 'arm64',
  runtimeBackend: 'rknn',
  templateId: '',
  slot: 'vision',
  baseImage: '',
  runtimeProfileDigest: '',
  modelEnvironment: {} as Record<string, string>,
  sourcePrefixes: [] as string[],
  validationTaskId: '',
  validationSourceId: '',
  validation: {
    maxLatencyMs: 0,
    maxMemoryBytes: 0,
    maxErrorRate: 0,
    minQuality: 0,
  },
};
const initialPolicy = {
  id: '',
  metadataVersion: 0,
  enabled: false,
  autoPublish: true,
  allowStopValidationTask: false,
  recipeIds: [] as string[],
  deviceIds: [] as string[],
  userIds: [] as number[],
  teamIds: [] as number[],
};
function split(value: string) {
  return value
    .split(',')
    .map((x) => x.trim())
    .filter(Boolean);
}
async function send<T>(method: string, path: string, data?: unknown) {
  return (await axios.request<T>({ method, url: `/edgestack/${path}`, data }))
    .data;
}
function errorText(error: unknown) {
  const value = error as {
    response?: { data?: { errorCode?: string; message?: string } };
    message?: string;
  };
  const code = value.response?.data?.errorCode;
  const messages: Record<string, string> = {
    metadata_version_conflict: '配置已被更新，请刷新后重新编辑。',
    invalid_delivery_policy: '请填写发布范围、配方和有效验证门限。',
    invalid_delivery_recipe: '请检查固定镜像摘要、运行画像和验证任务。',
    delivery_worker_not_configured:
      'Server 尚未配置构建服务凭据，请先完成服务接入。',
  };
  return (
    (code && messages[code]) ||
    value.response?.data?.message ||
    code ||
    value.message ||
    '暂时无法完成操作。'
  );
}
export function DeliveryManagement() {
  const [tab, setTab] = useState<Tab>('requests');
  const [rows, setRows] = useState<Row[]>([]);
  const [recipes, setRecipes] = useState<Row[]>([]);
  const [policies, setPolicies] = useState<Row[]>([]);
  const [tasks, setTasks] = useState<Row[]>([]);
  const [sources, setSources] = useState<Row[]>([]);
  const [selected, setSelected] = useState('');
  const [resourceGroup, setResourceGroup] = useState('');
  const [stopPlan, setStopPlan] = useState<{
    id: string;
    status: string;
    targets: Array<{ taskId: string }>;
  }>();
  const [reply, setReply] = useState('');
  const [source, setSource] = useState({
    uri: '',
    sha256: '',
    modelId: '',
    format: 'rknn',
  });
  const [recipeId, setRecipeId] = useState('');
  const [policyId, setPolicyId] = useState('');
  const [recipe, setRecipe] = useState(initialRecipe);
  const [environmentText, setEnvironmentText] = useState('');
  const [policy, setPolicy] = useState(initialPolicy);
  const [error, setError] = useState('');
  const [notice, setNotice] = useState('');
  const [settings, setSettings] = useState<Record<string, unknown>>({});
  const generation = useRef(0);
  const [busy, setBusy] = useState(false);
  const endpoints: Record<Tab, string> = {
    requests: 'admin/model-requests',
    jobs: 'admin/delivery/jobs',
    recipes: 'admin/delivery/recipes',
    policies: 'admin/delivery/policies',
    execution: 'admin/execution-leases',
  };
  async function load() {
    const activeGeneration = generation.current;
    const values = await Promise.all([
      send<{ items: Row[] }>('GET', endpoints[tab]),
      send<{ items: Row[] }>('GET', 'admin/delivery/recipes'),
      send<{ items: Row[] }>('GET', 'admin/delivery/policies'),
      send<{ items: Row[] }>('GET', 'admin/tasks?pageSize=100'),
      send<{ items: Row[] }>('GET', 'admin/sources?pageSize=100'),
      send<Record<string, unknown>>('GET', 'admin/delivery/settings'),
    ]);
    if (activeGeneration !== generation.current) return;
    setSettings(values[5]);
    setRows(
      (values[0].items || []).map((row) => ({
        ...row,
        id: row.id || String(row.resourceGroup),
      }))
    );
    setRecipes(values[1].items || []);
    setPolicies(values[2].items || []);
    setTasks(values[3].items || []);
    setSources(values[4].items || []);
  }
  useEffect(() => {
    let alive = true;
    async function refresh() {
      try {
        if (alive) await load();
      } catch (e) {
        if (alive) setError(errorText(e));
      }
    }
    refresh();
    const timer = setInterval(refresh, 5000);
    return () => {
      alive = false;
      generation.current += 1;
      clearInterval(timer);
    };
    // The selected tab owns its polling interval.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [tab]);
  async function mutate(action: () => Promise<unknown>) {
    if (busy) return;
    setBusy(true);
    setError('');
    setNotice('');
    try {
      await action();
      setNotice('操作已完成，结果已重新读取。');
      await load();
    } catch (e) {
      setError(errorText(e));
    } finally {
      setBusy(false);
    }
  }
  function text(
    label: string,
    value: string | number,
    change: (value: string) => void,
    type = 'text'
  ) {
    return (
      <label className="mb-3 block">
        {label}
        {label.startsWith('模型参数') ? (
          <textarea
            className="form-control"
            rows={4}
            value={value}
            onChange={(event) => change(event.target.value)}
          />
        ) : (
          <Input
            data-cy={`delivery-input-${label}`}
            type={type}
            value={value}
            onChange={(event) => change(event.target.value)}
          />
        )}
      </label>
    );
  }
  function choices(items: Row[]) {
    return [
      { value: '', label: '请选择' },
      ...items.map((row) => ({
        value: row.id,
        label: String(row.name || row.id),
      })),
    ];
  }
  return (
    <Widget>
      <Widget.Title title="模型交付与发布" />
      <Widget.Body>
        <p className="mb-4" role="status">
          服务状态：{settings.enabled ? '已启用' : '未启用'} · 构建凭据
          {settings.workerTokenConfigured ? '已配置' : '待配置'} · 新任务模板
          {settings.newTaskProviderConfigured ? '已配置' : '待配置'} · 健康观测
          {settings.healthObserverConfigured ? '已配置' : '待配置'}
        </p>
        <div
          className="mb-4 flex flex-wrap gap-2"
          role="group"
          aria-label="模型交付"
        >
          {(Object.keys(titles) as Tab[]).map((name) => (
            <Button
              data-cy="delivery-action"
              key={name}
              color={tab === name ? 'primary' : 'default'}
              onClick={() => {
                setTab(name);
                setSelected('');
                setError('');
              }}
              aria-pressed={tab === name}
            >
              {titles[name]}
            </Button>
          ))}
        </div>
        {error && (
          <p role="alert" className="text-error">
            {error}
          </p>
        )}
        {notice && <p role="status">{notice}</p>}
        <div className="mb-4 space-y-2">
          {rows.map((row) => (
            <div
              key={row.id}
              className="flex flex-wrap items-center gap-3 border-b py-2"
            >
              <Button
                data-cy="delivery-action"
                color="link"
                onClick={() => {
                  setSelected(row.id);
                  if (tab === 'recipes') {
                    setRecipe({
                      ...initialRecipe,
                      ...row,
                    } as typeof initialRecipe);
                    setEnvironmentText(
                      Object.entries(
                        (row.modelEnvironment || {}) as Record<string, string>
                      )
                        .map(([key, value]) => `${key}=${value}`)
                        .join('\n')
                    );
                  }
                  if (tab === 'execution')
                    setResourceGroup(String(row.resourceGroup));
                  if (tab === 'policies')
                    setPolicy({
                      ...initialPolicy,
                      ...row,
                    } as typeof initialPolicy);
                }}
              >
                {String(
                  row.modelName || row.name || row.resourceGroup || row.id
                )}
              </Button>
              <span>
                {statusLabels[
                  String(
                    row.status === 'claimed'
                      ? row.phase || row.status
                      : row.status
                  )
                ] || String(row.status || (row.enabled ? '已启用' : '未启用'))}
              </span>
              {row.errorCode ? <span>{String(row.errorCode)}</span> : null}
            </div>
          ))}
        </div>
        {!rows.length && <p>当前没有{titles[tab]}记录。</p>}
        {tab === 'requests' && selected && (
          <section>
            <h3>申请详情与执行来源</h3>
            <ol>
              {(
                (rows.find((row) => row.id === selected)?.messages ||
                  []) as Array<{ id: string; message: string }>
              ).map((message) => (
                <li key={message.id} className="mb-2 whitespace-pre-wrap">
                  {message.message}
                </li>
              ))}
            </ol>
            <label className="mb-3 block">
              回复申请
              <textarea
                className="form-control"
                rows={3}
                maxLength={4000}
                value={reply}
                onChange={(event) => setReply(event.target.value)}
              />
            </label>
            <Button
              data-cy="delivery-request-reply"
              disabled={busy || !reply.trim()}
              onClick={() =>
                mutate(async () => {
                  await send(
                    'POST',
                    `model-requests/${encodeURIComponent(selected)}/messages`,
                    { message: reply }
                  );
                  setReply('');
                })
              }
            >
              发送说明
            </Button>
            <p>
              固定来源和摘要后，构建服务按选定配方执行，最终镜像通过策略核验后发布。
            </p>
            <label className="mb-3 block" htmlFor="delivery-request-recipe">
              运行配方
              <Select
                id="delivery-request-recipe"
                data-cy="delivery-select"
                options={choices(recipes)}
                value={recipeId}
                onChange={(event) => setRecipeId(event.target.value)}
              />
            </label>
            <label className="mb-3 block" htmlFor="delivery-request-policy">
              发布策略
              <Select
                id="delivery-request-policy"
                data-cy="delivery-select"
                options={choices(policies)}
                value={policyId}
                onChange={(event) => setPolicyId(event.target.value)}
              />
            </label>
            <p>
              所选策略发布范围：
              {String(
                (
                  policies.find((item) => item.id === policyId)?.deviceIds || []
                ).toString() || '请选择策略后查看设备范围'
              )}
              。当前配方只有在最终镜像通过验证门限后才会发布。
            </p>
            {text('模型来源地址', source.uri, (value) =>
              setSource({ ...source, uri: value })
            )}
            {text('模型文件摘要（sha256:...）', source.sha256, (value) =>
              setSource({ ...source, sha256: value })
            )}
            {text('模型标识', source.modelId, (value) =>
              setSource({ ...source, modelId: value })
            )}
            {text('模型格式', source.format, (value) =>
              setSource({ ...source, format: value })
            )}
            <Button
              data-cy="delivery-action"
              disabled={busy}
              onClick={() =>
                mutate(() =>
                  send(
                    'POST',
                    `admin/model-requests/${encodeURIComponent(selected)}/resolve`,
                    { recipeId, policyId, source }
                  )
                )
              }
            >
              确认来源并创建交付任务
            </Button>
          </section>
        )}
        {tab === 'jobs' && selected && (
          <section>
            <p>
              重试复用已冻结的输入和已完成阶段，结果未知的设备任务需要先核对。
            </p>
            <Button
              data-cy="delivery-action"
              disabled={busy}
              onClick={() =>
                mutate(() =>
                  send(
                    'POST',
                    `admin/model-requests/${encodeURIComponent(String(rows.find((row) => row.id === selected)?.requestId || ''))}/retry`,
                    {}
                  )
                )
              }
            >
              重试失败阶段
            </Button>
          </section>
        )}
        {tab === 'recipes' && (
          <section>
            <h3>{selected ? '编辑运行配方' : '登记运行配方'}</h3>
            {selected && (
              <Button
                data-cy="delivery-new-recipe"
                color="default"
                onClick={() => {
                  setSelected('');
                  setRecipe(initialRecipe);
                  setEnvironmentText('');
                }}
              >
                登记新配方
              </Button>
            )}
            {text('配方 ID', recipe.id, (id) => setRecipe({ ...recipe, id }))}
            {text('配方版本', recipe.version, (version) =>
              setRecipe({ ...recipe, version })
            )}
            <label className="mb-3 block" htmlFor="delivery-recipe-family">
              模型家族
              <Select
                id="delivery-recipe-family"
                data-cy="delivery-select"
                value={recipe.taskKind}
                options={[
                  { value: 'camera', label: 'YOLOv8 RKNN 视觉' },
                  { value: 'voice-test', label: 'Qwen3 ASR 语音' },
                ]}
                onChange={(event) =>
                  setRecipe({
                    ...recipe,
                    taskKind: event.target.value,
                    slot:
                      event.target.value === 'camera'
                        ? 'vision'
                        : 'asr-encoder',
                  })
                }
              />
            </label>
            {text('运行后端', recipe.runtimeBackend, (runtimeBackend) =>
              setRecipe({ ...recipe, runtimeBackend })
            )}
            {text('运行模板 ID', recipe.templateId, (templateId) =>
              setRecipe({ ...recipe, templateId })
            )}
            {text('基础镜像完整 digest 引用', recipe.baseImage, (baseImage) =>
              setRecipe({ ...recipe, baseImage })
            )}
            {text(
              '硬件与运行库画像摘要',
              recipe.runtimeProfileDigest,
              (runtimeProfileDigest) =>
                setRecipe({ ...recipe, runtimeProfileDigest })
            )}
            {text(
              '模型参数，逐行填写 KEY=VALUE',
              environmentText,
              setEnvironmentText
            )}
            {text(
              '允许的模型来源前缀，逗号分隔',
              recipe.sourcePrefixes.join(','),
              (value) => setRecipe({ ...recipe, sourcePrefixes: split(value) })
            )}
            <label
              className="mb-3 block"
              htmlFor="delivery-recipe-validation-task"
            >
              最终镜像验证任务
              <Select
                id="delivery-recipe-validation-task"
                data-cy="delivery-select"
                value={recipe.validationTaskId}
                options={choices(tasks.filter((task) => task.testTask))}
                onChange={(event) =>
                  setRecipe({ ...recipe, validationTaskId: event.target.value })
                }
              />
            </label>
            <label
              className="mb-3 block"
              htmlFor="delivery-recipe-validation-source"
            >
              可信验证来源
              <Select
                id="delivery-recipe-validation-source"
                data-cy="delivery-select"
                value={recipe.validationSourceId}
                options={choices(sources)}
                onChange={(event) =>
                  setRecipe({
                    ...recipe,
                    validationSourceId: event.target.value,
                  })
                }
              />
            </label>
            {(
              [
                'maxLatencyMs',
                'maxMemoryBytes',
                'maxErrorRate',
                'minQuality',
              ] as const
            ).map((key, index) => (
              <div key={key}>
                {text(
                  [
                    '时延上限，毫秒',
                    '内存上限，字节',
                    '错误率上限，0 到 1',
                    '质量下限，0 到 1',
                  ][index],
                  recipe.validation[key],
                  (value) =>
                    setRecipe({
                      ...recipe,
                      validation: {
                        ...recipe.validation,
                        [key]: Number(value),
                      },
                    }),
                  'number'
                )}
              </div>
            ))}
            <label className="mb-3 block">
              <input
                type="checkbox"
                checked={recipe.enabled}
                onChange={(event) =>
                  setRecipe({ ...recipe, enabled: event.target.checked })
                }
              />{' '}
              启用该运行配方
            </label>
            <Button
              data-cy="delivery-action"
              disabled={busy}
              onClick={() =>
                mutate(() =>
                  send(
                    'PUT',
                    `admin/delivery/recipes/${encodeURIComponent(recipe.id)}`,
                    {
                      ...recipe,
                      modelEnvironment: Object.fromEntries(
                        environmentText
                          .split('\n')
                          .filter((line) => line.trim())
                          .map((line) => {
                            const separator = line.indexOf('=');
                            if (separator <= 0)
                              throw Error('模型参数请逐行填写 KEY=VALUE。');
                            return [
                              line.slice(0, separator).trim(),
                              line.slice(separator + 1),
                            ];
                          })
                      ),
                    }
                  )
                )
              }
            >
              保存配方
            </Button>
          </section>
        )}
        {tab === 'policies' && (
          <section>
            <h3>{selected ? '编辑自动发布策略' : '登记自动发布策略'}</h3>
            {selected && (
              <Button
                data-cy="delivery-new-policy"
                color="default"
                onClick={() => {
                  setSelected('');
                  setPolicy(initialPolicy);
                }}
              >
                登记新策略
              </Button>
            )}
            {text('策略 ID', policy.id, (id) => setPolicy({ ...policy, id }))}
            {text(
              '允许的配方 ID，逗号分隔',
              policy.recipeIds.join(','),
              (value) => setPolicy({ ...policy, recipeIds: split(value) })
            )}
            {text(
              '允许的设备 ID，逗号分隔',
              policy.deviceIds.join(','),
              (value) => setPolicy({ ...policy, deviceIds: split(value) })
            )}
            {text('可见用户 ID，逗号分隔', policy.userIds.join(','), (value) =>
              setPolicy({ ...policy, userIds: split(value).map(Number) })
            )}
            {text('可见团队 ID，逗号分隔', policy.teamIds.join(','), (value) =>
              setPolicy({ ...policy, teamIds: split(value).map(Number) })
            )}
            <label className="mb-3 block">
              <input
                type="checkbox"
                checked={policy.allowStopValidationTask}
                onChange={(event) =>
                  setPolicy({
                    ...policy,
                    allowStopValidationTask: event.target.checked,
                  })
                }
              />{' '}
              允许构建前停止已登记的独立验证任务以交接设备租约
            </label>
            <label className="mb-3 block">
              <input
                type="checkbox"
                checked={policy.enabled}
                onChange={(event) =>
                  setPolicy({ ...policy, enabled: event.target.checked })
                }
              />{' '}
              启用策略
            </label>
            <label className="mb-3 block">
              <input
                type="checkbox"
                checked={policy.autoPublish}
                onChange={(event) =>
                  setPolicy({ ...policy, autoPublish: event.target.checked })
                }
              />{' '}
              最终验证和门限通过后自动发布
            </label>
            <Button
              data-cy="delivery-action"
              disabled={busy}
              onClick={() =>
                mutate(() =>
                  send(
                    'PUT',
                    `admin/delivery/policies/${encodeURIComponent(policy.id)}`,
                    policy
                  )
                )
              }
            >
              保存策略
            </Button>
          </section>
        )}
        {tab === 'execution' && (
          <section>
            <h3>释放隔离验证设备</h3>
            <p>
              预览会列出需要停止的独立验证任务。设备包含业务任务或执行结果未知时，系统拒绝释放租约。
            </p>
            {text('设备资源组', resourceGroup, (value) => {
              setResourceGroup(value);
              setStopPlan(undefined);
            })}
            <Button
              data-cy="delivery-stop-preview"
              disabled={busy || !resourceGroup}
              onClick={() =>
                mutate(async () => {
                  setStopPlan(
                    await send('POST', 'admin/execution-leases/stop-preview', {
                      resourceGroup,
                    })
                  );
                })
              }
            >
              预览停止影响
            </Button>
            {stopPlan && (
              <div className="mt-4">
                <h4>即将停止的验证任务</h4>
                <ul>
                  {stopPlan.targets.map((target) => (
                    <li key={target.taskId}>
                      {String(
                        tasks.find((task) => task.id === target.taskId)?.name ||
                          target.taskId
                      )}
                    </li>
                  ))}
                </ul>
                <p>
                  当前状态：
                  {stopPlan.status === 'released'
                    ? '租约已释放'
                    : '等待停止确认'}
                </p>
                <Button
                  data-cy="delivery-stop-confirm"
                  disabled={busy || stopPlan.status === 'released'}
                  onClick={() =>
                    mutate(async () => {
                      setStopPlan(
                        await send(
                          'POST',
                          'admin/execution-leases/stop-confirm',
                          { previewId: stopPlan.id, confirm: true }
                        )
                      );
                    })
                  }
                >
                  确认停止并释放租约
                </Button>
                <Button
                  data-cy="delivery-stop-reconcile"
                  color="default"
                  disabled={busy || stopPlan.status === 'released'}
                  onClick={() =>
                    mutate(async () => {
                      setStopPlan(
                        await send(
                          'POST',
                          'admin/execution-leases/reconcile-stop',
                          { previewId: stopPlan.id, confirm: true }
                        )
                      );
                    })
                  }
                >
                  核对上次停止结果
                </Button>
              </div>
            )}
          </section>
        )}
      </Widget.Body>
    </Widget>
  );
}
