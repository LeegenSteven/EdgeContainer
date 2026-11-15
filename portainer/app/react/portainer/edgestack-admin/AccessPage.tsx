import { useState } from 'react';
import { useQuery } from '@tanstack/react-query';

import { Widget } from '@@/Widget';
import { Select } from '@@/form-components/Input/Select';
import { FormControl } from '@@/form-components/FormControl';
import { Link } from '@@/Link';
import { Button } from '@@/buttons';

import { Device, Task } from './types';
import { useAdminChoices, useAdminMutation, send, get, adminKey } from './api';
import { useCatalog } from './catalog';
import { ACLFields } from './EntityPages';
import { Editor, ErrorNotice, Notice } from './components';

interface AccessPreview {
  userId: number;
  deviceId: string;
  taskId?: string;
  deviceBusinessAccess: boolean;
  taskBusinessAccess: boolean;
  nativeEndpointAccess: boolean | null;
  effectiveAccess: boolean;
  missing: string[];
}

export function AccessPage() {
  const catalog = useCatalog();
  const devices = useAdminChoices<Device>('devices');
  const tasks = useAdminChoices<Task>('tasks');
  const [deviceId, setDeviceId] = useState('');
  const [taskId, setTaskId] = useState('');
  const [userId, setUserId] = useState('');
  const [editing, setEditing] = useState(false);
  const device = devices.items.find((d) => d.id === deviceId);
  const task = tasks.items.find((t) => t.id === taskId);
  const target = task || device;
  const mutation = useAdminMutation((value: Device | Task) =>
    send(
      'PATCH',
      `${task ? 'tasks' : 'devices'}/${encodeURIComponent(value.id)}`,
      {
        metadataVersion: value.metadataVersion,
        userIds: value.userIds,
        teamIds: value.teamIds,
      }
    )
  );
  const preview = useQuery({
    queryKey: [
      ...adminKey,
      'access-preview',
      userId,
      deviceId,
      taskId,
      target?.metadataVersion,
    ],
    queryFn: () =>
      get<AccessPreview>('access-preview', {
        userId,
        deviceId,
        ...(taskId ? { taskId } : {}),
      }),
    enabled: !!userId && !!deviceId,
    refetchOnWindowFocus: false,
  });
  return (
    <>
      <ErrorNotice
        error={catalog.error || devices.error || tasks.error || preview.error}
      />
      <Widget>
        <Widget.Title title="业务授权与有效权限" />
        <Widget.Body>
          <Notice>
            此页只编辑业务查看范围，不授予 Docker、Stack
            或容器操作权限。有效访问同时要求设备、任务业务授权及原生环境权限。
          </Notice>
          <FormControl label="业务设备" inputId="access-device">
            <Select
              data-edgestack-raw
              id="access-device"
              value={deviceId}
              options={[
                { value: '', label: '选择设备' },
                ...devices.items.map((d) => ({ value: d.id, label: d.name })),
              ]}
              onChange={(e) => {
                setDeviceId(e.target.value);
                setTaskId('');
                setEditing(false);
              }}
              data-cy="access-device"
            />
          </FormControl>
          <FormControl label="授权对象" inputId="access-task">
            <Select
              data-edgestack-raw
              id="access-task"
              value={taskId}
              options={[
                { value: '', label: '设备级业务授权' },
                ...tasks.items
                  .filter((t) => t.deviceId === deviceId)
                  .map((t) => ({ value: t.id, label: t.name })),
              ]}
              onChange={(e) => {
                setTaskId(e.target.value);
                setEditing(false);
              }}
              data-cy="access-task"
            />
          </FormControl>
          {target && catalog.data && !editing && (
            <>
              <p>
                当前授权：{(target.userIds || []).length} 位用户、
                {(target.teamIds || []).length} 个团队。
              </p>
              <Button onClick={() => setEditing(true)} data-cy="access-edit">
                编辑业务授权
              </Button>
            </>
          )}
          {target && catalog.data && editing && (
            <Editor
              key={`${target.id}-${target.metadataVersion}`}
              title={`授权：${target.name}`}
              initialValues={target}
              onSubmit={(v) => mutation.mutateAsync(v)}
              onCancel={() => setEditing(false)}
            >
              <ACLFields catalog={catalog.data} />
            </Editor>
          )}
          <h4>检查指定用户的实际访问条件</h4>
          <FormControl label="已有用户" inputId="access-user">
            <Select
              data-edgestack-raw
              id="access-user"
              value={userId}
              options={[
                { value: '', label: '选择要核对的用户' },
                ...(catalog.data?.users || []).map((u) => ({
                  value: String(u.Id),
                  label: u.Username,
                })),
              ]}
              onChange={(e) => setUserId(e.target.value)}
              data-cy="access-user"
            />
          </FormControl>
          {preview.data && (
            <div data-cy="access-preview">
              <p>
                设备业务授权：
                {preview.data.deviceBusinessAccess ? '满足' : '未满足'}
              </p>
              {taskId && (
                <p>
                  任务业务授权：
                  {preview.data.taskBusinessAccess ? '满足' : '未满足'}
                </p>
              )}
              <p>
                原生环境权限：
                {preview.data.nativeEndpointAccess === null
                  ? '暂无法确认'
                  : preview.data.nativeEndpointAccess
                    ? '满足'
                    : '未满足'}
              </p>
              <p>
                <strong>
                  有效访问：
                  {preview.data.effectiveAccess ? '允许' : '未满足访问条件'}
                </strong>
              </p>
              {preview.data.missing.length > 0 && (
                <p className="text-muted">请补齐上述未满足的授权后重新核对。</p>
              )}
            </div>
          )}
          {device && (
            <Link
              to="portainer.endpoints.endpoint.access"
              params={{ id: device.endpointId }}
              data-cy="access-native-link"
            >
              打开原生环境权限页
            </Link>
          )}
        </Widget.Body>
      </Widget>
    </>
  );
}
