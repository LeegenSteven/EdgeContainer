import { useState } from 'react';
import { useRouter } from '@uirouter/react';
import { FieldArray, useFormikContext, useField } from 'formik';
import { ColumnDef } from '@tanstack/react-table';
import { useQuery } from '@tanstack/react-query';

import axios from '@/portainer/services/axios/axios';

import { Widget } from '@@/Widget';
import { Button } from '@@/buttons';
import { Input } from '@@/form-components/Input';
import { Select } from '@@/form-components/Input/Select';
import { FormControl } from '@@/form-components/FormControl';
import { UsersSelector } from '@@/UsersSelector';
import { TeamsSelector } from '@@/TeamsSelector';
import { Link } from '@@/Link';

import {
  useAdminChoices,
  useAdminList,
  useAdminItem,
  useAdminMutation,
  send,
} from './api';
import { Device, Task, Source, ACL, ModelPackage, DataLocation } from './types';
import { useCatalog, useStackServices } from './catalog';
import {
  Editor,
  TextField,
  ChoiceField,
  CheckField,
  ErrorNotice,
  Notice,
  Records,
  Raw,
  statusLabel,
  timestamp,
  taskKinds,
  modelSlots,
  slotLabel,
} from './components';

export function ACLFields({
  catalog,
}: {
  catalog: NonNullable<ReturnType<typeof useCatalog>['data']>;
}) {
  const { values, setFieldValue } = useFormikContext<ACL>();
  return (
    <>
      <FormControl label="允许查看的用户" inputId="business-users-select">
        <div data-edgestack-raw>
          <UsersSelector
            value={values.userIds || []}
            onChange={(v) => setFieldValue('userIds', v)}
            users={catalog.users}
            dataCy="business-authorized-users"
            inputId="business-users-select"
            placeholder="选择已有用户"
          />
        </div>
      </FormControl>
      <FormControl label="允许查看的团队" inputId="business-teams-select">
        <div data-edgestack-raw>
          <TeamsSelector
            value={values.teamIds || []}
            onChange={(v) => setFieldValue('teamIds', v)}
            teams={catalog.teams}
            dataCy="business-authorized-teams"
            inputId="business-teams-select"
            placeholder="选择已有团队"
          />
        </div>
      </FormControl>
      <Notice>
        业务授权与原生环境权限取交集；此处保存不会增加 Docker
        操作权限。设备与任务均须授权，空授权仅管理员可见。
      </Notice>
    </>
  );
}

