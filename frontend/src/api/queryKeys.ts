export const keys = {
  sponsors: () => ['sponsors'] as const,
  server: {
    status: () => ['server', 'status'] as const,
    fail2banStatus: () => ['server', 'fail2banStatus'] as const,
  },
  nodes: {
    root: () => ['nodes'] as const,
    list: () => ['nodes', 'list'] as const,
  },
  hosts: {
    root: () => ['hosts'] as const,
    list: () => ['hosts', 'list'] as const,
    raw: () => ['hosts', 'raw'] as const,
    byInbound: (inboundId: number) => ['hosts', 'byInbound', inboundId] as const,
    tags: () => ['hosts', 'tags'] as const,
  },
  subBalancers: {
    root: () => ['sub-balancers'] as const,
    list: () => ['sub-balancers', 'list'] as const,
  },
  settings: {
    root: () => ['settings'] as const,
    all: () => ['settings', 'all'] as const,
    defaults: () => ['settings', 'defaults'] as const,
    factoryDefaults: () => ['settings', 'factoryDefaults'] as const,
  },
  inbounds: {
    root: () => ['inbounds'] as const,
    slim: () => ['inbounds', 'slim'] as const,
    options: () => ['inbounds', 'options'] as const,
    amneziawgKernelReadiness: () => ['inbounds', 'amneziawgKernelReadiness'] as const,
  },
  clients: {
    root: () => ['clients'] as const,
    list: (params: unknown) => ['clients', 'list', params] as const,
    all: () => ['clients', 'all'] as const,
    onlines: () => ['clients', 'onlines'] as const,
    onlinesByGuid: () => ['clients', 'onlinesByGuid'] as const,
    activeInbounds: () => ['clients', 'activeInbounds'] as const,
    lastOnline: () => ['clients', 'lastOnline'] as const,
    groups: () => ['clients', 'groups'] as const,
  },
  xray: {
    root: () => ['xray'] as const,
    config: (nodeId = 0) => ['xray', 'config', nodeId] as const,
    outboundsTraffic: (nodeId = 0) => ['xray', 'outboundsTraffic', nodeId] as const,
    geodata: {
      root: () => ['xray', 'geodata'] as const,
      files: (nodeId = 0) => ['xray', 'geodata', 'files', nodeId] as const,
      categories: (file: string, query: string, nodeId = 0) =>
        ['xray', 'geodata', 'categories', file, query, nodeId] as const,
      entries: (
        file: string,
        code: string,
        query: string,
        offset: number,
        limit: number,
        nodeId = 0,
      ) => ['xray', 'geodata', 'entries', file, code, query, offset, limit, nodeId] as const,
    },
  },
} as const;
