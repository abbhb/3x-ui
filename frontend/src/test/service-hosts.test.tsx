import { fireEvent, screen, waitFor } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { MemoryRouter, useLocation } from 'react-router';

import ServiceHostsPage, {
  connectionRows,
  ConnectionsPanel,
} from '@/pages/service-hosts/ServiceHostsPage';
import { hostRuntimeFixture } from '@/pages/service-hosts/fixtures';
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

  it('shows migrated exit annotations without a fake routing control', async () => {
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
    await screen.findByText('Migration exit record');
    expect(screen.getByText(/Read-only migration records/)).toBeTruthy();
    expect(screen.queryByRole('combobox')).toBeNull();
    fireEvent.click(screen.getByText('Imported IPv6 entry'));
    expect(post).not.toHaveBeenCalled();
  });
});
