import { useState } from 'react';
import { useRouter } from '@uirouter/react';
import { ColumnDef } from '@tanstack/react-table';
import { useQuery } from '@tanstack/react-query';

import axios from '@/portainer/services/axios/axios';

import { Widget } from '@@/Widget';
import { Button } from '@@/buttons';
import { Input } from '@@/form-components/Input';
import { Select } from '@@/form-components/Input/Select';
import { FormControl } from '@@/form-components/FormControl';

import { Operation, Audit, Task, Device, Preview } from './types';
import {
  useAdminChoices,
  useAdminList,
  useAdminMutation,
  send,
  errorMessage,
} from './api';
import {
  Confirm,
  ErrorNotice,
  Notice,
  Records,
  Raw,
  statusLabel,
  timestamp,
} from './components';
import { PreviewDetails } from './ModelsPage';

interface ImportPlan {
  id: string;
  expiresAt: string;
  changes: {
    kind: string;
    id: string;
    action: string;
    fields: string[];
    metadataVersion: number;
  }[];
}

export function OperationsPage({ id }: { id?: string }) {
  const router = useRouter();
  const [filters, setFilters] = useState({
    deviceId: '',
    taskId: '',
    status: '',
    result: '',
    actorUserId: '',
    from: '',
    to: '',
  });
  const operations = useAdminList<Operation>(
    'operations',
    filterParams(filters, 'operations')
  );
  const audit = useAdminList<Audit>('audit', filterParams(filters, 'audit'));
  const devices = useAdminChoices<Device>('devices');
  const tasks = useAdminChoices<Task>('tasks');
  const detail = useQuery({
    queryKey: ['edgestack', 'admin', 'operation', id],
    queryFn: async () =>
      (
        await axios.get<Operation>(
          `/edgestack/operations/${encodeURIComponent(id || '')}`
        )
      ).data,
    enabled: !!id,
    refetchInterval: 3000,
  });
  const [preview, setPreview] = useState<Preview>();
  const restore = useAdminMutation(async (op: Operation) => {
    const task = tasks.items.find((t) => t.id === op.taskId);
    if (!task || !op.previousPackageId) throw new Error('target_required');
    const { data } = await axios.post<Preview>(
      `/edgestack/tasks/${encodeURIComponent(task.id)}/model-change-preview`,
      {
        packageId: op.previousPackageId,
        slot: op.slot,
        revision: task.revision,
      }
    );
    setPreview(data);
  });
  const confirm = useAdminMutation(async () => {
    if (!preview) return;
    const { data } = await axios.post<Operation>(
      `/edgestack/tasks/${encodeURIComponent(preview.taskId)}/model-changes`,
      { previewId: preview.id, confirm: true }
    );
    setPreview(undefined);
    await router.stateService.go('portainer.business.operations.item', {
      id: data.id,
    });
  });
  const columns: ColumnDef<Operation>[] = [
    {
      accessorKey: 'id',
      header: '操作标识',
      cell: ({ row }) => (
        <Button
          color="link"
          onClick={() =>
            router.stateService.go('portainer.business.operations.item', {
              id: row.original.id,
            })
          }
          data-cy="operation-details"
        >
          <Raw>{row.original.id}</Raw>
        </Button>
      ),
    },
    { accessorKey: 'deviceId', header: '设备' },
    { accessorKey: 'taskId', header: '任务' },
    { id: 'status', header: '阶段', accessorFn: (o) => statusLabel(o.status) },
    {
      accessorKey: 'createdAt',
      header: '发起时间',
      cell: ({ row }) => timestamp(row.original.createdAt),
    },
  ];
  const auditColumns: ColumnDef<Audit>[] = [
    {
      accessorKey: 'occurredAt',
      header: '时间',
      cell: ({ row }) => timestamp(row.original.occurredAt),
    },
    {
      accessorKey: 'actorUserId',
      header: '操作者',
      cell: ({ row }) => row.original.actorUserId || '未知',
    },
    { accessorKey: 'action', header: '动作' },
    { accessorKey: 'resourceId', header: '目标' },
    { accessorKey: 'result', header: '结果' },
    {
      accessorKey: 'fields',
      header: '修改字段',
      cell: ({ row }) => <Raw>{(row.original.fields || []).join('、')}</Raw>,
    },
  ];
  return (
    <>
      <ErrorNotice error={operations.error || audit.error || detail.error} />
      <Widget>
        <Widget.Title title="检索管理操作" />
        <Widget.Body>
          <div className="grid grid-cols-1 gap-3 md:grid-cols-3">
            <FormControl
              label="设备"
              size="vertical"
              inputId="operations-device"
            >
              <Select
                data-edgestack-raw
                id="operations-device"
                value={filters.deviceId}
                onChange={(e) =>
                  setFilters({
                    ...filters,
                    deviceId: e.target.value,
                    taskId: '',
                  })
                }
                options={[
                  { value: '', label: '全部设备' },
                  ...devices.items.map((d) => ({ value: d.id, label: d.name })),
                ]}
                data-cy="operations-device"
              />
            </FormControl>
            <FormControl label="任务" size="vertical" inputId="operations-task">
              <Select
                data-edgestack-raw
                id="operations-task"
                value={filters.taskId}
                onChange={(e) =>
                  setFilters({ ...filters, taskId: e.target.value })
                }
                options={[
                  { value: '', label: '全部任务' },
                  ...tasks.items
                    .filter(
                      (t) =>
                        !filters.deviceId || t.deviceId === filters.deviceId
                    )
                    .map((t) => ({ value: t.id, label: t.name })),
                ]}
                data-cy="operations-task"
              />
            </FormControl>
            <FormControl
              label="操作阶段"
              size="vertical"
              inputId="operations-status"
            >
              <Select
                data-edgestack-raw
                id="operations-status"
                value={filters.status}
                onChange={(e) =>
                  setFilters({ ...filters, status: e.target.value })
                }
                options={[
                  { value: '', label: '全部阶段' },
                  ...[
                    'dispatching',
                    'awaiting_observation',
                    'succeeded',
                    'failed',
                    'reconciliation_required',
                  ].map((v) => ({ value: v, label: statusLabel(v) })),
                ]}
                data-cy="operations-status"
              />
            </FormControl>
            <FormControl
              label="审计结果"
              size="vertical"
              inputId="audit-result"
            >
              <Select
                data-edgestack-raw
                id="audit-result"
                value={filters.result}
                onChange={(e) =>
                  setFilters({ ...filters, result: e.target.value })
                }
                options={[
                  { value: '', label: '全部结果' },
                  { value: 'succeeded', label: '成功' },
                  { value: 'failed', label: '失败' },
                ]}
                data-cy="audit-result"
              />
            </FormControl>
            <FormControl
              label="操作者 ID"
              size="vertical"
              inputId="operations-actor"
            >
              <Input
                id="operations-actor"
                type="number"
                min={1}
                value={filters.actorUserId}
                onChange={(e) =>
                  setFilters({ ...filters, actorUserId: e.target.value })
                }
                data-cy="operations-actor"
              />
            </FormControl>
            <FormControl
              label="起始时间"
              size="vertical"
              inputId="operations-from"
            >
              <Input
                id="operations-from"
                type="datetime-local"
                value={filters.from ? localInput(filters.from) : ''}
                onChange={(e) =>
                  setFilters({
                    ...filters,
                    from: e.target.value
                      ? new Date(e.target.value).toISOString()
                      : '',
                  })
                }
                data-cy="operations-from"
              />
            </FormControl>
            <FormControl
              label="结束时间"
              size="vertical"
              inputId="operations-to"
            >
              <Input
                id="operations-to"
                type="datetime-local"
                value={filters.to ? localInput(filters.to) : ''}
                onChange={(e) =>
                  setFilters({
                    ...filters,
                    to: e.target.value
                      ? new Date(e.target.value).toISOString()
                      : '',
                  })
                }
                data-cy="operations-to"
              />
            </FormControl>
          </div>
        </Widget.Body>
      </Widget>
      <Records
        name="operations"
        title="模型运行操作"
        items={operations.items}
        columns={columns}
        loading={operations.isLoading}
      />
      {operations.hasNextPage && (
        <Button
          color="default"
          onClick={() => operations.fetchNextPage()}
          data-cy="operations-more"
        >
          继续载入操作
        </Button>
      )}
      {detail.data && (
        <Widget data-cy="operation-detail">
          <Widget.Title title={`操作 ${detail.data.id}`} />
          <Widget.Body>
            <p>
              阶段：{statusLabel(detail.data.status)}；任务{' '}
              <Raw>{detail.data.taskId}</Raw>
            </p>
            <ol className="flex flex-wrap gap-6">
              <li>预览与确认</li>
              <li
                className={
                  detail.data.status === 'dispatching' ? 'font-bold' : ''
                }
              >
                下发部署
              </li>
              <li
                className={
                  detail.data.status === 'awaiting_observation'
                    ? 'font-bold'
                    : ''
                }
              >
                等待可信回执
              </li>
              <li>
                {['succeeded', 'failed'].includes(detail.data.status)
                  ? statusLabel(detail.data.status)
                  : '待最终确认'}
              </li>
            </ol>
            <p>
              原模型：<Raw>{detail.data.previousPackageId || '未登记'}</Raw> →{' '}
              <Raw>{detail.data.packageId}</Raw>
            </p>
            {detail.data.errorCode && (
              <p role="alert">
                失败原因：
                {errorMessage({
                  response: { data: { errorCode: detail.data.errorCode } },
                })}
              </p>
            )}
            <Notice>
              实例运行、模型加载与业务健康属于不同事实。恢复旧模型会生成新的预览，重新检查发布、兼容性、权限和部署版本。
            </Notice>
            <ErrorNotice error={restore.error} />
            <Button
              color="default"
              disabled={
                !detail.data.previousPackageId ||
                !['succeeded', 'failed'].includes(detail.data.status) ||
                restore.isLoading
              }
              onClick={() => detail.data && restore.mutate(detail.data)}
              data-cy="operation-restore-preview"
            >
              预览恢复旧模型
            </Button>
          </Widget.Body>
        </Widget>
      )}
      <Records
        name="audit"
        title="业务管理审计（不含凭据或对话全文）"
        items={audit.items}
        columns={auditColumns}
        loading={audit.isLoading}
      />
      {audit.hasNextPage && (
        <Button
          color="default"
          onClick={() => audit.fetchNextPage()}
          data-cy="audit-more"
        >
          继续载入审计
        </Button>
      )}
      <ImportPanel />
      {preview && (
        <Confirm
          title="确认恢复旧模型"
          onClose={() => setPreview(undefined)}
          onConfirm={() => confirm.mutate()}
          busy={confirm.isLoading}
          error={confirm.error}
        >
          <PreviewDetails preview={preview} />
        </Confirm>
      )}
    </>
  );
}

