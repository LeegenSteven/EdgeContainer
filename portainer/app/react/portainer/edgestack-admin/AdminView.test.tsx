import { render, screen } from '@testing-library/react';

import { UserContext } from '@/react/hooks/useUser';
import { User } from '@/portainer/users/types';
import { withTestRouter } from '@/react/test-utils/withRouter';

import { AdminView } from './AdminView';

it('refuses to mount business management for a regular user', () => {
  const View = withTestRouter(() => (
    <UserContext.Provider value={{ user: { Id: 2, Role: 2 } as User }}>
      <AdminView />
    </UserContext.Provider>
  ));
  render(<View />);
  expect(screen.getByText('需要系统管理员权限。')).toBeVisible();
  expect(screen.queryByTestId('edgestack-admin-view')).not.toBeInTheDocument();
});