export function EntityPage({
  kind,
  id,
  isNew,
}: {
  kind: 'devices' | 'tasks' | 'sources';
  id?: string;
  isNew: boolean;
}) {
  const router = useRouter();
  const [search, setSearch] = useState('');
  const [archived, setArchived] = useState('');
  const query = useAdminList<Device | Task | Source>(kind, {
    q: search,
    ...(archived ? { archived } : {}),
  });
  const item = useAdminItem<Device | Task | Source>(kind, id);
  const catalog = useCatalog();
  const devices = useAdminChoices<Device>('devices');
  const tasks = useAdminChoices<Task>('tasks');
  const mutation = useAdminMutation((value: Device | Task | Source) =>
    id
      ? send(
          'PATCH',
          `${kind}/${encodeURIComponent(id)}`,
          editable(kind, value, item.data)
        )
      : send('POST', kind, createPayload(value))
  );
  const title = {
    devices: '设备业务登记',
    tasks: '业务任务',
    sources: '事件来源',
  }[kind];
  function close() {
    return router.stateService.go(`portainer.business.${kind}`);
  }
  const columns: ColumnDef<Device | Task | Source>[] = [
    {
      accessorKey: 'name',
      header: '名称',
      cell: ({ row }) => (
        <Button
          color="link"
          onClick={() =>
            router.stateService.go(`portainer.business.${kind}.item`, {
              id: row.original.id,
            })
          }
          data-cy="business-edit-record"
        >
          <Raw>{row.original.name}</Raw>
        </Button>
      ),
    },
    { accessorKey: 'id', header: '登记标识' },
    {
      id: 'state',
      header: '状态',
      accessorFn: (r) =>
        r.archived
          ? '已归档'
          : 'runtimeState' in r
            ? statusLabel(r.runtimeState)
            : 'status' in r
              ? statusLabel(r.status)
              : '已登记',
    },
    { accessorKey: 'description', header: '说明' },
  ];
  return (
    <>
      <ErrorNotice error={query.error || item.error || catalog.error} />
      {!id && !isNew && (
        <>
          <div className="mb-4 flex gap-2">
            <Select
              data-edgestack-raw
              aria-label="归档筛选"
              value={archived}
              onChange={(e) => setArchived(e.target.value)}
              options={[
                { value: '', label: '全部登记' },
                { value: 'false', label: '当前登记' },
                { value: 'true', label: '已归档' },
              ]}
              data-cy="business-archive-filter"
            />
            <Input
              aria-label="搜索业务资料"
              value={search}
              onChange={(e) => setSearch(e.target.value)}
              placeholder="按名称或标识搜索"
              data-cy="business-search"
            />
            <Button
              onClick={() =>
                router.stateService.go(`portainer.business.${kind}.new`)
              }
              data-cy="business-add-record"
            >
              新增登记
            </Button>
          </div>
          <Records
            name={kind}
            title={title}
            items={query.items}
            columns={columns}
            loading={query.isLoading}
          />
          {query.hasNextPage && (
            <Button
              color="default"
              onClick={() => query.fetchNextPage()}
              disabled={query.isFetchingNextPage}
              data-cy="business-load-more"
            >
              继续载入记录（已载入 {query.items.length}）
            </Button>
          )}
        </>
      )}
      {(id || isNew) && (
        <div className="mb-4">
          <Button color="default" onClick={close} data-cy="business-back">
            返回列表
          </Button>
        </div>
      )}
      {(isNew || id) && catalog.data && (!id || item.data) && (
        <Editor
          key={`${kind}-${id || 'new'}-${item.data?.metadataVersion || 0}`}
          title={isNew ? `新增${title}` : `编辑${title}`}
          initialValues={
            item.data ? normalizeEntity(item.data) : emptyEntity(kind)
          }
          validate={validateEntity}
          onSubmit={(v) => mutation.mutateAsync(v)}
          onCancel={close}
        >
          <TextField
            name="id"
            label="登记标识"
            disabled={!isNew}
            required
            hint="使用字母、数字、点、短横线或下划线；登记后不可更换。"
          />
          <TextField name="name" label="用户显示名称" required />
          <TextField name="description" label="业务说明" multiline />
          {kind === 'devices' && (
            <>
              <ChoiceField
                name="endpointId"
                label="原生设备"
                disabled={!isNew}
                options={[
                  { value: '', label: '选择已接入的环境' },
                  ...catalog.data.environments.map((e) => ({
                    value: String(e.Id),
                    label: `${e.Name}（${e.Id}）`,
                  })),
                ]}
              />
              <ChoiceField
                name="architecture"
                label="设备架构"
                disabled={!isNew}
                options={[
                  { value: '', label: '选择已核对的架构' },
                  { value: 'arm64', label: 'ARM64' },
                  { value: 'amd64', label: 'AMD64' },
                ]}
              />
              <ACLFields catalog={catalog.data} />
              {id && item.data && 'endpointId' in item.data && (
                <>
                  <DeviceRuntime id={id} />
                  <Link
                    to="portainer.endpoints.endpoint"
                    params={{ id: item.data.endpointId }}
                    data-cy="business-native-device"
                  >
                    打开原生设备管理
                  </Link>
                </>
              )}
            </>
          )}
          {kind === 'tasks' && (
            <>
              <TaskFields
                devices={devices.items}
                catalog={catalog.data}
                isNew={isNew}
              />
              <ACLFields catalog={catalog.data} />
            </>
          )}
          {kind === 'sources' && (
            <SourceFields
              devices={devices.items}
              tasks={tasks.items}
              isNew={isNew}
              source={item.data as Source | undefined}
            />
          )}
          {!isNew && (
            <CheckField name="archived" label="归档此登记并保留历史" />
          )}
        </Editor>
      )}
      {(isNew || id) && (catalog.isLoading || (!!id && item.isLoading)) && (
        <Widget>
          <Widget.Body>正在读取登记资料…</Widget.Body>
        </Widget>
      )}
    </>
  );
}

