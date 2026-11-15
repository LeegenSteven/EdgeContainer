import { useCurrentStateAndParams } from '@uirouter/react';

import { useCurrentUser } from '@/react/hooks/useUser';

import { PageHeader } from '@@/PageHeader';
import { Widget } from '@@/Widget';

import { businessSections } from './BusinessSidebar';
import { Section } from './types';
import { EntityPage } from './EntityPages';
import { AccessPage } from './AccessPage';
import { CredentialsPanel } from './CredentialsPanel';
import { ModelsPage } from './ModelsPage';
import { OperationsPage } from './OperationsPage';
import { CloudPage } from './CloudPage';

export function AdminView() {
  const { isPureAdmin } = useCurrentUser();
  const { state, params } = useCurrentStateAndParams();
  if (!isPureAdmin)
    return (
      <Widget>
        <Widget.Body>需要系统管理员权限。</Widget.Body>
      </Widget>
    );
  const section = (state.data?.businessSection || 'devices') as Section;
  const title =
    businessSections.find(([name]) => name === section)?.[1] || '业务管理';
  const id = typeof params.id === 'string' ? params.id : undefined;
  const isNew = state.name?.endsWith('.new') || false;
  return (
    <div data-cy="edgestack-admin-view">
      <PageHeader title={title} breadcrumbs={['业务管理', title]} reload />
      {(section === 'devices' ||
        section === 'tasks' ||
        section === 'sources') && (
        <EntityPage kind={section} id={id} isNew={isNew} />
      )}
      {section === 'sources' && !id && !isNew && <CredentialsPanel />}
      {section === 'access' && <AccessPage />}
      {section === 'models' && <ModelsPage id={id} isNew={isNew} />}
      {section === 'operations' && <OperationsPage id={id} />}
      {section === 'cloud' && <CloudPage />}
    </div>
  );
}
