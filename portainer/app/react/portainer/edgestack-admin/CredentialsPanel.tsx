import { useState } from 'react';
import { ColumnDef } from '@tanstack/react-table';
import { useFormikContext } from 'formik';

import { Button } from '@@/buttons';
import { Modal } from '@@/modals/Modal';

import { useAdminChoices, useAdminList, useAdminMutation, send } from './api';
import { Credential, Source } from './types';
import {
  Editor,
  TextField,
  ChoiceField,
  Confirm,
  ErrorNotice,
  Notice,
  Records,
  Raw,
  timestamp,
} from './components';
import { ArrayChoices } from './EntityPages';

interface CredentialInput {
  name: string;
  sourceId: string;
  capabilities: string[];
}
interface CreatedCredential {
  id: string;
  token: string;
  credential: Credential;
}

export function CredentialsPanel() {
  const query = useAdminList<Credential>('ingest-credentials');
  const sources = useAdminChoices<Source>('sources');
  const [creating, setCreating] = useState(false);
  const [oneTime, setOneTime] = useState<CreatedCredential>();
  const [action, setAction] = useState<{
    kind: 'rotate' | 'revoke';
    credential: Credential;
  }>();
  const create = useAdminMutation(async (value: CredentialInput) => {
    const s = sources.items.find((x) => x.id === value.sourceId);
    if (!s) throw new Error('source_required');
    const result = await send<CreatedCredential>('POST', 'ingest-credentials', {
      name: value.name,
      sourceIds: [s.id],
      taskIds: [s.taskId],
      deviceIds: [s.deviceId],
      capabilities: value.capabilities,
    });
    setOneTime(result);
  });
  const execute = useAdminMutation(async () => {
    if (!action) return;
    if (action.kind === 'rotate') {
      const result = await send<CreatedCredential>(
        'POST',
        `ingest-credentials/${encodeURIComponent(action.credential.id)}/rotate`,
        {}
      );
      setOneTime(result);
    } else
      await send(
        'DELETE',
        `ingest-credentials/${encodeURIComponent(action.credential.id)}`
      );
    setAction(undefined);
  });
  const columns: ColumnDef<Credential>[] = [
    { accessorKey: 'name', header: '名称' },
    { accessorKey: 'id', header: '凭据 ID' },
    {
      accessorKey: 'sourceIds',
      header: '来源范围',
      cell: ({ row }) => <Raw>{row.original.sourceIds.join('、')}</Raw>,
    },
    {
      id: 'state',
      header: '状态',
      accessorFn: (c) => (c.revoked ? '已撤销' : '可用'),
    },
    {
      accessorKey: 'lastUsedAt',
      header: '最近使用',
      cell: ({ row }) => timestamp(row.original.lastUsedAt),
    },
    {
      accessorKey: 'lastSuccessfulAt',
      header: '最近成功上报',
      cell: ({ row }) => timestamp(row.original.lastSuccessfulAt),
    },
    {
      id: 'actions',
      header: '操作',
      cell: ({ row }) => (
        <>
          <Button
            color="link"
            disabled={row.original.revoked}
            onClick={() =>
              setAction({ kind: 'rotate', credential: row.original })
            }
            data-cy="credential-rotate"
          >
            换发
          </Button>
          <Button
            color="link"
            disabled={row.original.revoked}
            onClick={() =>
              setAction({ kind: 'revoke', credential: row.original })
            }
            data-cy="credential-revoke"
          >
            撤销
          </Button>
        </>
      ),
    },
  ];
  return (
    <>
      <ErrorNotice error={query.error || sources.error} />
      <Notice>
        换发顺序：创建新凭据 → 用新凭据成功上报 →
        撤销旧凭据。短暂并行用于切换；此页从不重新读取已创建的明文。
      </Notice>
      <Records
        name="credentials"
        title="来源上报凭据"
        items={query.items}
        columns={columns}
        loading={query.isLoading}
        actions={
          <Button onClick={() => setCreating(true)} data-cy="credential-create">
            创建凭据
          </Button>
        }
      />
      {query.hasNextPage && (
        <Button
          color="default"
          onClick={() => query.fetchNextPage()}
          data-cy="credentials-more"
        >
          继续载入
        </Button>
      )}
      {creating && (
        <Editor
          title="创建限定来源凭据"
          initialValues={{
            name: '',
            sourceId: '',
            capabilities: [] as string[],
          }}
          validate={(v) => ({
            ...(v.name.trim() ? {} : { name: '请输入凭据名称。' }),
            ...(v.sourceId ? {} : { sourceId: '请选择来源。' }),
          })}
          onSubmit={(v) => create.mutateAsync(v)}
          onCancel={() => setCreating(false)}
        >
          <TextField name="name" label="凭据名称" required />
          <ChoiceField
            name="sourceId"
            label="固定绑定来源"
            options={[
              { value: '', label: '选择来源' },
              ...sources.items
                .filter((s) => !s.archived)
                .map((s) => ({ value: s.id, label: `${s.name} · ${s.id}` })),
            ]}
          />
          <CredentialCapabilities sources={sources.items} />
          <Notice>
            设备与任务范围由来源绑定自动确定，不允许创建不受范围约束的上报凭据。
          </Notice>
        </Editor>
      )}
      {oneTime && (
        <Modal
          aria-label="仅显示一次的上报凭据"
          onDismiss={() => setOneTime(undefined)}
        >
          <h3>仅显示这一次</h3>
          <p>
            凭据 ID：<Raw>{oneTime.id}</Raw>
          </p>
          <p>
            请立即复制到 Worker
            私有凭据文件。关闭后无法再次查看；未保存请重新换发。
          </p>
          <pre
            className="whitespace-pre-wrap break-all"
            data-cy="credential-once"
          >
            {oneTime.token}
          </pre>
          <Button
            onClick={() => setOneTime(undefined)}
            data-cy="credential-dismiss"
          >
            我已保存，关闭明文
          </Button>
        </Modal>
      )}
      {action && (
        <Confirm
          title={action.kind === 'rotate' ? '换发来源凭据' : '撤销来源凭据'}
          onClose={() => setAction(undefined)}
          onConfirm={() => execute.mutate()}
          busy={execute.isLoading}
          error={execute.error}
        >
          <p>
            <Raw>{action.credential.name}</Raw>
          </p>
          <p>
            凭据 ID：<Raw>{action.credential.id}</Raw>
          </p>
          <Notice>
            {action.kind === 'rotate'
              ? '将创建新凭据，旧凭据继续有效。请验证新凭据成功上报后再撤销旧凭据。'
              : '撤销后此凭据无法继续上报。请先确认新凭据已经成功接收记录。'}
          </Notice>
        </Confirm>
      )}
    </>
  );
}

function CredentialCapabilities({ sources }: { sources: Source[] }) {
  const { values } = useFormikContext<CredentialInput>();
  const model =
    sources.find((s) => s.id === values.sourceId)?.type === 'model.observer';
  return (
    <ArrayChoices
      name="capabilities"
      label="所需上报能力"
      options={
        model
          ? [
              { value: 'observe-model', label: '提交模型加载回执' },
              { value: 'ingest-status', label: '提交来源状态' },
            ]
          : [
              { value: 'ingest-events', label: '提交业务事件' },
              { value: 'ingest-status', label: '提交来源状态' },
            ]
      }
    />
  );
}
