import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';

import { withTestRouter } from '@/react/test-utils/withRouter';

import { CloudPage } from './CloudPage';
import { get, send } from './api';

vi.mock('./api', async (importOriginal) => ({
  ...(await importOriginal<typeof import('./api')>()),
  get: vi.fn(),
  send: vi.fn(),
}));

function setup() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  const View = withTestRouter(() => (
    <QueryClientProvider client={client}>
      <CloudPage />
    </QueryClientProvider>
  ));
  render(<View />);
  return client;
}
const settings = {
  metadataVersion: 3,
  provider: 'deepseek',
  model: 'example-summary',
  source: 'hostfile',
  hasKey: true,
};
beforeEach(() => {
  localStorage.clear();
  vi.mocked(get).mockReset();
  vi.mocked(send).mockReset();
  vi.mocked(get).mockResolvedValue(settings);
});

it('clears a candidate key after failed verification and never reflects it', async () => {
  vi.mocked(send).mockRejectedValue({
    response: {
      status: 502,
      data: { errorCode: 'provider_authentication_failed' },
    },
  });
  const client = setup();
  const key = await screen.findByLabelText('候选 API Key');
  await userEvent.type(key, 'NOT_A_REAL_CANDIDATE_KEY');
  await userEvent.click(screen.getByRole('button', { name: '仅验证连接' }));
  await waitFor(() => expect(key).toHaveValue(''));
  expect(await screen.findByRole('alert')).toHaveTextContent(
    '候选密钥验证失败'
  );
  expect(document.body).not.toHaveTextContent('NOT_A_REAL_CANDIDATE_KEY');
  expect(
    JSON.stringify(
      client
        .getQueryCache()
        .getAll()
        .map((q) => q.state.data)
    )
  ).not.toContain('NOT_A_REAL_CANDIDATE_KEY');
  expect(localStorage.length).toBe(0);
});

it('saves with the expected version and clears the key after success', async () => {
  vi.mocked(send).mockResolvedValue({
    ...settings,
    metadataVersion: 4,
    source: 'managed',
  });
  setup();
  const key = await screen.findByLabelText('候选 API Key');
  await userEvent.type(key, 'NOT_A_REAL_NEW_KEY');
  await userEvent.click(screen.getByRole('button', { name: '验证并保存' }));
  await waitFor(() =>
    expect(send).toHaveBeenCalledWith('PATCH', 'settings/cloud', {
      metadataVersion: 3,
      provider: 'deepseek',
      model: 'example-summary',
      apiKey: 'NOT_A_REAL_NEW_KEY',
    })
  );
  expect(key).toHaveValue('');
  expect(await screen.findByRole('status')).toHaveTextContent('验证并保存');
});

it('shows CAS conflict while retaining current source and empty key', async () => {
  vi.mocked(send).mockRejectedValue({
    response: { status: 409, data: { errorCode: 'cloud_settings_conflict' } },
  });
  setup();
  await screen.findByLabelText('候选 API Key');
  await userEvent.click(screen.getByRole('button', { name: '验证并保存' }));
  expect(await screen.findByRole('alert')).toHaveTextContent('刷新当前配置');
  expect(screen.getByLabelText('候选 API Key')).toHaveValue('');
  expect(screen.getByText(/配置来源：主机密钥文件/)).toBeVisible();
});

it('requires confirmation before restoring the host configuration', async () => {
  vi.mocked(send).mockResolvedValue(settings);
  setup();
  await screen.findByLabelText('候选 API Key');
  await userEvent.click(
    screen.getByRole('button', { name: '恢复主机文件配置' })
  );
  expect(send).not.toHaveBeenCalled();
  await userEvent.click(screen.getByRole('button', { name: '确认执行' }));
  await waitFor(() =>
    expect(send).toHaveBeenCalledWith(
      'POST',
      'settings/cloud/restore-hostfile',
      { metadataVersion: 3 }
    )
  );
});
