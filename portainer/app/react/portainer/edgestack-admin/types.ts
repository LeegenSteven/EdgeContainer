export interface ACL {
  userIds: number[];
  teamIds: number[];
}
export interface Metadata {
  id: string;
  name: string;
  description?: string;
  metadataVersion: number;
  archived?: boolean;
}
export interface Device extends Metadata, ACL {
  endpointId: number;
  architecture?: string;
  online?: boolean;
  lastSeenAt?: string;
}
export interface Target {
  endpointId: number;
  stackId: number;
  stackName?: string;
  serviceId: string;
}
export interface Binding {
  slot: string;
  packageId: string;
  name?: string;
  version?: string;
  state?: string;
  observedAt?: string;
  observedModelId?: string;
  observedModelHash?: string;
}
export interface DataLocation {
  name: string;
  kind: string;
  containerPath?: string;
  path?: string;
  location?: string;
  volumeName?: string;
  expectedReadOnly?: boolean;
  verification?: string;
}
export interface Task extends Metadata, ACL {
  deviceId: string;
  kind: string;
  state: string;
  revision: number;
  target: Target;
  models: Binding[];
  dataLocations: DataLocation[];
  capabilities: string[];
  modelEnvKeys?: string[];
  runtimeState?: string;
  runtimeObservedAt?: string;
  testTask?: boolean;
  runtimeBackend?: string;
  templateId?: string;
  voiceOrigin?: string;
  voiceTokenFile?: string;
}
export interface Source extends Metadata {
  deviceId: string;
  taskId: string;
  type: string;
  enabled: boolean;
  status?: string;
  lastSeenAt?: string;
  lastEventAt?: string;
  checkpoint?: unknown;
  coverage?: unknown;
  errorCode?: string;
}
export interface Credential {
  id: string;
  name: string;
  deviceIds: string[];
  taskIds: string[];
  sourceIds: string[];
  capabilities: string[];
  revoked: boolean;
  createdAt?: string;
  lastUsedAt?: string;
  lastSuccessfulAt?: string;
  rotatedFromId?: string;
}
export interface ModelPackage extends Metadata {
  version: string;
  kind: string;
  slot: string;
  taskKinds: string[];
  architectures: string[];
  published: boolean;
  immutable: boolean;
  image: string;
  modelId: string;
  modelHash: string;
  environment: Record<string, string>;
  requiresRestart: boolean;
  validationStatus?: string;
  legacyScoped?: boolean;
  allowedTaskIds?: string[];
  runtimeBackend?: string;
  templateIds?: string[];
}
export interface Operation {
  id: string;
  taskId: string;
  deviceId: string;
  status: string;
  packageId: string;
  previousPackageId?: string;
  slot: string;
  revision: number;
  createdAt: string;
  updatedAt?: string;
  errorCode?: string;
  actorUserId?: number;
  target?: Target;
  purpose?: string;
}
export interface Audit {
  id: string;
  action: string;
  actorUserId?: number;
  resourceId?: string;
  resourceType?: string;
  deviceId?: string;
  taskId?: string;
  result?: string;
  occurredAt?: string;
  fields?: string[];
}
export interface Preview {
  id: string;
  taskId: string;
  packageId: string;
  previousPackageId?: string;
  slot: string;
  revision: number;
  target: Target;
  effects: string[];
  expiresAt: string;
  requiresRestart: boolean;
}
export interface ListResponse<T> {
  items: T[];
  nextCursor?: string;
  totalCount?: number;
  page?: number;
  pageSize?: number;
}
export type Collection =
  | 'devices'
  | 'tasks'
  | 'sources'
  | 'models'
  | 'ingest-credentials'
  | 'operations'
  | 'audit';
export type Section =
  | 'devices'
  | 'tasks'
  | 'access'
  | 'sources'
  | 'models'
  | 'operations'
  | 'cloud';