function emptyEntity(
  kind: 'devices' | 'tasks' | 'sources'
): Device | Task | Source {
  const base = {
    id: '',
    name: '',
    description: '',
    metadataVersion: 0,
    archived: false,
  };
  if (kind === 'devices')
    return {
      ...base,
      endpointId: 0,
      architecture: 'arm64',
      userIds: [],
      teamIds: [],
    };
  if (kind === 'sources')
    return {
      ...base,
      deviceId: '',
      taskId: '',
      type: 'voice.events',
      enabled: true,
    };
  return {
    ...base,
    deviceId: '',
    kind: 'voice-test',
    state: 'registered',
    revision: 1,
    target: { endpointId: 0, stackId: 0, serviceId: '' },
    userIds: [],
    teamIds: [],
    models: [],
    dataLocations: [],
    capabilities: [],
    modelEnvKeys: [],
    testTask: false,
    runtimeBackend: '',
    templateId: '',
  };
}

export function validateEntity(value: Device | Task | Source) {
  const errors: Record<string, string> = {};
  if (!/^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,127}$/.test(value.id))
    errors.id = '请输入有效登记标识。';
  if (!value.name.trim()) errors.name = '请输入用户可理解的名称。';
  if ('endpointId' in value && !Number(value.endpointId))
    errors.endpointId = '请选择原生环境。';
  if ('deviceId' in value && !value.deviceId)
    errors.deviceId = '请选择业务设备。';
  if ('taskId' in value && !value.taskId) errors.taskId = '请选择任务。';
  return errors;
}

export function editable(
  kind: string,
  value: Device | Task | Source,
  initial?: Device | Task | Source
) {
  const basic = {
    metadataVersion: value.metadataVersion,
    name: value.name,
    description: value.description || '',
    archived: !!value.archived,
  };
  if (kind === 'sources')
    return { ...basic, enabled: (value as Source).enabled };
  const acl = {
    userIds: (value as ACL).userIds || [],
    teamIds: (value as ACL).teamIds || [],
  };
  if (kind === 'devices') return { ...basic, ...acl };
  const task = value as Task;
  const before = initial as Task | undefined;
  const configuration: Partial<Task> = {};
  (
    [
      'capabilities',
      'modelEnvKeys',
      'testTask',
      'runtimeBackend',
      'templateId',
      'voiceOrigin',
      'voiceTokenFile',
    ] as const
  ).forEach((key) => {
    const isList = key === 'capabilities' || key === 'modelEnvKeys';
    const current = isList ? task[key] || [] : task[key];
    const previous = isList ? before?.[key] || [] : before?.[key];
    if (JSON.stringify(current) !== JSON.stringify(previous))
      Object.assign(configuration, { [key]: task[key] });
  });
  return {
    ...basic,
    ...acl,
    dataLocations: registeredLocations(task.dataLocations),
    ...configuration,
  };
}

export function createPayload(value: Device | Task | Source) {
  if ('endpointId' in value)
    return { ...value, endpointId: Number(value.endpointId) };
  if ('target' in value)
    return {
      ...value,
      dataLocations: registeredLocations(value.dataLocations),
      target: {
        ...value.target,
        endpointId: Number(value.target.endpointId),
        stackId: Number(value.target.stackId),
      },
    };
  return value;
}

function registeredLocations(locations: DataLocation[]) {
  return (locations || []).map((location) => {
    const metadata = { ...location } as DataLocation & Record<string, unknown>;
    for (const key of [
      'verification',
      'observedAt',
      'runtimeState',
      'runtimeObservedAt',
      'hostSource',
      'inspect',
      'mounts',
    ])
      delete metadata[key];
    return metadata;
  });
}

function normalizeEntity(
  value: Device | Task | Source
): Device | Task | Source {
  if ('target' in value)
    return {
      ...value,
      models: value.models || [],
      capabilities: value.capabilities || [],
      dataLocations: (value.dataLocations || []).map((loc) =>
        typeof loc === 'string' ? { name: loc, kind: 'business' } : loc
      ),
    };
  return value;
}

