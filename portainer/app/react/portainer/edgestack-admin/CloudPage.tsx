import { useState } from 'react';
import { Form, Formik } from 'formik';
import { useQuery, useQueryClient } from '@tanstack/react-query';

import { Widget } from '@@/Widget';
import { Button } from '@@/buttons';
import { LoadingButton } from '@@/buttons/LoadingButton';
import { FormControl } from '@@/form-components/FormControl';
import { Input } from '@@/form-components/Input';

import { get, send, adminKey } from './api';
import {
  Confirm,
  ErrorNotice,
  FormGuard,
  Notice,
  timestamp,
} from './components';

interface CloudSettings {
  metadataVersion: number;
  provider: 'deepseek';
  model: string;
  source: 'hostfile' | 'managed';
  hasKey: boolean;
  updatedAt?: string;
  lastTestAt?: string;
  lastTestStatus?: string;
  availableModels?: string[];
}
interface CloudTest {
  ok: boolean;
  metadataVersion: number;
  provider: string;
  model: string;
  availableModels: string[];
  checkedAt: string;
}

export function CloudPage() {
  const query = useQuery({
    queryKey: [...adminKey, 'cloud'],
    queryFn: () => get<CloudSettings>('settings/cloud'),
    refetchOnWindowFocus: false,
  });
  const client = useQueryClient();
  const [mode, setMode] = useState<'test' | 'save'>('test');
  const [test, setTest] = useState<CloudTest>();
  const [result, setResult] = useState('');
  const [error, setError] = useState<unknown>();
  const [restore, setRestore] = useState(false);
  const [restoring, setRestoring] = useState(false);
  async function restoreHost() {
    if (!query.data) return;
    setRestoring(true);
    setError(undefined);
    try {
      await send('POST', 'settings/cloud/restore-hostfile', {
        metadataVersion: query.data.metadataVersion,
      });
      setRestore(false);
      setResult('已恢复主机文件配置，后续总结使用此来源。');
      await client.invalidateQueries({ queryKey: [...adminKey, 'cloud'] });
    } catch (e) {
      setError(e);
    } finally {
      setRestoring(false);
    }
  }
  return (
    <>
      <ErrorNotice error={query.error} />
      {query.data && (
        <Widget>
          <Widget.Title title="DeepSeek 总结配置" />
          <Widget.Body>
            <p>
              配置来源：
              {query.data.source === 'managed'
                ? '网页托管配置'
                : '主机密钥文件'}
              ；密钥：{query.data.hasKey ? '已配置' : '尚未配置'}
            </p>
            <p>
              当前模型：<span data-edgestack-raw>{query.data.model}</span>
              ；资料版本 {query.data.metadataVersion}
            </p>
            <p>
              最近更新 {timestamp(query.data.updatedAt)}；当前配置保存时的验证{' '}
              {timestamp(query.data.lastTestAt)}
            </p>
            <Notice>
              连接检查仅请求 DeepSeek
              官方模型目录，不发送业务记录，不代表推理额度或总结效果已通过验收。此处只返回配置状态，永不回显密钥或密钥片段。
            </Notice>
            <Formik
              key={query.data.metadataVersion}
              initialValues={{ model: query.data.model, apiKey: '' }}
              onSubmit={async (values, helpers) => {
                setError(undefined);
                setResult('');
                setTest(undefined);
                const request = {
                  metadataVersion: query.data?.metadataVersion,
                  provider: 'deepseek',
                  model: values.model,
                  ...(values.apiKey.trim()
                    ? { apiKey: values.apiKey.trim() }
                    : {}),
                };
                helpers.setFieldValue('apiKey', '', false);
                try {
                  if (mode === 'test') {
                    const checked = await send<CloudTest>(
                      'POST',
                      'settings/cloud/test',
                      request
                    );
                    setTest(checked);
                    setResult(
                      '候选配置仅测试，尚未保存；密钥已清空。如需换 Key，请重新输入并点击“验证并保存”。'
                    );
                  } else {
                    await send<CloudSettings>(
                      'PATCH',
                      'settings/cloud',
                      request
                    );
                    helpers.resetForm({
                      values: { model: values.model, apiKey: '' },
                    });
                    setResult(
                      '配置已验证并保存，后续总结使用新配置，无需重启。'
                    );
                    await client.invalidateQueries({
                      queryKey: [...adminKey, 'cloud'],
                    });
                  }
                } catch (e) {
                  setError(e);
                } finally {
                  helpers.setSubmitting(false);
                }
              }}
            >
              {({ values, handleChange, isSubmitting }) => (
                <Form className="form-horizontal" autoComplete="off">
                  <FormGuard />
                  <FormControl label="总结模型" inputId="cloud-model">
                    <Input
                      id="cloud-model"
                      name="model"
                      list="cloud-model-options"
                      value={values.model}
                      onChange={handleChange}
                      required
                      data-cy="cloud-model"
                    />
                    <datalist id="cloud-model-options" data-edgestack-raw>
                      {(
                        test?.availableModels ||
                        query.data?.availableModels ||
                        []
                      ).map((m) => (
                        <option key={m} value={m} />
                      ))}
                    </datalist>
                  </FormControl>
                  <FormControl label="候选 API Key" inputId="cloud-api-key">
                    <Input
                      id="cloud-api-key"
                      name="apiKey"
                      type="password"
                      value={values.apiKey}
                      onChange={handleChange}
                      autoComplete="new-password"
                      spellCheck={false}
                      data-cy="cloud-api-key"
                    />
                    <span className="help-block small">
                      留空保留当前密钥。候选 Key
                      只用于本次验证；每次请求后清空，不写入浏览器存储。
                    </span>
                  </FormControl>
                  <ErrorNotice error={error} />
                  {result && (
                    <p role="status" data-cy="cloud-result">
                      {result}
                    </p>
                  )}
                  <div className="flex gap-2">
                    <LoadingButton
                      isLoading={isSubmitting && mode === 'test'}
                      loadingText="正在验证…"
                      disabled={isSubmitting || !values.model.trim()}
                      onClick={() => setMode('test')}
                      data-cy="cloud-test"
                    >
                      仅验证连接
                    </LoadingButton>
                    <LoadingButton
                      isLoading={isSubmitting && mode === 'save'}
                      loadingText="验证并保存中…"
                      disabled={isSubmitting || !values.model.trim()}
                      onClick={() => setMode('save')}
                      data-cy="cloud-save"
                    >
                      验证并保存
                    </LoadingButton>
                    <Button
                      color="default"
                      disabled={isSubmitting}
                      onClick={() => query.refetch()}
                      data-cy="cloud-reload"
                    >
                      刷新当前配置
                    </Button>
                  </div>
                </Form>
              )}
            </Formik>
            <div className="mt-4">
              <Button
                color="default"
                onClick={() => {
                  setError(undefined);
                  setRestore(true);
                }}
                data-cy="cloud-restore"
              >
                恢复主机文件配置
              </Button>
            </div>
            <Notice>
              正在生成的总结继续使用提交时冻结的配置；换 Key
              不会自动重复调用。托管密钥缺失时须重新配置，不会静默回退来源。
            </Notice>
          </Widget.Body>
        </Widget>
      )}
      {restore && (
        <Confirm
          title="恢复主机文件配置"
          onClose={() => setRestore(false)}
          onConfirm={restoreHost}
          busy={restoring}
          error={error}
        >
          <Notice>
            将验证 Server
            启动时配置的模型和密钥文件，成功后切换来源。原主机文件不被网页覆盖。
          </Notice>
        </Confirm>
      )}
    </>
  );
}
