import { fireEvent, screen, waitFor } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { MemoryRouter, useLocation } from 'react-router';

import ServiceHostsPage, {
  connectionRows,
  ConnectionsPanel,
} from '@/pages/service-hosts/ServiceHostsPage';
import { hostRuntimeFixture } from '@/pages/service-hosts/fixtures';
import { nativeIPv6Bindings } from '@/pages/service-hosts/native-bindings';
import NativeEgressPanel from '@/pages/service-hosts/NativeEgressPanel';
import type { fetchXrayConfig } from '@/hooks/useXraySetting';
import { HttpUtil } from '@/utils';
import { renderWithProviders } from './test-utils';

vi.mock('@/layouts/AppSidebar', () => ({ default: () => null }));
vi.mock('@/api/queries/useNodesQuery', () => ({
  useNodesQuery: () => ({ nodes: [{ id: 1, name: 'US test service' }] }),
}));
afterEach(() => vi.restoreAllMocks());

function Location() {
  const location = useLocation();
  return <output data-testid="location">{location.search}</output>;
}

describe('service hosts', () => {
  it('reads native user bindings and links to the owning service without a legacy controller', () => {
    const config = {
      xraySetting: {
        outbounds: [
          { tag: 'direct', protocol: 'freedom' },
          { tag: 'v4-tunnel', protocol: 'freedom', sendThrough: '192.0.2.4' },
          { tag: 'user-7', protocol: 'freedom', sendThrough: '2001:db8:1::7' },
        ],
        routing: {
          rules: [
            {
              type: 'field',
              user: ['7.sample'],
              inboundTag: ['HY2'],
              ip: ['::/0'],
              outboundTag: 'user-7',
            },
          ],
        },
      },
      runtime: { running: true, heldBack: '' },
    } as Awaited<ReturnType<typeof fetchXrayConfig>>;
    const bindings = nativeIPv6Bindings(config.xraySetting);
    expect(bindings).toHaveLength(1);
    expect(bindings[0].users).toEqual(['7.sample']);
    renderWithProviders(
      <MemoryRouter>
        <NativeEgressPanel nodeId={4} name="HY2 native" config={config} loading={false} />
      </MemoryRouter>,
    );
    expect(screen.getByText('2001:db8:1::7')).toBeTruthy();
    expect(screen.getByRole('link', { name: 'Outbounds' }).getAttribute('href')).toBe(
      '/outbound?nodeId=4',
    );
    expect(screen.queryByText(/legacy controller/i)).toBeNull();
  });

  it('compares native configured sources with observed sockets independently', () => {
    const report = {
      ...hostRuntimeFixture,
      observer: {
        ...hostRuntimeFixture.observer,
        records: [
          {
            ...hostRuntimeFixture.observer.records[0],
            outbound: 'native-user-7',
            local: '[2001:db8:1::7]:30000',
          },
        ],
      },
    };
    const bindings = {
      1: [
        {
          key: 'native-user-7',
          outbound: 'native-user-7',
          address: '2001:db8:1::7',
          users: ['7.sample'],
          inbounds: ['TEST_ENTRY'],
          destinations: ['::/0'],
          enabled: true,
        },
      ],
    };
    expect(connectionRows([report], bindings)[0].allocationVerified).toBe(true);
    bindings[1][0].address = '2001:db8:1::9';
    const changed = connectionRows([report], bindings)[0];
    expect(changed.assignedIPv6).toBe('2001:db8:1::9');
    expect(changed.local).toBe('[2001:db8:1::7]:30000');
    expect(changed.allocationVerified).toBe(false);
  });
  it('keeps observed socket source separate from assigned post-NAT policy', () => {
    const rows = connectionRows([hostRuntimeFixture]);
    expect(rows.find((r) => r.user === '7.sample')?.assignedIPv6).toBe('2001:db8:1::7');
    expect(rows.find((r) => r.user === '7.sample')?.local).toBe('[2001:db8:1::a]:30000');
    const ordinary = {
      ...hostRuntimeFixture,
      observer: {
        ...hostRuntimeFixture.observer,
        records: [{ ...hostRuntimeFixture.observer.records[0], outbound: 'direct' }],
      },
    };
    expect(connectionRows([ordinary])[0].assignedIPv6).toBeUndefined();
    renderWithProviders(<ConnectionsPanel reports={[hostRuntimeFixture]} />);
    expect(screen.getByText('IPv4 → IPv6')).toBeTruthy();
    expect(screen.getByText('IPv6 → IPv4')).toBeTruthy();
    expect(screen.getByText('2001:db8:1::7')).toBeTruthy();
    expect(screen.getByText(/not a live connection count/)).toBeTruthy();
  });

  it('shows unavailable observer explicitly and never invents connection records', () => {
    renderWithProviders(
      <ConnectionsPanel
        reports={[{ ...hostRuntimeFixture, observer: { ready: false, dropped: 0, records: [] } }]}
      />,
    );
    expect(screen.getByText(/Connection observer not ready/)).toBeTruthy();
    expect(screen.queryByText('7.sample')).toBeNull();
  });

  it('opens the host connection deep link and changes settings in the page', async () => {
    const host = {
      id: 3,
      name: 'Test physical machine',
      note: '',
      services: [{ id: 1, name: 'US test service', status: 'online' }],
    };
    vi.spyOn(HttpUtil, 'get').mockImplementation(async (url) => ({
      success: true,
      msg: '',
      obj: url.includes('/runtime/')
        ? [hostRuntimeFixture]
        : url.includes('/get/')
          ? { ...host, lines: [] }
          : [host],
    }));
    renderWithProviders(
      <MemoryRouter initialEntries={['/service-hosts?id=3&tab=connections']}>
        <ServiceHostsPage />
        <Location />
      </MemoryRouter>,
    );
    await screen.findByText('IPv4 → IPv6');
    fireEvent.click(screen.getByRole('tab', { name: 'Host settings' }));
    await waitFor(() =>
      expect(screen.getByTestId('location').textContent).toContain('tab=settings'),
    );
    expect(screen.getByLabelText('Host name')).toBeTruthy();
    expect(screen.queryByRole('dialog')).toBeNull();
  });

  it('shows native entries without retired exit annotations or a second routing control', async () => {
    const host = {
      id: 3,
      name: 'Imported machine',
      note: '',
      services: [{ id: 1, name: 'US test service', status: 'online' }],
    };
    const line = {
      id: 108,
      nodeId: 1,
      tag: 'legacy-ipv6',
      name: 'Imported IPv6 entry',
      protocol: 'hysteria',
      transport: 'hysteria',
      listen: '',
      port: 22444,
      enable: true,
      egressMode: 'user_ipv6',
      note: '',
      endpoints: [],
    };
    const post = vi.spyOn(HttpUtil, 'post');
    vi.spyOn(HttpUtil, 'get').mockImplementation(async (url) => ({
      success: true,
      msg: '',
      obj: url.includes('/get/') ? { ...host, lines: [line] } : [host],
    }));
    renderWithProviders(
      <MemoryRouter initialEntries={['/service-hosts?id=3&tab=lines']}>
        <ServiceHostsPage />
      </MemoryRouter>,
    );
    await screen.findByText('Imported IPv6 entry');
    expect(screen.queryByText('Migration exit record')).toBeNull();
    expect(screen.queryByText(/Read-only migration records/)).toBeNull();
    expect(screen.queryByRole('combobox')).toBeNull();
    fireEvent.click(screen.getByText('Imported IPv6 entry'));
    expect(post).not.toHaveBeenCalled();
  });
});
