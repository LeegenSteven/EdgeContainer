import { ReactNode, PropsWithChildren } from 'react';
import { CellContext, ColumnDef } from '@tanstack/react-table';
import {
  Form,
  Formik,
  FormikHelpers,
  FormikValues,
  useField,
  useFormikContext,
} from 'formik';
import { Boxes } from 'lucide-react';

import { Widget } from '@@/Widget';
import { Button } from '@@/buttons';
import { LoadingButton } from '@@/buttons/LoadingButton';
import { FormControl } from '@@/form-components/FormControl';
import { Input } from '@@/form-components/Input';
import { Select, Option } from '@@/form-components/Input/Select';
import { usePreventFormExit } from '@@/form-components/usePreventFormExit';
import { Datatable } from '@@/datatables';
import { useTableStateWithoutStorage } from '@@/datatables/useTableState';
import { Modal } from '@@/modals/Modal';

import { errorMessage } from './api';

export function ErrorNotice({ error }: { error: unknown }) {
  return error ? (
    <div role="alert" className="alert alert-danger">
      {errorMessage(error)}
    </div>
  ) : null;
}

export function Notice({ children }: PropsWithChildren<unknown>) {
  return <p className="small text-muted mb-4">{children}</p>;
}

/** Resource metadata must not be reinterpreted as a translated UI label. */
export function Raw({ children }: PropsWithChildren<unknown>) {
  return <span data-edgestack-raw>{children}</span>;
}

export function TextField({
  name,
  label,
  multiline,
  type = 'text',
  disabled,
  hint,
  required,
}: {
  name: string;
  label: string;
  multiline?: boolean;
  type?: string;
  disabled?: boolean;
  hint?: string;
  required?: boolean;
}) {
  const [field, meta] = useField<string>(name);
  return (
    <FormControl
      inputId={`business-${name}`}
      label={label}
      required={required}
      errors={meta.touched && meta.error}
    >
      {multiline ? (
        <textarea
          name={field.name}
          onChange={field.onChange}
          onBlur={field.onBlur}
          value={field.value || ''}
          id={`business-${name}`}
          className="form-control"
          rows={3}
          disabled={disabled}
        />
      ) : (
        <Input
          name={field.name}
          onChange={field.onChange}
          onBlur={field.onBlur}
          value={field.value ?? ''}
          id={`business-${name}`}
          type={type}
          disabled={disabled}
          required={required}
          data-cy={`business-${name}`}
        />
      )}
      {hint && <span className="help-block small">{hint}</span>}
    </FormControl>
  );
}

export function ChoiceField({
  name,
  label,
  options,
  disabled,
  hint,
}: {
  name: string;
  label: string;
  options: Option<string>[];
  disabled?: boolean;
  hint?: string;
}) {
  const [field, meta] = useField<string>(name);
  return (
    <FormControl
      inputId={`business-${name}`}
      label={label}
      errors={meta.touched && meta.error}
    >
      <Select
        data-edgestack-raw
        name={field.name}
        value={field.value ?? ''}
        onChange={field.onChange}
        onBlur={field.onBlur}
        id={`business-${name}`}
        options={options}
        disabled={disabled}
        data-cy={`business-${name}`}
      />
      {hint && <span className="help-block small">{hint}</span>}
    </FormControl>
  );
}

export function CheckField({ name, label }: { name: string; label: string }) {
  const [field] = useField<boolean>({ name, type: 'checkbox' });
  return (
    <FormControl label="">
      <label className="control-label">
        <input
          name={field.name}
          onChange={field.onChange}
          onBlur={field.onBlur}
          type="checkbox"
          checked={!!field.value}
          data-cy={`business-${name}`}
        />{' '}
        {label}
      </label>
    </FormControl>
  );
}

export function FormGuard() {
  const { dirty } = useFormikContext();
  usePreventFormExit(() => dirty);
  return null;
}