function useDeviceRuntime(id: string, enabled = true) {
  return useQuery({
    queryKey: ['edgestack', 'admin', 'runtime', id],
    queryFn: async () =>
      (
        await axios.get<Device & { tasks: Task[] }>(
          `/edgestack/devices/${encodeURIComponent(id)}`
        )
      ).data,
    enabled: !!id && enabled,
    refetchOnWindowFocus: false,
    retry: false,
  });
}
function DeviceRuntime({ id }: { id: string }) {
  const query = useDeviceRuntime(id);
  return (
    <p>
      管理连接：{query.data ? (query.data.online ? '在线' : '离线') : '待确认'}
      ；最近联系 {timestamp(query.data?.lastSeenAt)}。管理在线不代表业务健康。
    </p>
  );
}

function TaskFields({
  devices,
  catalog,
  isNew,
}: {
  devices: Device[];
  catalog: NonNullable<ReturnType<typeof useCatalog>['data']>;
  isNew: boolean;
}) {
  const { values, setFieldValue } = useFormikContext<Task>();
  const packages = useAdminChoices<ModelPackage>('models');
  const services = useStackServices(Number(values.target.stackId));
  const runtime = useDeviceRuntime(values.deviceId, !isNew);
  const observed = runtime.data?.tasks.find((t) => t.id === values.id);
  return (
    <>
      <FormControl label="业务设备" inputId="business-task-device">
        <select
          id="business-task-device"
          data-edgestack-raw
          className="form-control"
          value={values.deviceId}
          disabled={!isNew}
          data-cy="business-deviceId"
          onChange={(e) => {
            setFieldValue('deviceId', e.target.value);
            setFieldValue(
              'target.endpointId',
              devices.find((d) => d.id === e.target.value)?.endpointId || 0
            );
          }}
        >
          <option value="">选择业务设备</option>
          {devices.map((d) => (
            <option key={d.id} value={d.id}>
              {d.name}
            </option>
          ))}
        </select>
      </FormControl>
      <ChoiceField
        name="kind"
        label="任务用途"
        disabled={!isNew}
        options={taskKinds}
      />
      <ChoiceField
        name="target.stackId"
        label="已有 Edge Stack"
        disabled={!isNew}
        options={[
          { value: '0', label: '选择独立部署的 Stack' },
          ...catalog.stacks.map((s) => ({
            value: String(s.Id),
            label: s.Name,
          })),
        ]}
      />
      <ChoiceField
        name="target.serviceId"
        label="Stack 中的业务服务"
        disabled={!isNew}
        options={[
          {
            value: '',
            label: services.isLoading ? '正在读取服务…' : '选择服务',
          },
          ...(services.data || []).map((s) => ({ value: s, label: s })),
        ]}
      />
      <ErrorNotice error={services.error} />
      <Notice>
        此处只登记已有单设备部署。目标登记后固定；共享多设备 Stack
        须先拆分。任务实例运行与模型加载事实由 Server 观察。
      </Notice>
      <TextField
        name="runtimeBackend"
        label="运行后端标识"
        hint="与模型发布包的运行后端要求一致，例如已登记的执行后端。"
      />
      <TextField name="templateId" label="兼容模板标识" />
      <CheckField name="testTask" label="此任务为独立受控测试任务" />
      <details className="mb-4">
        <summary>语音接入测试的受控代理设置</summary>
        <TextField
          name="voiceOrigin"
          label="已登记的语音服务地址"
          hint="仅代理此任务的受控语音接口，不授予任意网络或文件访问。"
        />
        <TextField
          name="voiceTokenFile"
          label="Server 私有凭据文件路径"
          hint="只登记文件路径，不在此输入或上传凭据明文。"
        />
        {!isNew && values.capabilities.includes('voice-test') && (
          <a
            href={`api/edgestack/tasks/${encodeURIComponent(values.id)}/voice-test`}
            target="_blank"
            rel="noreferrer"
            data-cy="business-voice-test"
          >
            打开语音接入测试（回声回复）
          </a>
        )}
      </details>
      <ArrayChoices
        name="capabilities"
        label="用户可用能力"
        options={[
          { value: 'replace-model', label: '受控更换模型' },
          { value: 'voice-test', label: '语音接入测试' },
        ]}
      />
      <StringArrayField name="modelEnvKeys" label="允许模型更改的环境变量名" />
      {isNew && (
        <FieldArray name="models">
          {({ push, remove }) => (
            <>
              <h4>登记模型绑定</h4>
              {values.models.map((binding, i) => (
                <div key={i} className="mb-2 rounded border p-3">
                  <ChoiceField
                    name={`models.${i}.slot`}
                    label="用途"
                    options={modelSlots}
                  />
                  <ChoiceField
                    name={`models.${i}.packageId`}
                    label="当前登记模型包"
                    options={[
                      { value: '', label: '选择模型包' },
                      ...packages.items.map((p) => ({
                        value: p.id,
                        label: `${p.name} · ${p.version}`,
                      })),
                    ]}
                  />
                  <Button
                    color="default"
                    onClick={() => remove(i)}
                    data-cy={`business-remove-binding-${i}`}
                  >
                    移除此绑定
                  </Button>
                </div>
              ))}
              <Button
                color="default"
                onClick={() =>
                  push({ slot: 'llm', packageId: '', state: 'unobserved' })
                }
                data-cy="business-add-binding"
              >
                添加模型绑定
              </Button>
            </>
          )}
        </FieldArray>
      )}
      {!isNew && (
        <>
          <h4>模型与数据运行事实</h4>
          <p>
            {statusLabel(observed?.runtimeState)} · 最近观察{' '}
            {timestamp(observed?.runtimeObservedAt)}
          </p>
          {(observed?.models || values.models).map((m) => (
            <p key={m.slot} data-edgestack-raw>
              {slotLabel(m.slot)}：{m.name || m.packageId} ·{' '}
              {statusLabel(m.state)} · 最近确认 {timestamp(m.observedAt)}
            </p>
          ))}
          {observed?.dataLocations?.map((loc, i) => (
            <p key={i} data-edgestack-raw>
              {loc.name}：
              {loc.kind === 'server'
                ? '由管理中枢保存，按服务端保留设置管理'
                : loc.verification === 'verified'
                  ? '已核验登记路径的实际挂载'
                  : '管理员登记，实际挂载待核验'}
            </p>
          ))}
          <Notice>
            编辑名称、说明、授权或数据用途不改部署版本，也不重置模型回执；更改允许的配置项会增加部署版本。
          </Notice>
        </>
      )}
      <FieldArray name="dataLocations">
        {({ push, remove }) => (
          <>
            <h4>数据位置与用途</h4>
            {values.dataLocations.map((loc, i) => (
              <div key={i} className="mb-2 rounded border p-3">
                <TextField
                  name={`dataLocations.${i}.name`}
                  label="业务用途"
                  required
                />
                <ChoiceField
                  name={`dataLocations.${i}.kind`}
                  label="位置类型"
                  options={[
                    { value: 'business', label: '业务数据' },
                    { value: 'model', label: '模型文件' },
                    { value: 'server', label: '管理中枢记录' },
                  ]}
                />
                {loc.kind === 'server' ? (
                  <Notice>由管理中枢保存，按服务端保留设置管理。</Notice>
                ) : (
                  <>
                    <TextField
                      name={`dataLocations.${i}.containerPath`}
                      label="容器内登记路径"
                    />
                    <TextField
                      name={`dataLocations.${i}.volumeName`}
                      label="预期数据卷名称"
                    />
                    <CheckField
                      name={`dataLocations.${i}.expectedReadOnly`}
                      label="预期只读挂载"
                    />
                    <Notice>管理员登记，实时核验结果见上方运行事实。</Notice>
                  </>
                )}
                <Button
                  color="default"
                  onClick={() => remove(i)}
                  data-cy={`business-remove-location-${i}`}
                >
                  移除位置登记
                </Button>
              </div>
            ))}
            <Button
              color="default"
              onClick={() =>
                push({
                  name: '',
                  kind: 'business',
                  containerPath: '',
                  volumeName: '',
                  expectedReadOnly: false,
                })
              }
              data-cy="business-add-location"
            >
              添加数据位置
            </Button>
          </>
        )}
      </FieldArray>
    </>
  );
}

