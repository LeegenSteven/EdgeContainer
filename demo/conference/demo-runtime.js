/* EdgeStack meeting simulator. All data is generated locally; no live API.
 * Used with the unchanged upstream Portainer CE front-end bundles.
 */
(function () {
  'use strict';
  const KEY = 'edgestack.conference.v1';
  const clone = (x) => JSON.parse(JSON.stringify(x));
  const now = () => Math.floor(Date.now() / 1000);
  const iso = () => new Date().toISOString();
  const hex = (n) => n.toString(16).padStart(12, '0').repeat(6).slice(0, 64);
  const assets = window.__EDGESTACK_DEMO_ASSETS__;
  const requests = [];
  const events = [];
  const formObject = (b) => {
    if (b instanceof FormData) return Object.fromEntries(b.entries());
    if (typeof b === 'string') { try { return JSON.parse(b); } catch { return Object.fromEntries(new URLSearchParams(b)); } }
    return b || {};
  };
  const rc = (id, type = 1) => ({ Id: [...String(id)].reduce((n, c) => (n * 31 + c.charCodeAt(0)) % 1000000, type) + 1, ResourceId: id, SubResourceIds: [], Type: type, UserAccesses: [], TeamAccesses: [], Public: false, AdministratorsOnly: true, System: false });

  function makeContainer(name, image, running = true, port = 0, index = 1) {
    const id = hex(5000 + index);
    const network = name.includes('agent') ? 'edgestack-agent-mgmt' : name.includes('voice') ? 'demo-voice-rk3588_default' : 'edge_acceptance_default';
    const ports = port ? { '8080/tcp': [{ HostIp: '0.0.0.0', HostPort: String(port) }] } : {};
    const project = name.includes('whoami') ? 'edge_acceptance' : name.includes('agent') ? 'edgestack-agent' : name.includes('voice') ? 'demo-voice-rk3588' : '';
    const config = {
      Hostname: id.slice(0, 12), Domainname: '', User: '', AttachStdin: false,
      AttachStdout: true, AttachStderr: true, ExposedPorts: port ? { '8080/tcp': {} } : {},
      Tty: false, OpenStdin: false, StdinOnce: false,
      Env: ['TZ=Asia/Shanghai', 'DEMO_MODE=true'], Cmd: name.includes('whoami') ? ['--port=8080', '--verbose'] : [],
      Healthcheck: { Test: ['CMD', 'true'], Interval: 30000000000, Timeout: 1000000000, Retries: 3 },
      Image: image, Volumes: {}, WorkingDir: '/', Entrypoint: [], OnBuild: null,
      Labels: project ? { 'com.docker.compose.project': project, 'com.docker.compose.service': name.includes('whoami') ? 'whoami' : 'app', 'com.docker.compose.version': '5.7.0' } : {},
      StopSignal: 'SIGTERM',
    };
    const mount = name.includes('voice') ? [{ Type: 'volume', Name: 'demo-voice-rk3588_speech-data', Source: '/var/lib/docker/volumes/demo-voice-rk3588_speech-data/_data', Destination: '/app/data', Driver: 'local', Mode: 'rw', RW: true, Propagation: '' }] : [];
    return {
      Id: id, Name: '/' + name, Names: ['/' + name], Created: new Date(Date.now() - 86400000).toISOString(),
      Image: hex(7000 + index), ImageID: hex(7000 + index), ImageName: image,
      State: { Status: running ? 'running' : 'exited', Running: running, Paused: false, Restarting: false, OOMKilled: false, Dead: false, Pid: running ? 1234 + index : 0, ExitCode: 0, Error: '', StartedAt: new Date(Date.now() - 86400000).toISOString(), FinishedAt: running ? '0001-01-01T00:00:00Z' : iso(), Health: { Status: running ? 'healthy' : 'none', FailingStreak: 0, Log: [] } },
      Path: '/app', Args: [], RestartCount: 0, Driver: 'overlayfs', Platform: 'linux', AppArmorProfile: '',
      Config: config,
      HostConfig: { Binds: [], ContainerIDFile: '', LogConfig: { Type: 'json-file', Config: {} }, NetworkMode: network, PortBindings: ports, RestartPolicy: { Name: 'unless-stopped', MaximumRetryCount: 0 }, AutoRemove: false, VolumeDriver: '', VolumesFrom: null, ConsoleSize: [0, 0], CapAdd: [], CapDrop: [], CgroupnsMode: 'private', Dns: [], DnsOptions: [], DnsSearch: [], ExtraHosts: [], GroupAdd: [], IpcMode: 'private', Links: [], OomScoreAdj: 0, PidMode: '', Privileged: false, PublishAllPorts: false, ReadonlyRootfs: false, SecurityOpt: [], StorageOpt: {}, Tmpfs: {}, UTSMode: '', UsernsMode: '', ShmSize: 67108864, Runtime: 'runc', Isolation: '', CpuShares: 0, Memory: 0, NanoCpus: 0, CgroupParent: '', BlkioWeight: 0, CpuPeriod: 0, CpuQuota: 0, CpusetCpus: '', CpusetMems: '', Devices: [], DeviceRequests: [], MemoryReservation: 0, MemorySwap: 0, OomKillDisable: null, PidsLimit: null, Ulimits: [], Sysctls: {}, Mounts: [] },
      GraphDriver: { Name: 'overlayfs', Data: {} }, Mounts: mount,
      NetworkSettings: { Bridge: '', SandboxID: hex(9000 + index), HairpinMode: false, LinkLocalIPv6Address: '', LinkLocalIPv6PrefixLen: 0, Ports: ports, SandboxKey: '/var/run/docker/netns/demo', EndpointID: hex(index), Gateway: '172.21.0.1', GlobalIPv6Address: '', GlobalIPv6PrefixLen: 0, IPAddress: '172.21.0.' + (index + 1), IPPrefixLen: 16, IPv6Gateway: '', MacAddress: '02:42:ac:15:00:02', Networks: { [network]: { IPAMConfig: null, Links: null, Aliases: [name], MacAddress: '02:42:ac:15:00:02', DriverOpts: null, GwPriority: 0, NetworkID: hex(300), EndpointID: hex(600 + index), Gateway: '172.21.0.1', IPAddress: '172.21.0.' + (index + 1), IPPrefixLen: 16, IPv6Gateway: '', GlobalIPv6Address: '', GlobalIPv6PrefixLen: 0, DNSNames: [name, id.slice(0, 12)] } } },
      Portainer: { ResourceControl: rc(id) }, ResourceControl: rc(id),
    };
  }

  function seed() {
    const settings = {
      LogoURL: '', CustomLoginBanner: '', BlackListedLabels: [], AuthenticationMethod: 1,
      InternalAuthSettings: { RequiredPasswordLength: 12 }, RequiredPasswordLength: 12,
      LDAPSettings: { AnonymousMode: true, ReaderDN: '', URL: '', TLSConfig: { TLS: false, TLSSkipVerify: false }, StartTLS: false, SearchSettings: [{ BaseDN: '', Filter: '', UserNameAttribute: 'uid' }], GroupSearchSettings: [{ GroupBaseDN: '', GroupFilter: '', GroupAttribute: 'member' }], AutoCreateUsers: true },
      OAuthSettings: { ClientID: '', AccessTokenURI: '', AuthorizationURI: '', ResourceURI: '', RedirectURI: '', UserIdentifier: '', Scopes: '', OAuthAutoCreateUsers: true, DefaultTeamID: 0, SSO: true, LogoutURI: '', KubeSecretKey: '' },
      Features: {}, FeatureFlagSettings: {}, SnapshotInterval: '5m', TemplatesURL: 'https://templates.edgestack-demo.invalid/templates.json', EnableEdgeComputeFeatures: true,
      UserSessionTimeout: '8h', KubeconfigExpiry: '0', HelmRepositoryURL: 'https://charts.example.invalid', KubectlShellImage: 'portainer/kubectl-shell:2.45.3', TrustOnFirstConnect: false,
      EnforceEdgeID: false, AgentSecret: '', EdgePortainerUrl: 'https://edgestack-demo.invalid:9443', EdgeAgentCheckinInterval: 5, EdgeCommandInterval: 5, EdgePingInterval: 5, EdgeSnapshotInterval: 300,
      DisplayDonationHeader: false, DisplayExternalContributors: false, EnableHostManagementFeatures: false, ExperimentalFeatures: {},
      AllowVolumeBrowserForRegularUsers: true, AllowBindMountsForRegularUsers: true, AllowPrivilegedModeForRegularUsers: false, AllowHostNamespaceForRegularUsers: false, AllowStackManagementForRegularUsers: true, AllowDeviceMappingForRegularUsers: true, AllowContainerCapabilitiesForRegularUsers: false, ForceSecureCookies: false,
      GlobalDeploymentOptions: { hideAddWithForm: false, perEnvOverride: false, hideWebEditor: false, hideFileUpload: false, requireNoteOnApplications: false, minApplicationNoteLength: 0, hideStacksFunctionality: false },
      Edge: { AsyncMode: false, PingInterval: 5, SnapshotInterval: 300, CommandInterval: 5, CheckinInterval: 5, TunnelServerAddress: 'edgestack-demo.invalid:8000' }, TeamSync: false, DefaultRegistry: { Hide: false },
      OAuthLoginURI: '', OAuthLogoutURI: '', OAuthHideInternalAuth: false,
    };
    const environment = {
      Id: 4, Name: 'rk3588-demo-01', Type: 4, URL: 'tcp://192.0.2.20:9001', GroupId: 1, PublicURL: '', Status: 1, TagIds: [],
      ComposeSyntaxMaxVersion: '3',
      TLSConfig: { TLS: false, TLSSkipVerify: true }, Kubernetes: { Configuration: { StorageClasses: [], IngressClasses: [], UseLoadBalancer: false, UseServerMetrics: false, AllowNoneIngressClass: true, IngressAvailabilityPerNamespace: false }, Flags: { IsServerIngressClassDetected: false, IsServerMetricsDetected: false, IsServerStorageDetected: false } },
      EdgeID: 'DEMO-EDGE-RK3588-01', EdgeKey: btoa('https://edgestack-demo.invalid:9443|edgestack-demo.invalid:8000|DEMO_NOT_A_REAL_SECRET|4'),
      EdgeCheckinInterval: 5, LastCheckInDate: now(), Heartbeat: true, Snapshots: [], Agent: { Version: '2.45.3' }, ContainerEngine: 'docker', Edge: clone(settings.Edge),
      UserAccessPolicies: {}, TeamAccessPolicies: {}, Extensions: [], AuthorizedUsers: [], AuthorizedTeams: [], Gpus: [], EnableGPUManagement: false, QueryDate: now(),
      SecuritySettings: { allowBindMountsForRegularUsers: true, allowPrivilegedModeForRegularUsers: false, allowVolumeBrowserForRegularUsers: true, allowHostNamespaceForRegularUsers: false, allowDeviceMappingForRegularUsers: true, allowStackManagementForRegularUsers: true, allowContainerCapabilitiesForRegularUsers: false, allowSecurityOptForRegularUsers: false, allowSysctlSettingForRegularUsers: false, enableHostManagementFeatures: false },
      IsEdgeDevice: true, ChangeWindow: { Enabled: false, StartTime: '', EndTime: '' }, DeploymentOptions: clone(settings.GlobalDeploymentOptions),
    };
    const containers = [
      makeContainer('edge_acceptance-whoami-1', 'traefik/whoami:v1.10.3', true, 18080, 1),
      makeContainer('edgestack-edge-agent', 'portainer/agent:2.45.3', true, 0, 2),
      makeContainer('demo-voice-rk3588', 'example.invalid/demo-voice:latest', true, 8621, 3),
      ...['hello-world-test', 'kernel-bench-test', 'python-test', 'voice-profile-old', 'nginx-test', 'rknn-test', 'camera-test', 'alpine-test'].map((n, i) => makeContainer(n, i === 4 ? 'nginx:alpine' : 'alpine:3.20', false, 0, i + 4)),
    ];
    const images = [...new Set(containers.map((c) => c.Config.Image))].map((tag, i) => ({ Id: 'sha256:' + hex(7000 + i + 1), ParentId: '', RepoTags: [tag], RepoDigests: [tag.split(':')[0] + '@sha256:' + hex(7000 + i + 1)], Created: now() - 86400 * (i + 1), Size: i === 2 ? 1048000000 : 10000000 + i * 17000000, SharedSize: -1, VirtualSize: 10000000, Labels: {}, Containers: i < 3 ? 1 : 0, used: i < 3 }));
    const networks = ['bridge', 'host', 'none', 'edge_acceptance_default', 'edgestack-agent-mgmt', 'demo-voice-rk3588_default'].map((name, i) => ({ Id: hex(300 + i), Name: name, Created: iso(), Scope: 'local', Driver: name === 'host' ? 'host' : name === 'none' ? 'null' : 'bridge', EnableIPv4: true, EnableIPv6: false, IPAM: { Driver: 'default', Options: {}, Config: [{ Subnet: `172.${17 + i}.0.0/16`, Gateway: `172.${17 + i}.0.1` }] }, Internal: false, Attachable: false, Ingress: false, ConfigFrom: { Network: '' }, ConfigOnly: false, Containers: {}, Options: {}, Labels: name.includes('_') ? { 'com.docker.compose.project': name.split('_')[0] } : {}, Portainer: { ResourceControl: { ...rc(hex(300 + i), 5), System: i < 3, Public: i < 3 } } }));
    const volumes = ['edgestack_agent_data', 'demo-voice-rk3588_speech-data', 'demo-voice-profile_speech-data'].map((name) => ({ Name: name, Driver: 'local', Mountpoint: '/var/lib/docker/volumes/' + name + '/_data', CreatedAt: iso(), Status: {}, Labels: { 'com.docker.compose.project': name.split('_')[0] }, Scope: 'local', Options: {}, UsageData: { Size: 10485760, RefCount: 1 }, Portainer: { ResourceControl: rc(name, 2) } }));
    const compose = 'services:\n  whoami:\n    image: traefik/whoami:v1.10.3\n    command: ["--port=8080", "--verbose"]\n    ports:\n      - "18080:8080"\n    restart: unless-stopped\n';
    return { schema: 1, loggedIn: true, serverOnline: true, boardOnline: true, cpu: 18, memory: 31,
      settings, environments: [environment], containers, images, networks, volumes, compose,
      users: [{ Id: 1, Username: 'admin', Role: 1, EndpointAuthorizations: {}, PortainerAuthorizations: {}, UseCache: true, ThemeSettings: { color: 'dark' }, GitCredentials: [], OpenAMTConfiguration: {}, EdgeGroups: [], RoleName: 'Administrator' }],
      teams: [{ Id: 1, Name: '研发团队' }], memberships: [{ Id: 1, UserID: 1, TeamID: 1, Role: 1 }], tags: [],
      endpoint_groups: [{ Id: 1, Name: 'Unassigned', Description: '默认环境分组', TagIds: [], UserAccessPolicies: {}, TeamAccessPolicies: [] }],
      edge_groups: [{ Id: 1, Name: 'home-workers', Dynamic: false, Endpoints: [4], EndpointIds: [4], TagIds: [], PartialMatch: false, HasEdgeStack: true, EdgeStackCount: 1, TrustedEndpoints: [4], EndpointTypes: [4] }],
      edge_stacks: [{ Id: 1, Name: 'acceptance', Type: 0, DeploymentType: 0, EdgeGroups: [1], Status: { '4': { EndpointID: 4, Status: [{ Type: 3, Time: now(), Error: '' }, { Type: 1, Time: now(), Error: '' }, { Type: 7, Time: now(), Error: '' }], DeploymentInfo: { Version: 1, FileVersion: 1, ConfigHash: 'demo' } } }, Version: 1, NumDeployments: 1, CreationDate: now() - 3600, EntryPoint: 'docker-compose.yml', ManifestPath: '', ProjectPath: '/data/edge_stacks/1', Prune: false, RePullImage: false, EnvVars: [], UseManifestNamespaces: false, PrePullImage: false, UpdateFailureAction: 0, EnableWebhook: false }],
      stacks: [{ Id: 1, Name: 'edge_acceptance', Type: 2, EndpointId: 4, SwarmId: '', EntryPoint: 'docker-compose.yml', Env: [], Status: 1, CreationDate: now() - 3600, CreatedBy: 'admin', UpdateDate: 0, UpdatedBy: '', ProjectPath: '/data/compose/1', ResourceControl: rc('edge_acceptance', 6), AdditionalFiles: [] }],
      edge_jobs: [], registries: [], custom_templates: [], gitops_sources: [], gitops_workflows: [], notifications: [], webhooks: [], accessTokens: [],
      files: [{ Name: 'data', Dir: true, Size: 0, ModTime: now(), Parent: '/', Content: '' }, { Name: 'conference.log', Dir: false, Size: 96, ModTime: now(), Parent: '/', Content: 'EdgeStack.io meeting demo\nThis file is generated locally.\n' }],
      audit: [{ time: iso(), action: '演示数据已就绪', target: 'Server + rk3588-demo-01' }],
    };
  }
  let db;
  try { db = JSON.parse(localStorage.getItem(KEY)); } catch { /* local file storage may be unavailable */ }
  if (!db || db.schema !== 1) db = seed();
  function save() { try { localStorage.setItem(KEY, JSON.stringify(db)); } catch { /* in-memory session remains usable */ } }
  function audit(action, target) { db.audit.unshift({ time: iso(), action, target }); db.audit = db.audit.slice(0, 120); save(); }
  const nextId = (items) => Math.max(0, ...items.map((x) => x.Id || x.ID || 0)) + 1;

  function dockerInfo() {
    return {
      ID: 'DEMO-RK3588-DOCKER', Name: 'rk3588-demo', ServerVersion: '29.9.3', Containers: db.containers.length,
      ContainersRunning: db.containers.filter((c) => c.State.Running).length, ContainersPaused: db.containers.filter((c) => c.State.Paused).length,
      ContainersStopped: db.containers.filter((c) => !c.State.Running).length, Images: db.images.length,
      Driver: 'overlayfs', DriverStatus: [['driver-type', 'io.containerd.snapshotter.v1']], Plugins: { Volume: ['local'], Network: ['bridge', 'host', 'ipvlan', 'macvlan', 'null', 'overlay'], Authorization: [], Log: ['json-file', 'local', 'syslog'] },
      MemoryLimit: true, SwapLimit: true, KernelMemoryTCP: false, CpuCfsPeriod: true, CpuCfsQuota: true, CPUShares: true, CPUSet: true, PidsLimit: true,
      IPv4Forwarding: true, BridgeNfIptables: true, BridgeNfIp6tables: true, Debug: false, NFd: 64, OomKillDisable: false, NGoroutines: 54,
      SystemTime: iso(), LoggingDriver: 'json-file', CgroupDriver: 'systemd', CgroupVersion: '2', NEventsListener: 3,
      KernelVersion: '6.1.118', OperatingSystem: 'Debian GNU/Linux 12 (bookworm)', OSVersion: '12', OSType: 'linux', Architecture: 'aarch64', IndexServerAddress: 'https://index.docker.io/v1/',
      RegistryConfig: { IndexConfigs: { 'docker.io': { Name: 'docker.io', Mirrors: [], Secure: true, Official: true } }, Mirrors: [], InsecureRegistryCIDRs: [], AllowNondistributableArtifactsCIDRs: [], AllowNondistributableArtifactsHostnames: [] },
      NCPU: 8, MemTotal: 16700000000, DockerRootDir: '/var/lib/docker', HttpProxy: '', HttpsProxy: '', NoProxy: '', Labels: [], ExperimentalBuild: false,
      Swarm: { NodeID: '', NodeAddr: '', LocalNodeState: 'inactive', ControlAvailable: false, Error: '', RemoteManagers: [] },
      Runtimes: { runc: { path: 'runc', runtimeType: 'io.containerd.runc.v2' }, 'io.containerd.runc.v2': { path: 'runc', runtimeType: 'io.containerd.runc.v2' } }, DefaultRuntime: 'runc', LiveRestoreEnabled: false, Isolation: '', InitBinary: 'docker-init',
      ContainerdCommit: { ID: 'demo', Expected: 'demo' }, RuncCommit: { ID: 'demo', Expected: 'demo' }, InitCommit: { ID: 'demo', Expected: 'demo' }, SecurityOptions: ['name=seccomp,profile=builtin', 'name=cgroupns'], Warnings: [],
    };
  }
  function envs() {
    return db.environments.map((e) => ({ ...e, Status: db.boardOnline ? 1 : 2, Heartbeat: db.boardOnline, LastCheckInDate: db.boardOnline ? now() : now() - 65,
      Snapshots: [{ Time: now(), DockerVersion: '29.9.3', DockerAPIVersion: '1.56', Swarm: false, IsPodman: false, GpuUseAll: false, TotalCPU: 8, TotalMemory: 16700000000, RunningContainerCount: dockerInfo().ContainersRunning, StoppedContainerCount: dockerInfo().ContainersStopped, ContainerCount: db.containers.length, HealthyContainerCount: 3, UnhealthyContainerCount: 0, VolumeCount: db.volumes.length, ImageCount: db.images.length, ServiceCount: 0, StackCount: db.stacks.length, NodeCount: 1, GpuUseList: [], Snapshots: [], DockerSnapshotRaw: { Info: dockerInfo(), Containers: [], Networks: [], Volumes: [], Images: [], Version: { Version: '29.9.3', ApiVersion: '1.56' } } }],
    }));
  }
  function listContainers() {
    return db.containers.map((c) => ({ Id: c.Id, Names: c.Names, Image: c.Config.Image, ImageID: c.Image, Command: (c.Config.Cmd || []).join(' '), Created: Math.floor(new Date(c.Created).getTime() / 1000), Ports: Object.entries(c.NetworkSettings.Ports || {}).flatMap(([p, v]) => (v || []).map((x) => ({ IP: x.HostIp, PrivatePort: +p.split('/')[0], PublicPort: +x.HostPort, Type: p.split('/')[1] }))), Labels: c.Config.Labels, State: c.State.Paused ? 'paused' : c.State.Status, Status: c.State.Running ? 'Up 1 day' + (c.State.Health.Status === 'healthy' ? ' (healthy)' : '') : 'Exited (0) 1 day ago', HostConfig: { NetworkMode: c.HostConfig.NetworkMode }, NetworkSettings: { Networks: c.NetworkSettings.Networks }, Mounts: c.Mounts, Portainer: c.Portainer }));
  }
  function jsonResponse(data, status = 200, extra = {}) {
    return { status, data, headers: { 'content-type': 'application/json; charset=utf-8', 'x-portainer-agent': '1', 'portainer-agent-api-version': '2', 'api-version': '1.56', ...extra } };
  }
  const textResponse = (text, status = 200, type = 'text/plain; charset=utf-8') => ({ status, text, headers: { 'content-type': type } });
  const missing = (message) => jsonResponse({ message, details: message }, 404);
  const logLines = (id) => {
    const c = db.containers.find((v) => v.Id.startsWith(id)) || db.containers[0];
    return `${new Date().toISOString()} [info] ${c.Name.slice(1)} started successfully\n${new Date().toISOString()} [info] Listening on :8080\n${new Date().toISOString()} [info] GET /edgestack-conference-check HTTP/1.1 200\n${new Date().toISOString()} [info] Demo data is served locally in this HTML file\n`;
  };
  function multiplex(text) {
    let result = '';
    for (const line of text.split('\n').filter(Boolean)) {
      const s = line + '\n', n = new TextEncoder().encode(s).length;
      result += '\x01\0\0\0' + String.fromCharCode((n >>> 24) & 255, (n >>> 16) & 255, (n >>> 8) & 255, n & 255) + s;
    }
    return result;
  }

  function handleDocker(method, path, q, body, endpointId) {
    if (!db.boardOnline) return jsonResponse({ message: '演示设备当前离线。可在「演示控制」中恢复连接。' }, 503);
    const p = path.replace(/^\/v\d+\.\d+/, '') || '/';
    if (p === '/_ping' || p === '/ping') return textResponse('OK');
    if (/^\/v2\/host\/info/.test(p)) return jsonResponse({ PCIDevices: [{ Vendor: 'Rockchip', Name: 'RK3588 NPU', Class: 'Processing accelerators' }], PhysicalDisks: [{ vendor: 'eMMC', model: 'RK3588 Storage', size: 256000000000, name: 'mmcblk0' }] });
    const browse = p.match(/^\/(?:v2\/browse|browse\/[^/]+)\/(ls|get|delete|rename|put)$/);
    if (browse) {
      const target = q.get('path') || q.get('Path') || body.Path || '/';
      const parts = target.split('/').filter(Boolean), name = parts.at(-1) || '', parent = '/' + parts.slice(0, -1).join('/');
      if (browse[1] === 'ls') return jsonResponse(db.files.filter((f) => f.Parent === (target.replace(/\/$/, '') || '/')).map(({ Parent, Content, ...f }) => f));
      const file = db.files.find((f) => f.Name === name && f.Parent === parent);
      if (browse[1] === 'get') return textResponse(file?.Content || 'EdgeStack meeting sample file\n');
      if (browse[1] === 'delete') db.files = db.files.filter((f) => f !== file);
      if (browse[1] === 'rename') { const current = body.CurrentFilePath?.split('/').pop(), next = body.NewFilePath?.split('/').pop(); const found = db.files.find((f) => f.Name === current); if (found) found.Name = next || found.Name; }
      if (browse[1] === 'put') db.files.push({ Name: body.FileName || 'uploaded-demo.txt', Dir: false, Size: String(body.file || '').length, ModTime: now(), Parent: target, Content: body.file || '' });
      audit('文件浏览操作：' + browse[1], target); return jsonResponse({});
    }
    if (p === '/version') return jsonResponse({ Platform: { Name: 'Docker Engine - Community' }, Version: '29.9.3', ApiVersion: '1.56', MinAPIVersion: '1.44', GitCommit: 'demo', GoVersion: 'go1.25.1', Os: 'linux', Arch: 'arm64', KernelVersion: '6.1.118', BuildTime: iso(), Components: [{ Name: 'Engine', Version: '29.9.3', Details: { ApiVersion: '1.56', Arch: 'arm64', Os: 'linux', Experimental: 'false' } }] });
    if (p === '/info') return jsonResponse(dockerInfo());
    if (p === '/events') return textResponse(events.map((x) => JSON.stringify(x)).join('\n'));
    if (p === '/system/df') return jsonResponse({ LayersSize: 4200000000, Images: db.images, Containers: listContainers(), Volumes: db.volumes, BuildCache: [], DiskUsage: { Total: 256000000000, Used: 42000000000, Available: 214000000000 } });
    if (p === '/containers/json') {
      let rows = listContainers();
      if (q.get('all') === '0' || q.get('all') === 'false') rows = rows.filter((c) => c.State === 'running');
      let filters = {};
      try { filters = JSON.parse(q.get('filters') || '{}'); } catch { /* native form validates filters */ }
      for (const [key, choices] of Object.entries(filters)) {
        const values = Array.isArray(choices) ? choices : Object.keys(choices);
        rows = rows.filter((c) => key === 'label' ? values.every((label) => { const [k, v] = label.split('='); return k in c.Labels && (v === undefined || c.Labels[k] === v); }) : key === 'name' ? values.some((v) => c.Names.some((n) => n.includes(v))) : key === 'id' ? values.some((v) => c.Id.startsWith(v)) : key === 'status' ? values.includes(c.State) : key === 'network' ? values.some((v) => v in c.NetworkSettings.Networks) : true);
      }
      return jsonResponse(rows);
    }
    if (p === '/containers/create' && method === 'POST') {
      const name = q.get('name') || 'demo-container-' + (db.containers.length + 1);
      if (db.containers.some((c) => c.Name === '/' + name)) return jsonResponse({ message: 'A container with that name already exists.' }, 409);
      const c = makeContainer(name, body.Image || 'alpine:3.20', false, 0, 20 + db.containers.length);
      c.Config = { ...c.Config, ...body }; c.HostConfig = { ...c.HostConfig, ...(body.HostConfig || {}) };
      if (body.HostConfig?.PortBindings) c.NetworkSettings.Ports = body.HostConfig.PortBindings;
      db.containers.push(c); audit('创建容器', name); return jsonResponse({ Id: c.Id, Warnings: [], Portainer: c.Portainer }, 201);
    }
    let match = p.match(/^\/containers\/([^/]+)(?:\/(.*))?$/);
    if (match) {
      const c = db.containers.find((x) => x.Id.startsWith(match[1]) || x.Name === '/' + match[1]);
      if (!c) return missing('No such container');
      const action = match[2] || '';
      if (action === 'json') return jsonResponse(c);
      if (action === 'logs') return textResponse(multiplex(logLines(c.Id)), 200, 'application/vnd.docker.raw-stream');
      if (action === 'stats') {
        const usage = db.cpu / 100;
        return jsonResponse({ id: c.Id, name: c.Name, read: iso(), preread: new Date(Date.now() - 1000).toISOString(), pids_stats: { current: 4 }, cpu_stats: { cpu_usage: { total_usage: 100000000 + usage * 1000000000, percpu_usage: [20000000, 20000000] }, system_cpu_usage: 5000000000, online_cpus: 8, throttling_data: {} }, precpu_stats: { cpu_usage: { total_usage: 100000000 }, system_cpu_usage: 4000000000, online_cpus: 8 }, memory_stats: { usage: db.memory * 10 * 1048576, limit: 16700000000, stats: { cache: 1048576, total_inactive_file: 0, inactive_file: 0 } }, networks: { eth0: { rx_bytes: 18000000, rx_packets: 7000, rx_errors: 0, rx_dropped: 0, tx_bytes: 7400000, tx_packets: 4000, tx_errors: 0, tx_dropped: 0 } }, blkio_stats: { io_service_bytes_recursive: [{ op: 'Read', value: 10485760 }, { op: 'Write', value: 5242880 }] } });
      }
      if (action === 'top') return jsonResponse({ Titles: ['UID', 'PID', 'PPID', 'C', 'STIME', 'TTY', 'TIME', 'CMD'], Processes: [['root', '1234', '1', '0', '09:00', '?', '00:00:12', '/app/server']] });
      if (action === 'changes') return jsonResponse([{ Path: '/tmp', Kind: 0 }, { Path: '/tmp/conference.log', Kind: 1 }]);
      if (action === 'export' || action === 'archive') return textResponse('EdgeStack meeting demo export\n');
      if (action === 'exec') return jsonResponse({ Id: 'demo-exec-' + Date.now() }, 201);
      if (method === 'DELETE') { db.containers = db.containers.filter((x) => x.Id !== c.Id); audit('移除容器', c.Name); return jsonResponse(null, 204); }
      if (['start', 'stop', 'kill', 'restart', 'pause', 'unpause', 'rename', 'update', 'resize', 'wait'].includes(action)) {
        if (['start', 'restart', 'unpause'].includes(action)) { c.State.Running = true; c.State.Status = 'running'; c.State.Paused = false; c.State.StartedAt = iso(); }
        if (['stop', 'kill'].includes(action)) { c.State.Running = false; c.State.Status = 'exited'; c.State.FinishedAt = iso(); }
        if (action === 'pause') c.State.Paused = true;
        if (action === 'restart') c.RestartCount++;
        if (action === 'rename') { c.Name = '/' + (q.get('name') || body.name); c.Names = [c.Name]; }
        if (action === 'update') c.HostConfig = { ...c.HostConfig, ...body };
        events.unshift({ Type: 'container', Action: action, Actor: { ID: c.Id, Attributes: { name: c.Name.slice(1), image: c.Config.Image } }, time: now(), timeNano: now() * 1e9 });
        audit(action, c.Name); return action === 'wait' ? jsonResponse({ StatusCode: 0 }) : jsonResponse(null, 204);
      }
    }
    if (/^\/exec\//.test(p)) return /json$/.test(p) ? jsonResponse({ ID: p.split('/')[2], Running: false, ExitCode: 0, ProcessConfig: { entrypoint: '/bin/sh', arguments: [] } }) : jsonResponse(null, 200);
    if (p === '/images/json') return jsonResponse(db.images);
    if (p === '/images/create') {
      const from = q.get('fromImage') || 'demo/image';
      const tag = from + (q.get('tag') ? ':' + q.get('tag') : /:[^/]+$/.test(from) ? '' : ':latest');
      if (!db.images.some((x) => x.RepoTags?.includes(tag))) db.images.push({ Id: 'sha256:' + hex(Date.now() % 99999999), ParentId: '', RepoTags: [tag], RepoDigests: [], Created: now(), Size: 23000000, VirtualSize: 23000000, SharedSize: 0, Labels: {}, Containers: 0 });
      audit('拉取镜像', tag);
      return textResponse(['Pulling fs layer', 'Downloading', 'Download complete', 'Pull complete', 'Status: Downloaded newer image for ' + tag].map((status) => JSON.stringify({ status, progress: '', id: 'demo-layer' })).join('\r\n') + '\r\n', 200, 'application/json');
    }
    if (p === '/images/search') return jsonResponse([{ name: 'nginx', description: 'Official nginx image', star_count: 25000, is_official: true, is_automated: false }]);
    if (p === '/images/prune') { const old = db.images.length; db.images = db.images.filter((x) => x.Containers > 0); audit('清理未使用镜像', String(old - db.images.length)); return jsonResponse({ ImagesDeleted: [], SpaceReclaimed: 12345678 }); }
    if (p === '/images/load' || p === '/build') { audit('导入或构建镜像', '会议模拟'); return textResponse(JSON.stringify({ stream: 'Successfully built DEMO_LOCAL_IMAGE\n' }) + '\n', 200, 'application/json'); }
    match = p.match(/^\/images\/(.+?)(?:\/(json|history|tag|push|get))?$/);
    if (match) {
      const key = decodeURIComponent(match[1]);
      const image = db.images.find((x) => x.Id === key || x.Id.replace('sha256:', '').startsWith(key) || x.RepoTags?.includes(key)) || db.images[0];
      if (method === 'DELETE') { db.images = db.images.filter((x) => x.Id !== image.Id); audit('移除镜像', key); return jsonResponse([{ Deleted: image.Id }]); }
      if (match[2] === 'history') return jsonResponse([{ Id: image.Id, Created: image.Created, CreatedBy: '/bin/sh -c #(nop) COPY . /app', Tags: image.RepoTags, Size: image.Size, Comment: '会议示例镜像层' }]);
      if (match[2] === 'tag') { image.RepoTags.push(q.get('repo') + ':' + (q.get('tag') || 'latest')); save(); return jsonResponse(null, 201); }
      return jsonResponse({ ...image, Architecture: 'arm64', Os: 'linux', DockerVersion: '29.9.3', Author: '', Created: new Date(image.Created * 1000).toISOString(), Config: { Env: ['TZ=Asia/Shanghai'], Labels: {}, Cmd: ['/app/server'], Entrypoint: [], ExposedPorts: { '8080/tcp': {} }, WorkingDir: '/app', User: '' }, RootFS: { Type: 'layers', Layers: [image.Id] }, GraphDriver: { Name: 'overlayfs', Data: {} }, Metadata: { LastTagTime: iso() } });
    }
    if (p === '/networks') return jsonResponse(db.networks);
    if (p === '/networks/create') { const network = { ...clone(db.networks[3]), ...body, Id: hex(Date.now() % 99999999), Containers: {}, Created: iso() }; db.networks.push(network); audit('创建网络', network.Name); return jsonResponse({ Id: network.Id, Warning: '' }, 201); }
    if (p === '/networks/prune') return jsonResponse({ NetworksDeleted: [], SpaceReclaimed: 0 });
    match = p.match(/^\/networks\/([^/]+)(?:\/(.*))?$/);
    if (match) {
      const n = db.networks.find((x) => x.Id.startsWith(match[1]) || x.Name === match[1]);
      if (!n) return missing('No such network');
      if (method === 'DELETE') { db.networks = db.networks.filter((x) => x.Id !== n.Id); audit('删除网络', n.Name); return jsonResponse(null, 204); }
      if (match[2]) { audit('更新网络连接', n.Name); return jsonResponse(null, 200); }
      return jsonResponse(n);
    }
    if (p === '/volumes') return jsonResponse({ Volumes: db.volumes, Warnings: [] });
    if (p === '/volumes/create') { const v = { Name: body.Name || 'demo-volume-' + Date.now(), Driver: body.Driver || 'local', Mountpoint: '/var/lib/docker/volumes/' + body.Name + '/_data', CreatedAt: iso(), Labels: body.Labels || {}, Scope: 'local', Options: body.DriverOpts || {}, Status: {}, UsageData: { Size: 0, RefCount: 0 }, Portainer: { ResourceControl: rc(body.Name, 2) } }; db.volumes.push(v); audit('创建数据卷', v.Name); return jsonResponse(v, 201); }
    if (p === '/volumes/prune') return jsonResponse({ VolumesDeleted: [], SpaceReclaimed: 0 });
    match = p.match(/^\/volumes\/(.+)$/);
    if (match) {
      const v = db.volumes.find((x) => x.Name === decodeURIComponent(match[1]));
      if (!v) return missing('No such volume');
      if (method === 'DELETE') { db.volumes = db.volumes.filter((x) => x.Name !== v.Name); audit('删除数据卷', v.Name); return jsonResponse(null, 204); }
      return jsonResponse(v);
    }
    if (p === '/plugins') return jsonResponse([]);
    if (p === '/distribution' || p.startsWith('/distribution/')) return jsonResponse({ Descriptor: { digest: 'sha256:' + hex(8) }, Platforms: [{ architecture: 'arm64', os: 'linux' }] });
    return null;
  }

  function deployStack(stack, content) {
    const text = content || db.compose;
    stack.StackFileContent = text;
    const services = [...text.matchAll(/^  ([a-zA-Z][\w-]*):\s*$/gm)].map((x) => x[1]);
    const image = (text.match(/image:\s*["']?([^\s"']+)/) || [])[1] || 'traefik/whoami:v1.10.3';
    for (const name of services.length ? services : ['app']) {
      const cname = (stack.Name || 'demo') + '-' + name + '-1';
      if (!db.containers.some((c) => c.Name === '/' + cname)) {
        const c = makeContainer(cname, image, true, 18081, 30 + db.containers.length);
        c.Config.Labels['com.docker.compose.project'] = stack.Name;
        c.Config.Labels['com.docker.compose.service'] = name;
        db.containers.push(c);
      }
    }
    save();
  }

  function route(method, url, rawBody) {
    const body = formObject(rawBody);
    let u;
    try { u = new URL(url, 'https://edgestack-demo.invalid/'); } catch { return missing('Invalid demo URL'); }
    let path = decodeURIComponent(u.pathname), q = u.searchParams;
    const ai = path.indexOf('/api/');
    if (ai >= 0) path = path.slice(ai + 4);
    else if (path.startsWith('/api')) path = path.slice(4);
    if (path.includes('/locales/')) return jsonResponse({});
    if (!path.startsWith('/')) path = '/' + path;
    if (path === '/motd') return jsonResponse({ Title: '', Message: '', Content: '', Hash: 'demo', Style: '', Dismissible: true });
    if (path === '/status' || path === '/system/status') return jsonResponse({ Edition: 'CE', Version: '2.45.3', InstanceID: 'EDGESTACK-CONFERENCE-LOCAL', Snapshot: false });
    if (path === '/version' || path === '/system/version') return jsonResponse({ UpdateAvailable: false, LatestVersion: '2.45.3', ServerVersion: '2.45.3', VersionSupport: 'LTS', DatabaseVersion: '2.45.3', Build: { BuildNumber: 'meeting-demo', ImageTag: '2.45.3', NodejsVersion: '22.22.1', PnpmVersion: '10.26.2', WebpackVersion: '5', GoVersion: '1.25', GitCommit: '会议演示' }, Dependencies: { DockerVersion: '29.9.3', HelmVersion: '3.18', KubectlVersion: '1.34', ComposeVersion: '5.7.0' }, Runtime: {} });
    if (path === '/system/info') return jsonResponse({ platform: 'Docker Standalone', agents: 0, edgeAgents: db.environments.length });
    if (path === '/system/nodes') return jsonResponse({ nodes: db.environments.length });
    if (/^\/docker\/\d+\/dashboard$/.test(path)) return jsonResponse({ containers: { total: db.containers.length, running: dockerInfo().ContainersRunning, stopped: dockerInfo().ContainersStopped, healthy: dockerInfo().ContainersRunning, unhealthy: 0 }, services: 0, images: { total: db.images.length, size: db.images.reduce((sum, image) => sum + image.Size, 0) }, volumes: db.volumes.length, networks: db.networks.length, stacks: db.stacks.length });
    if (/^\/docker\/\d+\/images$/.test(path)) return jsonResponse(db.images.map((image) => ({ id: image.Id, tags: image.RepoTags || [], size: image.Size, created: image.Created, used: image.Containers > 0 })));
    if (path === '/system/snapshot' || path === '/endpoints/snapshot') return jsonResponse(null, 204);
    if (path === '/settings' || path === '/settings/public') { if (method !== 'GET') { Object.assign(db.settings, body); audit('保存设置', '中枢 Server'); } return jsonResponse(db.settings); }
    if (path === '/settings/ssl' || path === '/ssl') return method === 'GET' ? jsonResponse({ CertPath: '', KeyPath: '', HTTPEnabled: true, HTTPSOnly: false, SSLCertPath: '', SSLKeyPath: '' }) : jsonResponse({});
    if (path === '/settings/experimental') return jsonResponse({});
    if (path === '/auth/logout') { db.loggedIn = false; save(); return jsonResponse(null, 204); }
    if (path === '/auth') { db.loggedIn = true; save(); return jsonResponse({ jwt: 'DEMO_OFFLINE_SESSION' }); }
    if (path === '/users/admin/check') return jsonResponse(null, 204);
    if (path === '/users/me') return db.loggedIn ? jsonResponse(db.users[0]) : jsonResponse({ message: '演示账户已退出，请用 admin / demo 重新登录。' }, 401);
    if (path === '/users/1/memberships') return jsonResponse(db.memberships);
    if (path === '/team_memberships' || path.startsWith('/team_memberships/')) return crud('memberships', method, path.split('/')[2], body);
    if (/^\/users\/\d+\/tokens/.test(path)) return crud('accessTokens', method, path.split('/').slice(4).join('/'), body);
    if (/^\/users\/\d+\/gitcredentials/.test(path)) return jsonResponse([]);
    if (/^\/users\/\d+\/passwd/.test(path)) { audit('更新演示账户信息', 'admin'); return jsonResponse(null, 204); }
    if (/^\/users\/\d+\/theme/.test(path)) { db.users[0].ThemeSettings = body; save(); return jsonResponse(db.users[0]); }
    let match = path.match(/^\/endpoints\/(\d+)\/docker(\/.*)?$/);
    if (match) return handleDocker(method, match[2] || '/', q, body, +match[1]) || unknown(method, path, body);
    if (path === '/endpoints/summary') return jsonResponse({ total: db.environments.length, up: db.boardOnline ? db.environments.length : 0, down: db.boardOnline ? 0 : db.environments.length, outdated: 0, unassigned: db.environments.filter((e) => e.GroupId === 1).length, byGroup: [{ groupName: 'Unassigned', groupID: 1, count: db.environments.length }], byPlatformType: { docker: db.environments.length, kubernetes: 0, podman: 0, azure: 0 }, byHealth: { down: db.boardOnline ? 0 : 1, up: 0, heartbeat: db.boardOnline ? 1 : 0, outdated: 0 } });
    if (path === '/endpoints/agent_versions') return jsonResponse(['2.45.3']);
    if (path === '/endpoints') {
      if (method === 'POST') { const e = { ...clone(db.environments[0]), Id: Math.max(4, ...db.environments.map((x) => x.Id)) + 1, Name: body.Name || 'rk3588-demo-02', EdgeID: 'DEMO-EDGE-' + Date.now(), TagIds: [], EdgeKey: btoa('https://edgestack-demo.invalid:9443|edgestack-demo.invalid:8000|DEMO_NOT_A_REAL_SECRET|5') }; db.environments.push(e); audit('添加模拟环境', e.Name); return jsonResponse(e, 201); }
      let list = envs();
      const search = q.get('search') || q.get('nameFilter');
      if (search) list = list.filter((e) => e.Name.toLowerCase().includes(search.toLowerCase()));
      const endpointIds = q.get('endpointIds');
      if (endpointIds) { try { const ids = JSON.parse(endpointIds); list = list.filter((e) => ids.includes(e.Id)); } catch { /* keep list */ } }
      return jsonResponse(list, 200, { 'x-total-count': String(list.length), 'x-total-available': String(db.environments.length), 'x-update-available': 'false' });
    }
    match = path.match(/^\/endpoints\/(\d+)(?:\/(.*))?$/);
    if (match) {
      const e = envs().find((x) => x.Id === +match[1]);
      if (!e) return missing('Environment not found');
      const action = match[2];
      if (!action) return crud('environments', method, match[1], body, e);
      if (/registries$/.test(action)) return jsonResponse(db.registries);
      if (/dockerhub/.test(action)) return jsonResponse({ remaining: 99, limit: 100 });
      if (action === 'agent/host/docker-storage') return jsonResponse({ available: false, reason: '演示环境使用固定示例磁盘数据' });
      if (/agent\/ping|ping/.test(action)) return textResponse('', 204);
      if (/edge\/status/.test(action)) return jsonResponse({ Status: db.boardOnline ? 3 : 0, TunnelStatus: db.boardOnline ? 3 : 0, LastActivity: now() });
      if (/browse/.test(action)) return jsonResponse([{ name: 'data', isDir: true, size: 0, modTime: iso(), permissions: 'drwxr-xr-x' }, { name: 'conference.log', isDir: false, size: 1024, modTime: iso(), permissions: '-rw-r--r--' }]);
      if (/disks|devices|gpus/.test(action)) return jsonResponse([]);
      if (/settings/.test(action) && method === 'PUT') { Object.assign(db.environments.find((x) => x.Id === e.Id).SecuritySettings, body); save(); return jsonResponse(e); }
      return method === 'GET' ? jsonResponse({}) : jsonResponse(null, 204);
    }
    if (path === '/stacks' && method === 'GET') return jsonResponse(db.stacks);
    if (path.startsWith('/stacks/create')) {
      const stack = { ...clone(db.stacks[0]), Id: nextId(db.stacks), Name: body.Name || body.name || 'meeting-app', CreationDate: now(), EndpointId: +(q.get('endpointId') || 4), CreatedBy: 'admin', Status: 1, Env: body.Env || [] };
      stack.ResourceControl = rc(stack.Name, 6);
      db.stacks.push(stack); deployStack(stack, body.StackFileContent || body.stackFileContent || body.StackFile || db.compose); audit('部署应用栈', stack.Name); return jsonResponse(stack, 200);
    }
    match = path.match(/^\/stacks\/(\d+)(?:\/(.*))?$/);
    if (match) {
      const stack = db.stacks.find((s) => s.Id === +match[1]);
      if (!stack) return missing('Stack not found');
      if (match[2] === 'file') return jsonResponse({ StackFileContent: stack.StackFileContent || db.compose });
      if (match[2] === 'start' || match[2] === 'stop') { stack.Status = match[2] === 'start' ? 1 : 2; for (const c of db.containers.filter((c) => c.Config.Labels['com.docker.compose.project'] === stack.Name)) { c.State.Running = stack.Status === 1; c.State.Status = c.State.Running ? 'running' : 'exited'; } save(); return jsonResponse(stack); }
      if (method === 'DELETE') { db.stacks = db.stacks.filter((s) => s.Id !== stack.Id); db.containers = db.containers.filter((c) => c.Config.Labels['com.docker.compose.project'] !== stack.Name); audit('删除应用栈', stack.Name); return jsonResponse(null, 204); }
      if (method !== 'GET') { Object.assign(stack, body); deployStack(stack, body.StackFileContent || db.compose); audit('更新应用栈', stack.Name); }
      return jsonResponse(stack);
    }
    if (path === '/templates') return jsonResponse({ version: '3', templates: [
      { id: 1, type: 1, title: 'Nginx', description: '会议演示：部署轻量 Web 服务', logo: '', image: 'nginx:alpine', categories: ['Web'], platform: 'linux', administrator_only: false, ports: ['80/tcp'], volumes: [], env: [], restart_policy: 'unless-stopped', labels: [] },
      { id: 2, type: 1, title: 'Redis', description: '会议演示：键值缓存服务', logo: '', image: 'redis:7-alpine', categories: ['Database'], platform: 'linux', administrator_only: false, ports: ['6379/tcp'], volumes: [], env: [], labels: [] },
      { id: 3, type: 1, title: 'Whoami', description: '会议演示：设备连通性验收服务', logo: '', image: 'traefik/whoami:v1.10.3', categories: ['Tools'], platform: 'linux', administrator_only: false, ports: ['80/tcp'], volumes: [], env: [], labels: [] },
    ] });
    if (path === '/backup/status') return jsonResponse({ Status: 0, Enabled: false });
    if (path === '/backup') return textResponse(JSON.stringify({ format: 'EdgeStack会议模拟备份', data: db }, null, 2), 200, 'application/octet-stream');
    if (path === '/resource_controls' || path.startsWith('/resource_controls/')) {
      const id = +path.split('/')[2];
      const controls = [...db.containers.map((c) => c.Portainer.ResourceControl), ...db.stacks.map((s) => s.ResourceControl), ...db.networks.map((n) => n.Portainer.ResourceControl), ...db.volumes.map((v) => v.Portainer.ResourceControl)];
      const control = controls.find((c) => c.Id === id) || rc(body.ResourceID || body.ResourceId || 'demo', body.Type || 1);
      if (method !== 'GET') { Object.assign(control, { ...body, Public: body.public ?? body.Public ?? control.Public, AdministratorsOnly: body.administratorsOnly ?? body.AdministratorsOnly ?? control.AdministratorsOnly }); audit('更新访问控制', control.ResourceId); }
      return jsonResponse(control);
    }
    if (/\/notifications/.test(path)) return jsonResponse([]);
    if (/auth_logs|activity_logs/.test(path)) return jsonResponse([]);
    if (path === '/dockerhub') return jsonResponse({ Authentication: false, Username: '' });
    if (path === '/endpoints/global-key') return jsonResponse({ key: 'DEMO_NOT_A_REAL_KEY' });
    match = path.match(/^\/(edge_groups|edge_stacks|edge_jobs|endpoint_groups|registries|users|teams|tags|custom_templates|webhooks)(?:\/([^/]+))?(?:\/(.*))?$/);
    if (match) {
      const [, collection, key, action] = match;
      if (action && /file/.test(action)) return jsonResponse({ StackFileContent: db[collection].find((x) => x.Id === +key)?.StackFileContent || db.compose, FileContent: db.compose });
      if (collection === 'edge_stacks' && method === 'POST' && (!key || ['create', 'create_file', 'create_repository', 'create_string'].includes(key))) {
        const stack = { ...clone(db.edge_stacks[0]), ...body, Id: nextId(db.edge_stacks), Name: body.Name || body.name || 'meeting-edge-app', EdgeGroups: body.EdgeGroups || body.edgeGroups || [1], CreationDate: now(), Version: 1, NumDeployments: db.environments.length, Status: {} };
        for (const e of db.environments) stack.Status[e.Id] = { EndpointID: e.Id, Status: [{ Type: 3, Time: now(), Error: '' }, { Type: 1, Time: now(), Error: '' }, { Type: 7, Time: now(), Error: '' }], DeploymentInfo: { Version: 1, FileVersion: 1, ConfigHash: 'demo' } };
        for (const group of db.edge_groups.filter((g) => stack.EdgeGroups.includes(g.Id))) { group.HasEdgeStack = true; group.EdgeStackCount = (group.EdgeStackCount || 0) + 1; }
        db.edge_stacks.push(stack); deployStack(stack, body.StackFileContent || body.stackFileContent || body.StackFile || body.file || db.compose); audit('下发边缘应用栈', stack.Name); return jsonResponse(stack, 201);
      }
      if (collection === 'edge_jobs' && method === 'POST') { const job = { ...body, Id: nextId(db.edge_jobs), Name: body.Name || body.name || 'demo-job', Endpoints: { '4': { LogsStatus: 3, CollectLogs: true } }, GroupLogsCollection: {}, CronExpression: body.CronExpression || body.cronExpression || '* * * * *', Created: now(), Recurring: !!(body.Recurring || body.recurring), ScriptPath: '/demo/job.sh', Version: 1, EdgeGroups: [1], Logs: {} }; db.edge_jobs.push(job); audit('创建边缘任务', job.Name); return jsonResponse(job, 201); }
      if (action && /tasks|logs/.test(action)) return /logs/.test(action) ? textResponse('EdgeStack demo task completed successfully\n') : jsonResponse([]);
      if (collection === 'edge_stacks' && action === 'status') return jsonResponse(db.edge_stacks.find((x) => x.Id === +key)?.Status || {});
      if (collection === 'teams' && action === 'memberships') return jsonResponse(db.memberships);
      return crud(collection, method, key, body);
    }
    if (path.includes('/gitops/')) return jsonResponse([]);
    return unknown(method, path, body);
  }

  function crud(collection, method, key, body, expanded) {
    const spelling = { url: 'URL', userId: 'UserID', userID: 'UserID', teamId: 'TeamID', teamID: 'TeamID' };
    body = Object.fromEntries(Object.entries(body).map(([k, v]) => [spelling[k] || k[0].toUpperCase() + k.slice(1), v]));
    const items = db[collection];
    const item = items.find((x) => (x.Id || x.ID) === +key);
    if (method === 'GET') return jsonResponse(key ? (expanded || item || {}) : items);
    if (method === 'DELETE') { db[collection] = items.filter((x) => (x.Id || x.ID) !== +key); audit('移除 ' + collection, item?.Name || item?.Username || key); return jsonResponse(null, 204); }
    if (method === 'PUT' || method === 'PATCH') { if (item) Object.assign(item, body); audit('更新 ' + collection, item?.Name || key); return jsonResponse(item || body); }
    const value = { ...(collection === 'edge_groups' ? clone(items[0] || {}) : {}), Id: nextId(items), ...body };
    if (collection === 'edge_groups') Object.assign(value, { Endpoints: body.Endpoints || [], TrustedEndpoints: body.Endpoints || [], EndpointTypes: [4], TagIds: body.TagIds || [], HasEdgeStack: false, EdgeStackCount: 0 });
    if (collection === 'users') Object.assign(value, { Role: body.Role || 2, EndpointAuthorizations: {}, PortainerAuthorizations: {}, ThemeSettings: { color: 'dark' } });
    if (collection === 'custom_templates') Object.assign(value, { CreatedByUserId: 1, Platform: body.Platform || 1, Type: body.Type || 2, FileContent: body.FileContent || db.compose });
    if (collection === 'accessTokens') Object.assign(value, { Description: body.Description || '会议演示令牌', DateCreated: now(), LastUsed: 0 });
    items.push(value); audit('新增 ' + collection, value.Name || value.Username || String(value.Id));
    return collection === 'accessTokens' ? jsonResponse({ rawAPIKey: 'DEMO_NOT_A_REAL_ACCESS_TOKEN', apiKey: value }, 201) : jsonResponse(value, 201);
  }
  function unknown(method, path) {
    requests.push({ method, path, unhandled: true });
    return jsonResponse(method === 'GET' && /\/list$|\/roles$|\/tls$|\/jobs$/.test(path) ? [] : {});
  }

  function resolveAsset(url) {
    if (!url || /^(?:data:|blob:|#)/.test(url)) return url;
    const clean = String(url).split(/[?#]/)[0];
    const name = clean.slice(clean.lastIndexOf('/') + 1);
    return assets[clean] || assets[name] || url;
  }
  const nativeSetAttr = Element.prototype.setAttribute;
  Element.prototype.setAttribute = function (name, value) {
    if (/^(?:src|href)$/i.test(name) && /^(?:IMG|SCRIPT|LINK|SOURCE|IMAGE|USE)$/.test(this.tagName)) value = resolveAsset(value);
    if (this.tagName === 'BASE' && name === 'href') value = location.href.split('#')[0];
    return nativeSetAttr.call(this, name, value);
  };
  for (const [type, prop] of [[HTMLImageElement, 'src'], [HTMLScriptElement, 'src'], [HTMLLinkElement, 'href'], [HTMLBaseElement, 'href']]) {
    const desc = Object.getOwnPropertyDescriptor(type.prototype, prop);
    Object.defineProperty(type.prototype, prop, { ...desc, set(value) { return desc.set.call(this, type === HTMLBaseElement ? location.href.split('#')[0] : resolveAsset(value)); } });
  }
  function remap(root) {
    const nodes = root.nodeType === 1 ? [root, ...root.querySelectorAll('img[src],link[href],svg use[href]')] : [];
    for (const el of nodes) for (const attr of ['src', 'href']) if (el.hasAttribute(attr)) {
      const old = el.getAttribute(attr), next = resolveAsset(old);
      if (old !== next) nativeSetAttr.call(el, attr, next);
    }
  }
  new MutationObserver((records) => { for (const r of records) for (const n of r.addedNodes) remap(n); }).observe(document.documentElement, { subtree: true, childList: true });

  class LocalXHR extends EventTarget {
    constructor() { super(); this.readyState = 0; this.status = 0; this.statusText = ''; this.responseText = ''; this.response = null; this.responseType = ''; this.timeout = 0; this.withCredentials = false; this.upload = new EventTarget(); this._headers = {}; this._aborted = false; }
    open(method, url, async = true) { this._method = method.toUpperCase(); this._url = String(url); this._async = async; this.readyState = 1; this._emit('readystatechange'); }
    setRequestHeader(k, v) { this._headers[k.toLowerCase()] = v; }
    getAllResponseHeaders() { return Object.entries(this._responseHeaders || {}).map(([k, v]) => k + ': ' + v).join('\r\n'); }
    getResponseHeader(k) { return this._responseHeaders?.[k.toLowerCase()] ?? null; }
    overrideMimeType() {}
    abort() { this._aborted = true; this._emit('abort'); this._emit('loadend'); }
    _emit(name) { const event = new Event(name); this.dispatchEvent(event); if (typeof this['on' + name] === 'function') this['on' + name](event); }
    send(body) {
      const run = () => {
        if (this._aborted) return;
        let r;
        try { r = route(this._method, this._url, body); }
        catch (error) { console.error('Local demo API:', error); r = jsonResponse({ message: '会议模拟数据异常：' + error.message }, 500); }
        this.status = r.status; this.statusText = r.status < 400 ? 'OK' : 'Demo API error';
        this.responseURL = this._url; this._responseHeaders = r.headers;
        this.responseText = r.text !== undefined ? r.text : r.data == null ? '' : JSON.stringify(r.data);
        if (this.responseType === 'json') this.response = r.data ?? null;
        else if (this.responseType === 'blob') this.response = new Blob([this.responseText], { type: r.headers['content-type'] });
        else if (this.responseType === 'arraybuffer') this.response = new TextEncoder().encode(this.responseText).buffer;
        else this.response = this.responseText;
        requests.push({ method: this._method, path: this._url, status: r.status });
        if (requests.length > 500) requests.splice(0, 100);
        this.readyState = 2; this._emit('readystatechange'); this.readyState = 3; this._emit('readystatechange'); this._emit('progress');
        this.readyState = 4; this._emit('readystatechange'); this._emit('load'); this._emit('loadend');
      };
      if (body instanceof FormData && [...body.values()].some((v) => v instanceof File)) {
        const fileName = [...body.values()].find((v) => v instanceof File)?.name;
        Promise.all([...body.entries()].map(async ([key, value]) => [key, value instanceof File ? await value.text() : value])).then((entries) => { body = { ...Object.fromEntries(entries), FileName: fileName }; run(); });
      } else if (this._async === false) run(); else setTimeout(run, 60);
    }
  }
  Object.assign(LocalXHR, { UNSENT: 0, OPENED: 1, HEADERS_RECEIVED: 2, LOADING: 3, DONE: 4 });
  window.XMLHttpRequest = LocalXHR;
  window.fetch = async function (input, init = {}) {
    const request = input instanceof Request ? input : null;
    const url = request ? request.url : String(input);
    const r = route((init.method || request?.method || 'GET').toUpperCase(), url, init.body || (request ? await request.clone().text() : null));
    requests.push({ method: init.method || request?.method || 'GET', path: url, status: r.status });
    return new Response([204, 205, 304].includes(r.status) ? null : r.text !== undefined ? r.text : JSON.stringify(r.data), { status: r.status, headers: r.headers });
  };
  class LocalWebSocket extends EventTarget {
    constructor(url) { super(); this.url = url; this.readyState = 0; this.binaryType = 'arraybuffer'; this.protocol = ''; this.bufferedAmount = 0; this.command = ''; setTimeout(() => { this.readyState = 1; this._emit('open'); this._message('EdgeStack.io meeting console (local simulation)\r\nroot@rk3588-demo:~# '); }, 120); }
    _emit(name, data) { const e = name === 'message' ? new MessageEvent(name, { data }) : new Event(name); this.dispatchEvent(e); if (this['on' + name]) this['on' + name](e); }
    _message(s) { this._emit('message', this.binaryType === 'arraybuffer' ? new TextEncoder().encode(s).buffer : new Blob([s])); }
    send(data) { const s = typeof data === 'string' ? data : new TextDecoder().decode(data); if (s.includes('\r') || s.includes('\n')) { const cmd = (this.command + s).trim(); this.command = ''; const answer = cmd === 'ls' ? 'data  docker-compose.yml  conference.log' : cmd === 'pwd' ? '/root' : cmd === 'hostname' ? 'rk3588-demo' : cmd.startsWith('docker ps') ? db.containers.filter((x) => x.State.Running).map((x) => x.Id.slice(0, 12) + '  ' + x.Config.Image + '  ' + x.Name.slice(1)).join('\r\n') : cmd.startsWith('echo ') ? cmd.slice(5) : cmd === 'date' ? new Date().toString() : cmd === 'help' ? 'Commands: ls, pwd, hostname, docker ps, date, echo, clear' : cmd === 'clear' ? '\x1b[2J\x1b[H' : cmd ? '已模拟执行：' + cmd : ''; this._message('\r\n' + answer + '\r\nroot@rk3588-demo:~# '); } else { this.command += s; this._message(s); } }
    close() { this.readyState = 3; this._emit('close'); }
  }
  Object.assign(LocalWebSocket, { CONNECTING: 0, OPEN: 1, CLOSING: 2, CLOSED: 3 });
  window.WebSocket = LocalWebSocket;
  navigator.sendBeacon = () => true;
  window.open = (url) => { showHelp(url); return null; };
  document.addEventListener('click', (event) => {
    const link = event.target.closest?.('a[href]');
    if (!link) return;
    const href = link.getAttribute('href');
    if (/^(?:https?:|mailto:)/.test(href)) { event.preventDefault(); event.stopPropagation(); showHelp(href); }
  }, true);

  function showHelp(url) {
    const old = document.getElementById('demo-info'); if (old) old.remove();
    const box = document.createElement('div'); box.id = 'demo-info'; box.className = 'demo-overlay';
    box.innerHTML = '<section class="demo-dialog"><button class="demo-close" aria-label="关闭帮助">×</button><h2>EdgeStack.io 演示说明</h2><p>当前文件内置中文管理界面和本地模拟的 Server、RK3588。所有操作会更新本次会议数据。</p><div class="demo-callout">常用路径：<br>首页 → 选择 RK3588 → 容器 → 日志 / 监控<br>边缘分组 → home-workers → 环境成员<br>边缘应用栈 → 添加应用栈 → 编辑 Compose → 部署</div><p class="demo-muted">外部链接</p><code></code><p>会议使用说明见同目录的「交互文档.md」。服务端和板卡连接状态可在右下角「演示控制」中切换。</p></section>';
    box.querySelector('code').textContent = String(url || '本地会议演示');
    box.querySelector('button').onclick = () => box.remove(); box.onclick = (e) => { if (e.target === box) box.remove(); };
    document.body.append(box);
  }
  function download(name, content, type = 'application/json') { const a = document.createElement('a'); a.href = URL.createObjectURL(new Blob([content], { type })); a.download = name; a.click(); setTimeout(() => URL.revokeObjectURL(a.href), 1000); }
  function controls() {
    const box = document.createElement('div'); box.id = 'demo-tools';
    box.innerHTML = '<button id="demo-toggle" aria-expanded="false">● 会议演示 <span>本地模拟</span></button><section id="demo-panel" hidden><header><strong>演示控制</strong><button id="demo-close-panel" aria-label="关闭演示控制">×</button></header><p class="demo-muted">Server · Windows / WSL<br>Worker · RK3588 / Debian 12 ARM64</p><label>RK3588 连接状态 <select id="demo-board"><option value="1">在线 · 心跳正常</option><option value="0">离线 · 业务容器继续运行</option></select></label><label>CPU 使用率 <input id="demo-cpu" type="range" min="1" max="95"><output id="demo-cpu-value"></output></label><div class="demo-actions"><button data-route="/home">首页</button><button data-route="/4/docker/host">主机</button><button data-route="/4/docker/containers">容器</button><button data-route="/edge/groups">分组</button><button data-route="/edge/stacks">边缘应用栈</button></div><button id="demo-help">交互帮助</button><button id="demo-export">导出会议数据</button><button id="demo-audit">查看操作记录</button><button id="demo-reset" class="demo-danger">恢复初始演示数据</button><p class="demo-muted">原生界面和交互保留。页面中的服务、设备、账户和凭据均为会议示例。</p></section>';
    document.body.append(box);
    const panel = box.querySelector('#demo-panel'), toggle = box.querySelector('#demo-toggle');
    const close = () => { panel.hidden = true; toggle.setAttribute('aria-expanded', 'false'); };
    toggle.onclick = () => { panel.hidden = !panel.hidden; toggle.setAttribute('aria-expanded', String(!panel.hidden)); };
    box.querySelector('#demo-close-panel').onclick = close;
    const board = box.querySelector('#demo-board'); board.value = db.boardOnline ? '1' : '0';
    board.onchange = () => { db.boardOnline = board.value === '1'; audit(db.boardOnline ? '恢复管理连接' : '模拟管理链路离线', 'RK3588'); location.hash = '#!/home'; setTimeout(() => location.reload(), 100); };
    const cpu = box.querySelector('#demo-cpu'); cpu.value = db.cpu;
    const output = box.querySelector('#demo-cpu-value'); output.value = db.cpu + '%';
    cpu.oninput = () => { db.cpu = +cpu.value; output.value = db.cpu + '%'; save(); };
    box.querySelectorAll('[data-route]').forEach((b) => { b.onclick = () => { location.hash = '#!' + b.dataset.route; close(); }; });
    box.querySelector('#demo-help').onclick = () => showHelp('本地交互文档');
    box.querySelector('#demo-export').onclick = () => download('EdgeStack-会议数据.json', JSON.stringify(db, null, 2));
    box.querySelector('#demo-audit').onclick = () => { showHelp('本次会议操作记录'); const block = document.querySelector('#demo-info code'); block.textContent = db.audit.map((x) => new Date(x.time).toLocaleTimeString('zh-CN') + '  ' + x.action + ' · ' + x.target).join('\n'); };
    box.querySelector('#demo-reset').onclick = () => { if (confirm('恢复初始演示数据？本文件中的会议操作将被清除。')) { db = seed(); try { for (const key of Object.keys(localStorage)) if (key.startsWith('portainer.') || key.startsWith('edgestack.conference.')) localStorage.removeItem(key); } catch {} save(); location.hash = '#!/4/docker/host'; location.reload(); } };
    document.addEventListener('keydown', (e) => { if (e.key === 'Escape') { close(); document.getElementById('demo-info')?.remove(); } });
  }
  window.EdgeStackDemo = { version: 1, requests, getState: () => clone(db), reset() { db = seed(); save(); location.reload(); }, route };
  if (!location.hash || location.hash === '#') location.hash = '#!/4/docker/host';
  document.documentElement.lang = 'zh-CN';
  if (document.readyState === 'loading') document.addEventListener('DOMContentLoaded', controls, { once: true }); else controls();
  setInterval(() => { if (db.boardOnline) db.environments.forEach((e) => { e.LastCheckInDate = now(); e.Heartbeat = true; }); }, 5000);
})();
