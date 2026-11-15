import { Boxes } from 'lucide-react';

import { SidebarItem } from '@/react/sidebar/SidebarItem';
import { SidebarParent } from '@/react/sidebar/SidebarItem/SidebarParent';
import { SidebarSection } from '@/react/sidebar/SidebarSection';

export const businessSections = [
  ['devices', '设备业务登记'],
  ['tasks', '业务任务'],
  ['access', '业务授权'],
  ['sources', '来源与凭据'],
  ['models', '模型目录'],
  ['operations', '操作与审计'],
  ['cloud', '云端总结设置'],
] as const;

export function BusinessSidebar() {
  return (
    <SidebarSection title="业务管理">
      <SidebarParent
        label="业务管理"
        icon={Boxes}
        to="portainer.business.devices"
        pathOptions={{ includePaths: ['portainer.business'] }}
        listId="edgestack-business"
        data-cy="edgestack-admin-menu"
      >
        {businessSections.map(([id, label]) => (
          <SidebarItem
            key={id}
            to={`portainer.business.${id}`}
            label={label}
            isSubMenu
            data-cy={`edgestack-admin-${id}`}
          />
        ))}
      </SidebarParent>
    </SidebarSection>
  );
}
