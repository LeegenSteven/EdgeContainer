import angular from 'angular';

import { r2a } from '@/react-tools/react2angular';
import { withCurrentUser } from '@/react-tools/withCurrentUser';
import { withReactQuery } from '@/react-tools/withReactQuery';
import { withUIRouter } from '@/react-tools/withUIRouter';
import { AdminView } from '@/react/portainer/edgestack-admin/AdminView';

export const edgestackAdminModule = angular
  .module('portainer.app.react.views.edgestackAdmin', [])
  .component(
    'edgestackAdminView',
    r2a(withUIRouter(withReactQuery(withCurrentUser(AdminView))), [])
  ).name;
