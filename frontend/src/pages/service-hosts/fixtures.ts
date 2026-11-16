import type { HostRuntime } from '@/schemas/service-host';

// Public documentation addresses and synthetic account names only.
export const hostRuntimeFixture: HostRuntime = {
  nodeId: 1,
  name: 'US test service',
  installed: true,
  policy: {
    enabled: true,
    namespace: 'test',
    prefix: '2001:db8:1::/64',
    interface: 'eth0',
    mark_prefix: 100,
    inbounds: ['TEST_ENTRY'],
  },
  status: {
    enabled: true,
    verified: true,
    updatedAt: 1794800000000,
    reason: '',
    users: [{ identity: '7', address: '2001:db8:1::7' }],
    blockedUsers: [],
  },
  inbounds: [{ id: 101, tag: 'TEST_ENTRY', protocol: 'vless' }],
  observer: {
    ready: true,
    dropped: 0,
    records: [
      {
        id: 'sample-1',
        timestamp: '2026-11-16T06:00:00Z',
        user: '7.sample',
        source: 'tcp:192.0.2.10:10000',
        target: 'example.test:443',
        network: 'tcp',
        inbound: 'TEST_ENTRY',
        outbound: 'mz6-example',
        local: '[2001:db8:1::a]:30000',
        remote: '[2001:db8:2::1]:443',
        clientFamily: 'IPv4',
        egressFamily: 'IPv6',
      },
      {
        id: 'sample-2',
        timestamp: '2026-11-16T06:00:01Z',
        user: '9.sample',
        source: 'tcp:[2001:db8:3::9]:10001',
        target: 'ipv4.example.test:443',
        network: 'tcp',
        inbound: 'TEST_ENTRY',
        outbound: 'direct',
        local: '192.0.2.1:30001',
        remote: '198.51.100.1:443',
        clientFamily: 'IPv6',
        egressFamily: 'IPv4',
      },
    ],
  },
};