export function filterParams(
  filters: Record<string, string>,
  collection: 'operations' | 'audit'
) {
  return Object.fromEntries(
    Object.entries(filters).filter(
      ([key, value]) =>
        value && key !== (collection === 'audit' ? 'status' : 'result')
    )
  );
}

function localInput(iso: string) {
  const d = new Date(iso);
  return new Date(d.getTime() - d.getTimezoneOffset() * 60000)
    .toISOString()
    .slice(0, 16);
}

function ImportPanel() {
  const [text, setText] = useState('');
  const [plan, setPlan] = useState<ImportPlan>();
  const [done, setDone] = useState(false);
  const preview = useAdminMutation(async () => {
    const config = JSON.parse(text) as unknown;
    const p = await send<ImportPlan>('POST', 'imports/preview', { config });
    setPlan(p);
    setDone(false);
  });
  const confirm = useAdminMutation(async () => {
    if (!plan) return;
    await send('POST', 'imports/confirm', {
      previewId: plan.id,
      confirm: true,
    });
    setPlan(undefined);
    setText('');
    setDone(true);
  });
  return (
    <Widget>
      <Widget.Body>
        <details>
          <summary>高级：预览导入业务登记配置</summary>
          <Notice>
            仅用于明确审核的业务配置导入，不上传真实凭据或业务记录。Server
            会给出差异，确认前不应用。
          </Notice>
          <label htmlFor="business-import">业务登记 JSON</label>
          <textarea
            id="business-import"
            rows={5}
            className="form-control"
            value={text}
            onChange={(e) => setText(e.target.value)}
            data-cy="business-import-input"
          />
          <ErrorNotice error={preview.error} />
          <Button
            disabled={!text.trim() || preview.isLoading}
            onClick={() => preview.mutate()}
            data-cy="business-import-preview"
          >
            预览差异
          </Button>
          {done && <p role="status">已应用审核后的业务配置。</p>}
        </details>
        {plan && (
          <Confirm
            title="确认业务配置导入"
            onClose={() => setPlan(undefined)}
            onConfirm={() => confirm.mutate()}
            busy={confirm.isLoading}
            error={confirm.error}
          >
            <Notice>
              有效至 {timestamp(plan.expiresAt)}
              。以下变更将按预期资料版本检查后应用。
            </Notice>
            <ul>
              {plan.changes.map((c) => (
                <li key={`${c.kind}-${c.id}`} data-edgestack-raw>
                  {c.kind} / {c.id}：{c.action}；字段 {c.fields.join('、')}
                </li>
              ))}
            </ul>
          </Confirm>
        )}
      </Widget.Body>
    </Widget>
  );
}
