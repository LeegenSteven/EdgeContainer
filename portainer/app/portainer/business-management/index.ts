import angular from 'angular';

import { AccessHeaders } from '../authorization-guard';

export const businessRoutesModule = angular
  .module('portainer.businessmanagement', [])
  .config([
    '$stateRegistryProvider',
    ($stateRegistryProvider: { register(state: object): void }) => {
      $stateRegistryProvider.register({
        name: 'portainer.business',
        url: '/business',
        abstract: true,
        data: { access: AccessHeaders.Admin },
      });
      [
        'devices',
        'tasks',
        'access',
        'sources',
        'models',
        'operations',
        'cloud',
      ].forEach((section) => {
        const view = { 'content@': { component: 'edgestackAdminView' } };
        $stateRegistryProvider.register({
          name: `portainer.business.${section}`,
          url: `/${section}`,
          views: view,
          data: { access: AccessHeaders.Admin, businessSection: section },
        });
        if (['devices', 'tasks', 'sources', 'models'].includes(section)) {
          $stateRegistryProvider.register({
            name: `portainer.business.${section}.new`,
            url: '/new',
            views: view,
            data: { access: AccessHeaders.Admin, businessSection: section },
          });
          $stateRegistryProvider.register({
            name: `portainer.business.${section}.item`,
            url: '/:id',
            views: view,
            data: { access: AccessHeaders.Admin, businessSection: section },
          });
        }
        if (section === 'operations') {
          $stateRegistryProvider.register({
            name: 'portainer.business.operations.item',
            url: '/:id',
            views: view,
            data: { access: AccessHeaders.Admin, businessSection: section },
          });
        }
      });
    },
  ]).name;