export function Editor<T extends FormikValues>({
  title,
  initialValues,
  validate,
  onSubmit,
  onCancel,
  children,
}: PropsWithChildren<{
  title: string;
  initialValues: T;
  validate?(value: T): object;
  onSubmit(value: T): Promise<unknown>;
  onCancel(): void;
}>) {
  return (
    <Widget data-cy="business-editor">
      <Widget.Title title={title} />
      <Widget.Body>
        <Formik
          initialValues={initialValues}
          validate={validate}
          onSubmit={async (value, helpers: FormikHelpers<T>) => {
            helpers.setStatus(undefined);
            try {
              await onSubmit(value);
              helpers.resetForm({ values: value });
              onCancel();
            } catch (error) {
              helpers.setStatus(error);
            } finally {
              helpers.setSubmitting(false);
            }
          }}
        >
          {({ isSubmitting, status }) => (
            <Form className="form-horizontal">
              <FormGuard />
              <ErrorNotice error={status} />
              {children}
              <div className="mt-4 flex gap-2">
                <LoadingButton
                  isLoading={isSubmitting}
                  loadingText="正在保存…"
                  data-cy="business-save"
                >
                  保存
                </LoadingButton>
                <Button
                  color="default"
                  onClick={onCancel}
                  disabled={isSubmitting}
                  data-cy="business-cancel"
                >
                  取消
                </Button>
              </div>
            </Form>
          )}
        </Formik>
      </Widget.Body>
    </Widget>
  );
}

export function Records<T extends object>({
  name,
  title,
  items,
  columns,
  loading,
  actions,
}: {
  name: string;
  title: string;
  items: T[];
  columns: ColumnDef<T>[];
  loading: boolean;
  actions?: ReactNode;
}) {
  // No records, credentials or query text are persisted in browser storage.
  const settings = useTableStateWithoutStorage();
  return (
    <Datatable
      dataset={items}
      columns={columns.map((column) => ({
        ...column,
        cell:
          column.cell ||
          ((context: CellContext<T, unknown>) => (
            <Raw>{String(context.getValue() ?? '')}</Raw>
          )),
      }))}
      settingsManager={settings}
      title={title}
      titleIcon={Boxes}
      disableSelect
      isLoading={loading}
      emptyContentLabel="尚无登记记录"
      renderTableActions={() => actions}
      data-cy={`business-table-${name}`}
    />
  );
}

export function Confirm({
  title,
  children,
  onClose,
  onConfirm,
  busy,
  error,
  label = '确认执行',
}: PropsWithChildren<{
  title: string;
  onClose(): void;
  onConfirm(): void;
  busy?: boolean;
  error?: unknown;
  label?: string;
}>) {
  return (
    <Modal aria-label={title} onDismiss={busy ? undefined : onClose}>
      <h3 className="mt-0">{title}</h3>
      {children}
      <ErrorNotice error={error} />
      <div className="mt-4 flex gap-2">
        <LoadingButton
          type="button"
          isLoading={!!busy}
          loadingText="正在执行…"
          onClick={onConfirm}
          data-cy="business-confirm"
        >
          {label}
        </LoadingButton>
        <Button
          color="default"
          disabled={busy}
          onClick={onClose}
          data-cy="business-confirm-cancel"
        >
          取消
        </Button>
      </div>
    </Modal>
  );
}

export function timestamp(value?: string) {
  return value && !value.startsWith('0001-')
    ? new Date(value).toLocaleString()
    : '暂无记录';
}
export function statusLabel(value?: string) {
  const labels: Record<string, string> = {
    passed: '已通过可信验证',
    validating: '正在受控验证',
    draft: '待验证草稿',
    legacy_scoped: '保留历史任务范围',
    registered: '已登记',
    running: '任务实例运行中',
    unknown: '运行状态待确认',
    loaded: '已收到加载确认',
    unobserved: '待运行时确认',
    unconfigured: '尚未接入',
    healthy: '来源已联系',
    connected: '来源已连接',
    streaming: '正在接收',
    disconnected: '来源连接中断',
    disabled: '已停用',
    archived: '已归档',
    succeeded: '已完成',
    failed: '失败',
    dispatching: '正在下发',
    awaiting_observation: '等待运行时确认',
    reconciliation_required: '需核对',
    success: '成功',
    completed: '已完成',
  };
  return value ? labels[value] || '待确认' : '待确认';
}

export const taskKinds = [
  { value: 'voice', label: '语音交互' },
  { value: 'voice-test', label: '语音接入测试' },
  { value: 'camera', label: '视觉观察' },
  { value: 'business', label: '其他业务任务' },
];
export const modelSlots = [
  { value: 'asr-encoder', label: '语音识别' },
  { value: 'llm', label: '对话' },
  { value: 'tts', label: '语音合成' },
  { value: 'vision', label: '视觉理解' },
];
export function slotLabel(slot: string) {
  return (
    modelSlots.find((item) => item.value === slot)?.label ||
    (slot === 'asr' ? '语音识别' : '用途待登记')
  );
}