export function ArrayChoices({
  name,
  label,
  options,
}: {
  name: string;
  label: string;
  options: { value: string; label: string }[];
}) {
  const { values, setFieldValue } =
    useFormikContext<Record<string, string[]>>();
  const value = values[name] || [];
  return (
    <FormControl label={label}>
      <div className="flex flex-wrap gap-4">
        {options.map((o) => (
          <label key={o.value}>
            <input
              type="checkbox"
              checked={value.includes(o.value)}
              onChange={(e) =>
                setFieldValue(
                  name,
                  e.target.checked
                    ? [...value, o.value]
                    : value.filter((v) => v !== o.value)
                )
              }
            />{' '}
            {o.label}
          </label>
        ))}
      </div>
    </FormControl>
  );
}

export function StringArrayField({
  name,
  label,
}: {
  name: string;
  label: string;
}) {
  const { values, setFieldValue } =
    useFormikContext<Record<string, string[]>>();
  const [field, meta] = useField<string[]>(name);
  const [text, setText] = useState((values[name] || []).join(', '));
  return (
    <FormControl
      label={label}
      inputId={`business-${name}`}
      errors={meta.touched && meta.error}
    >
      <Input
        id={`business-${name}`}
        name={field.name}
        onBlur={field.onBlur}
        value={text}
        onChange={(e) => {
          setText(e.target.value);
          setFieldValue(
            name,
            e.target.value
              .split(',')
              .map((v) => v.trim())
              .filter(Boolean)
          );
        }}
        data-cy={`business-${name}`}
      />
      <span className="help-block small">多个值用逗号分隔。</span>
    </FormControl>
  );
}

