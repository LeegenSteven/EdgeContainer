import { readFileSync } from 'node:fs';

import { render, screen } from '@testing-library/react';
import { Formik } from 'formik';

import { Raw, Records, ChoiceField } from './components';

it('preserves resource names and IDs while the actual Chinese overlay translates UI controls', () => {
  render(
    <>
      <button type="button">Home</button>
      <button type="button">
        <Raw>Home</Raw>
      </button>
      <h2>
        <Raw>Home</Raw>
      </h2>
      <Records
        name="resource-label-regression"
        title="登记列表"
        items={[{ id: 'Running' }]}
        columns={[{ accessorKey: 'id', header: '标识' }]}
        loading={false}
      />
      <Formik initialValues={{ selection: 'Home' }} onSubmit={() => {}}>
        <ChoiceField
          name="selection"
          label="登记设备"
          options={[{ value: 'Home', label: 'Home' }]}
        />
      </Formik>
    </>
  );
  const script = document.createElement('script');
  script.src = 'http://localhost/edgestack-ui-test.js';
  const previous = Object.getOwnPropertyDescriptor(document, 'currentScript');
  Object.defineProperty(document, 'currentScript', {
    configurable: true,
    value: script,
  });
  try {
    const source = readFileSync('../deploy/ui/src/edgestack-ui.js', 'utf8')
      .replace('__EDGESTACK_VERSION__', 'resource-name-regression')
      .replace(
        '__EDGESTACK_TRANSLATIONS__',
        JSON.stringify({ Home: '首页', Running: '运行中' })
      );
    window.eval(source);
    (
      window as unknown as Window & { EdgeStackUI: { refresh(): void } }
    ).EdgeStackUI.refresh();
    expect(
      screen.getByRole('button', { name: '首页', exact: true })
    ).toBeVisible();
    expect(
      screen.getByRole('button', { name: 'Home', exact: true })
    ).toBeVisible();
    expect(
      screen.getByRole('heading', { name: 'Home', exact: true })
    ).toBeVisible();
    expect(
      screen.getByRole('option', { name: 'Home', exact: true })
    ).toBeInTheDocument();
    expect(screen.getByText('Running', { exact: true })).toBeVisible();
  } finally {
    if (previous) Object.defineProperty(document, 'currentScript', previous);
    else
      delete (document as unknown as { currentScript?: HTMLScriptElement })
        .currentScript;
  }
});
