import { render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';

import axios from '@/portainer/services/axios/axios';
import { withTestRouter } from '@/react/test-utils/withRouter';

import { CredentialsPanel } from './CredentialsPanel';

vi.mock('@/portainer/services/axios/axios', () => ({
  default: { get: vi.fn(), request: vi.fn() },
}));

function setup() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  const View = withTestRouter(() => (
    <QueryClientProvider client={client}>
      <CredentialsPanel />
    </QueryClientProvider>
  ));
  render(<View />);
  return client;
}
beforeEach(() => {
  vi.mocked(axios.get).mockReset();
  vi.mocked(axios.request).mockReset();
  vi.mocked(axios.get).mockImplementation(async (url) => ({
    data: {
      items: String(url).endsWith('sources')
        ? [
            {
              id: 'example-source',
              name: '示例语音来源',
              type: 'voice',
              deviceId: 'example-device',
              taskId: 'example-task',
              enabled: true,
            },
          ]
        : [],
      totalCount: 1,
      page: 1,
      pageSize: 100,
    },
  }));
});

it('shows a newly created token once and removes it when dismissed', async () => {
  vi.mocked(axios.request).mockResolvedValue({
    data: {
      id: 'example-credential',
      token: 'DEMO_NOT_A_REAL_ONCE_TOKEN',
      credential: { id: 'example-credential', name: '测试凭据' },
    },
  });
  const client = setup();
  await userEvent.click(
    await screen.findByRole('button', { name: '创建凭据' })
  );
  await userEvent.type(screen.getByLabelText(/凭据名称/), '示例凭据');
  await userEvent.selectOptions(
    screen.getByLabelText('固定绑定来源'),
    'example-source'
  );
  await userEvent.click(screen.getByLabelText('提交业务事件'));
  await userEvent.click(screen.getByRole('button', { name: '保存' }));
  expect(await screen.findByText('DEMO_NOT_A_REAL_ONCE_TOKEN')).toBeVisible();
  expect(
    within(screen.getByRole('dialog')).getByText('example-credential')
  ).toBeVisible();
  expect(axios.request).toHaveBeenCalledWith(
    expect.objectContaining({
      method: 'POST',
      data: {
        name: '示例凭据',
        deviceIds: ['example-device'],
        taskIds: ['example-task'],
        sourceIds: ['example-source'],
        capabilities: ['ingest-events'],
      },
    })
  );
  await userEvent.click(
    screen.getByRole('button', { name: '我已保存，关闭明文' })
  );
  await waitFor(() =>
    expect(
      screen.queryByText('DEMO_NOT_A_REAL_ONCE_TOKEN')
    ).not.toBeInTheDocument()
  );
  expect(
    JSON.stringify(
      client
        .getQueryCache()
        .getAll()
        .map((q) => q.state.data)
    )
  ).not.toContain('DEMO_NOT_A_REAL_ONCE_TOKEN');
  expect(
    JSON.stringify(
      client
        .getMutationCache()
        .getAll()
        .map((m) => m.state.data)
    )
  ).not.toContain('DEMO_NOT_A_REAL_ONCE_TOKEN');
});

it('identifies same-name credentials by ID before revocation', async () => {
  vi.mocked(axios.get).mockImplementation(async (url) => ({
    data: {
      items: String(url).endsWith('ingest-credentials')
        ? [
            {
              id: 'credential-old',
              name: '同名凭据',
              sourceIds: ['example-source'],
              revoked: false,
            },
            {
              id: 'credential-new',
              name: '同名凭据',
              sourceIds: ['example-source'],
              revoked: false,
            },
          ]
        : [],
      page: 1,
      pageSize: 100,
      totalCount: 2,
    },
  }));
  setup();
  await screen.findByText('credential-old');
  expect(screen.getByText('credential-new')).toBeVisible();
  await userEvent.click(screen.getAllByRole('button', { name: '撤销' })[1]);
  const dialog = screen.getByRole('dialog');
  expect(within(dialog).getByText('credential-new')).toBeVisible();
  expect(within(dialog).queryByText('credential-old')).not.toBeInTheDocument();
  expect(axios.request).not.toHaveBeenCalled();
});

it('does not send an unscoped credential when no source was selected', async () => {
  setup();
  await userEvent.click(
    await screen.findByRole('button', { name: '创建凭据' })
  );
  await userEvent.type(screen.getByLabelText(/凭据名称/), '无范围');
  await userEvent.click(screen.getByRole('button', { name: '保存' }));
  expect(await screen.findByText('请选择来源。')).toBeVisible();
  expect(axios.request).not.toHaveBeenCalled();
});
