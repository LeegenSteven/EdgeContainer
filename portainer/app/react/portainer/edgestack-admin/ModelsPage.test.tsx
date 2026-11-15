import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';

import axios from '@/portainer/services/axios/axios';
import { withTestRouter } from '@/react/test-utils/withRouter';

import { ModelsPage } from './ModelsPage';

vi.mock('@/portainer/services/axios/axios', () => ({
  default: { get: vi.fn(), request: vi.fn() },
}));
const pkg = {
  id: 'example-package',
  name: '示例模型',
  metadataVersion: 2,
  version: '1',
  slot: 'asr-encoder',
  published: false,
  validationStatus: 'draft',
  image: 'example.invalid/model@sha256:' + 'a'.repeat(64),
  modelId: 'example-weights',
  modelHash: 'sha256:' + 'b'.repeat(64),
};
const task = {
  id: 'example-test-task',
  name: '独立测试实例',
  testTask: true,
  revision: 3,
};
function setup() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  const View = withTestRouter(() => (
    <QueryClientProvider client={client}>
      <ModelsPage id={pkg.id} isNew={false} />
    </QueryClientProvider>
  ));
  render(<View />);
}
beforeEach(() => {
  vi.mocked(axios.get).mockReset();
  vi.mocked(axios.request).mockReset();
  vi.mocked(axios.get).mockImplementation(async (url) => ({
    data: String(url).endsWith('models/example-package')
      ? pkg
      : {
          items: String(url).endsWith('/tasks')
            ? [
                task,
                {
                  id: 'production-task',
                  name: '业务生产实例',
                  testTask: false,
                },
              ]
            : String(url).endsWith('/models')
              ? [pkg]
              : [],
          page: 1,
          pageSize: 100,
          totalCount: 1,
        },
  }));
});

it('requires an independent test task and explicit preview confirmation', async () => {
  vi.mocked(axios.request).mockImplementation(async (config) => ({
    data: String(config.url).endsWith('/validation-preview')
      ? {
          id: 'preview',
          taskId: task.id,
          packageId: pkg.id,
          slot: 'asr-encoder',
          revision: 3,
          target: { stackId: 8, serviceId: 'voice' },
          effects: [
            'Wait for a trusted runtime receipt before marking the model loaded',
          ],
          expiresAt: '2026-11-15T12:00:00Z',
        }
      : { id: 'operation', status: 'awaiting_observation', taskId: task.id },
  }));
  setup();
  const select = await screen.findByRole('combobox', { name: '独立测试任务' });
  expect(
    screen.queryByRole('option', { name: '业务生产实例' })
  ).not.toBeInTheDocument();
  await userEvent.selectOptions(select, task.id);
  await userEvent.click(screen.getByRole('button', { name: '预览受控验证' }));
  expect(
    await screen.findByText('收到可信运行时回执后，才标记模型已加载')
  ).toBeVisible();
  expect(axios.request).toHaveBeenCalledTimes(1);
  await userEvent.click(screen.getByRole('button', { name: '确认执行' }));
  await waitFor(() =>
    expect(axios.request).toHaveBeenCalledWith(
      expect.objectContaining({
        method: 'POST',
        url: '/edgestack/admin/models/example-package/validations',
        data: { previewId: 'preview', confirm: true },
      })
    )
  );
});

it('keeps draft publication disabled until a trusted validation exists', async () => {
  setup();
  expect(
    await screen.findByRole('button', { name: '发布用户候选' })
  ).toBeDisabled();
  expect(axios.request).not.toHaveBeenCalled();
});