function SourceFields({
  devices,
  tasks,
  isNew,
  source,
}: {
  devices: Device[];
  tasks: Task[];
  isNew: boolean;
  source?: Source;
}) {
  const { values, setFieldValue } = useFormikContext<Source>();
  return (
    <>
      <FormControl label="业务设备" inputId="business-source-device">
        <select
          id="business-source-device"
          data-edgestack-raw
          className="form-control"
          disabled={!isNew}
          value={values.deviceId}
          onChange={(e) => {
            setFieldValue('deviceId', e.target.value);
            setFieldValue('taskId', '');
          }}
          data-cy="business-source-device"
        >
          <option value="">选择业务设备</option>
          {devices.map((d) => (
            <option key={d.id} value={d.id}>
              {d.name}
            </option>
          ))}
        </select>
      </FormControl>
      <ChoiceField
        name="taskId"
        label="业务任务"
        disabled={!isNew}
        options={[
          { value: '', label: '选择任务' },
          ...tasks
            .filter((t) => t.deviceId === values.deviceId)
            .map((t) => ({ value: t.id, label: t.name })),
        ]}
      />
      <ChoiceField
        name="type"
        label="来源类型"
        disabled={!isNew}
        options={[
          ...(![
            'voice.events',
            'camera.observation',
            'model.observer',
          ].includes(values.type)
            ? [{ value: values.type, label: '历史登记类型（绑定保持不变）' }]
            : []),
          { value: 'voice.events', label: '语音业务事件' },
          { value: 'camera.observation', label: '视觉观察事件' },
          { value: 'model.observer', label: '模型加载确认' },
        ]}
      />
      <CheckField name="enabled" label="允许接收此来源上报" />
      {source && (
        <>
          <p>
            来源状态：{statusLabel(source.status)}；最近联系{' '}
            {timestamp(source.lastSeenAt)}；最近业务事件{' '}
            {timestamp(source.lastEventAt)}
          </p>
          <details>
            <summary>检查点与覆盖信息</summary>
            <pre className="whitespace-pre-wrap text-xs">
              {JSON.stringify(
                {
                  checkpoint: source.checkpoint,
                  coverage: source.coverage,
                  errorCode: source.errorCode,
                },
                null,
                2
              )}
            </pre>
          </details>
        </>
      )}
      <Notice>
        来源类型和绑定登记后固定。模型确认来源不计入对话记录；启用来源不代表已收到业务事件。
      </Notice>
    </>
  );
}
