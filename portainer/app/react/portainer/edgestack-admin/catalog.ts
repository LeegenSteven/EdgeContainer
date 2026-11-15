import { useQuery } from '@tanstack/react-query';
import { parse } from 'yaml';

import axios from '@/portainer/services/axios/axios';
import { User } from '@/portainer/users/types';
import { Team } from '@/react/portainer/users/teams/types';

export interface Environment {
  Id: number;
  Name: string;
  Type: number;
}
export interface EdgeStack {
  Id: number;
  Name: string;
}

export function useCatalog() {
  return useQuery({
    queryKey: ['edgestack', 'admin', 'native-catalog'],
    queryFn: async () => {
      const [users, teams, environments, stacks] = await Promise.all([
        axios.get<User[]>('/users'),
        axios.get<Team[]>('/teams'),
        axios.get<Environment[]>('/endpoints', { params: { limit: 0 } }),
        axios.get<EdgeStack[]>('/edge_stacks'),
      ]);
      return {
        users: users.data,
        teams: teams.data,
        environments: environments.data,
        stacks: stacks.data,
      };
    },
    refetchOnWindowFocus: false,
  });
}

export function useStackServices(id: number) {
  return useQuery({
    queryKey: ['edgestack', 'admin', 'stack-services', id],
    enabled: id > 0,
    queryFn: async () => {
      const { data } = await axios.get<{ StackFileContent: string }>(
        `/edge_stacks/${id}/file`
      );
      const document = parse(data.StackFileContent) as {
        services?: Record<string, unknown>;
      } | null;
      return Object.keys(document?.services || {});
    },
    refetchOnWindowFocus: false,
  });
}
