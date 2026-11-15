import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';

import axios from '@/portainer/services/axios/axios';
import { withTestRouter } from '@/react/test-utils/withRouter';

import { EntityPage } from './EntityPages';

vi.mock('@/portainer/services/axios/axios', () => ({
  default: { get: vi.fn(), request: vi.fn() },
}));
const device = {
  id: 'example-device',
  name: '示例设备',
  metadataVersion: 2,
  endpointId: 4,
  architecture: 'arm64',
  userIds: [],
  teamIds: [],
};
function setup(isNew = false) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  const View = withTestRouter(
    () => (
      <QueryClientProvider client={client}>
        <EntityPage
          kind="devices"
          id={isNew ? undefined : device.id}
          isNew={isNew}
        />
      </QueryClientProvider>
    ),
    {
      stateConfig: [
        { name: 'portainer', abstract: true },
        { name: 'portainer.business', abstract: true },
        { name: 'portainer.business.devices' },
        { name: 'portainer.endpoints', abstract: true },
        { name: 'portainer.endpoints.endpoint', url: '/:id' },
      ],
    }
  );
  render(<View />);
}
beforeEach(() => {
  vi.mocked(axios.get).mockReset();
  vi.mocked(axios.request).mockReset();
  vi.mocked(axios.get).mockImplementation(async (url) => ({
    data:
      url === '/users' || url === '/teams' || url === '/edge_stacks'
        ? []
        : url === '/endpoints'
          ? [{ Id: 4, Name: '示例原生环境', Type: 4 }]
          : String(url).endsWith('/devices/example-device')
            ? device
            : {
                items: String(url).endsWith('/devices') ? [device] : [],
                page: 1,
                pageSize: 100,
                totalCount: 1,
              },
  }));
});

it('creates a device registration using an existing native environment', async () => {
  vi.mocked(axios.request).mockResolvedValue({
    data: { ...device, metadataVersion: 1 },
  });
  setup(true);
  await userEvent.type(
    await screen.findByLabelText(/登记标识/),
    'new-example-device'
  );
  await userEvent.type(screen.getByLabelText(/用户显示名称/), '新设备');
  await userEvent.selectOptions(screen.getByLabelText('原生设备'), '4');
  await userEvent.click(screen.getByRole('button', { name: '保存' }));
  await waitFor(() =>
    expect(axios.request).toHaveBeenCalledWith(
      expect.objectContaining({
        method: 'POST',
        url: '/edgestack/admin/devices',
        data: expect.objectContaining({
          id: 'new-example-device',
          name: '新设备',
          endpointId: 4,
          userIds: [],
          teamIds: [],
        }),
      })
    )
  );
});

it('keeps unsaved metadata on CAS conflict and excludes immutable target fields', async () => {
  const log = vi.spyOn(console, 'error').mockImplementation(() => {});
  try {
    vi.mocked(axios.request).mockRejectedValue({
      response: {
        status: 409,
        data: { errorCode: 'metadata_version_conflict' },
      },
    });
    setup();
    const name = await screen.findByLabelText(/用户显示名称/);
    await userEvent.clear(name);
    await userEvent.type(name, '保留此输入');
    await userEvent.click(screen.getByRole('button', { name: '保存' }));
    expect(await screen.findByRole('alert')).toHaveTextContent('刷新');
    expect(name).toHaveValue('保留此输入');
    expect(screen.getByLabelText(/登记标识/)).toBeDisabled();
    const request = vi.mocked(axios.request).mock.calls[0][0];
    expect(request.data).toMatchObject({
      metadataVersion: 2,
      name: '保留此输入',
    });
    expect(request.data).not.toHaveProperty('endpointId');
    expect(request.data).not.toHaveProperty('architecture');
  } finally {
    log.mockRestore();
  }
});
