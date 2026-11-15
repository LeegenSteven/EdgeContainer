import {
  useInfiniteQuery,
  useMutation,
  useQuery,
  useQueryClient,
} from '@tanstack/react-query';
import { useEffect } from 'react';

import axios from '@/portainer/services/axios/axios';

import { Collection, ListResponse } from './types';

export const adminKey = ['edgestack', 'admin'];
export const adminUrl = '/edgestack/admin';

export async function get<T>(path: string, params?: object): Promise<T> {
  const response = await axios.get<T>(`${adminUrl}/${path}`, { params });
  return response.data;
}

export async function send<T>(
  method: 'POST' | 'PATCH' | 'DELETE',
  path: string,
  data?: unknown
): Promise<T> {
  const response = await axios.request<T>({
    method,
    url: `${adminUrl}/${path}`,
    data,
  });
  return response.data;
}

export function useAdminList<T>(
  collection: Collection,
  params: Record<string, string> = {}
) {
  const query = useInfiniteQuery({
    queryKey: [...adminKey, collection, params],
    queryFn: ({ pageParam = 1 }) =>
      get<ListResponse<T>>(collection, {
        ...params,
        pageSize: 100,
        page: pageParam,
      }),
    getNextPageParam: (page) =>
      (page.page || 1) * (page.pageSize || 100) < (page.totalCount || 0)
        ? (page.page || 1) + 1
        : undefined,
    refetchOnWindowFocus: false,
  });
  return {
    ...query,
    items: query.data?.pages.flatMap((page) => page.items) || [],
  };
}

export function useAdminItem<T>(collection: string, id?: string) {
  return useQuery({
    queryKey: [...adminKey, collection, id],
    queryFn: () => get<T>(`${collection}/${encodeURIComponent(id || '')}`),
    enabled: !!id,
    refetchOnWindowFocus: false,
  });
}

/** Selectors must include records after the first server page. */
export function useAdminChoices<T>(collection: Collection) {
  const query = useAdminList<T>(collection);
  const { hasNextPage, isFetchingNextPage, isError, fetchNextPage } = query;
  useEffect(() => {
    if (hasNextPage && !isFetchingNextPage && !isError) fetchNextPage();
  }, [hasNextPage, isFetchingNextPage, isError, fetchNextPage]);
  return query;
}

export function useAdminMutation<T, V = void>(fn: (value: V) => Promise<T>) {
  const client = useQueryClient();
  return useMutation({
    mutationFn: fn,
    onSuccess: () => client.invalidateQueries({ queryKey: adminKey }),
  });
}

const errors: Record<string, string> = {
  cloud_settings_conflict:
    '云端配置已被其他管理员更新，本次未保存。请刷新当前配置后重新验证。',
  provider_authentication_failed:
    '候选密钥验证失败，当前配置未更改。请检查后重新输入。',
  provider_model_unavailable:
    '所选模型不在官方模型目录中，当前配置未更改。请选择可用模型。',
  provider_not_configured:
    '当前密钥来源缺失或无效，请重新配置；不会自动切换到其他来源。',
  cloud_secret_write_failed: '密钥文件保存失败，原配置已保留。',
  provider_timeout: '连接验证超时，当前配置未更改。请稍后重试。',
  provider_rate_limited: '云端服务暂时限流，本次未保存配置。请稍后重试。',
  provider_unavailable: '暂时无法连接云端服务，当前配置未更改。请稍后重试。',
  invalid_cloud_settings:
    '云端配置输入无效，当前配置未更改。请检查模型与候选密钥。',
  model_runtime_unverified:
    '已收到模型回执，尚待核实测试实例使用登记的固定镜像。请稍后刷新；核验前不会通过验证或发布。',
  model_change_in_progress:
    '此任务仍有模型操作进行中，请等待结果后再修改运行配置。',
  invalid_immutable_package:
    '模型包资料不完整，请检查模型身份、运行后端与兼容模板。',
  invalid_image_digest: '请登记固定的容器镜像 SHA-256 digest。',
  invalid_model_parameter: '模型参数未通过检查，请核对允许的变量名与固定值。',
  invalid_source: '来源登记无效，请核对来源类型与设备、任务绑定。',
  invalid_task: '任务登记无效，请核对已有设备、独立 Stack 与服务。',
  invalid_data_location: '数据位置登记无效，请核对用途与容器内路径。',
  derived_location_field: '运行时核验字段只读，不能作为管理员登记资料保存。',
  validation_preview_required: '请先生成当前模型包的受控验证预览，再显式确认。',
  metadata_version_conflict:
    '资料已被其他管理员修改。请刷新后检查差异，再重新保存。',
  stale_metadata: '资料版本已变化，请刷新后重新编辑。',
  version_conflict: '资料版本已变化，请刷新后重新编辑。',
  conflict: '当前资料或部署版本已变化，请刷新后重新检查。',
  package_immutable: '模型包内容不可变，请使用新的包标识登记新版本。',
  forbidden: '当前账户没有此项管理权限。',
  target_immutable: '已登记任务的部署目标不可更换，请登记新任务。',
};

export function errorMessage(error: unknown): string {
  const value = error as {
    response?: {
      status?: number;
      data?: {
        error?: string;
        errorCode?: string;
        message?: string;
        code?: string;
      };
    };
    message?: string;
  };
  const code =
    value?.response?.data?.errorCode ||
    value?.response?.data?.error ||
    value?.response?.data?.code ||
    value?.response?.data?.message;
  if (code && errors[code]) return errors[code];
  if (value?.response?.status === 409) return errors.conflict;
  if (value?.response?.status === 403) return errors.forbidden;
  return code && /^[\w.-]+$/.test(code)
    ? `请求未完成（${code}）`
    : '请求未完成，请检查输入或稍后重试。';
}
