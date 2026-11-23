import { screen } from '@testing-library/react';
import { MemoryRouter } from 'react-router';
import { expect, it, vi } from 'vitest';
import ClientInfoModal from '@/pages/clients/ClientInfoModal';
import ClientQrModal from '@/pages/clients/ClientQrModal';
import SubLinksModal from '@/pages/clients/SubLinksModal';
import { HttpUtil, Msg } from '@/utils';
import { renderWithProviders } from './test-utils';

const settings = {
  enable: true,
  subURI: 'https://panel.example/sub/',
  subJsonURI: 'https://panel.example/json/',
  subJsonEnable: true,
  subClashURI: 'https://panel.example/clash/',
  subClashEnable: true,
  happLinkEnable: true,
};
const managed = { id: 7, email: 'managed@example.com', subId: 'managed-client', requireMGMA: true };
const ordinary = { id: 8, email: 'ordinary@example.com', subId: 'ordinary-client' };
const notice = 'Subscription requires an access token';
const portal = 'Open customer portal';

it('replaces protected subscription links in client details and keeps ordinary links usable', async () => {
  vi.spyOn(HttpUtil, 'get').mockResolvedValue(new Msg(true, '', []));
  const props = {
    open: true,
    inboundsById: {},
    isOnline: false,
    subSettings: settings,
    onOpenChange: vi.fn(),
  };
  const { rerender } = renderWithProviders(<ClientInfoModal {...props} client={managed} />);

  expect(await screen.findByText(notice)).toBeTruthy();
  expect(screen.getByRole('link', { name: portal }).getAttribute('href')).toBe('/portal/');
  expect(screen.queryByRole('link', { name: managed.subId })).toBeNull();

  rerender(<ClientInfoModal {...props} client={ordinary} />);
  const links = await screen.findAllByRole('link', { name: ordinary.subId });
  expect(links.map((link) => link.getAttribute('href'))).toEqual([
    settings.subURI + ordinary.subId,
    settings.subJsonURI + ordinary.subId,
    settings.subClashURI + ordinary.subId,
  ]);
  expect(screen.queryByText(notice)).toBeNull();
});

it('shows the portal entry in the QR dialog for a protected subscription', async () => {
  vi.spyOn(HttpUtil, 'get').mockResolvedValue(new Msg(true, '', []));
  renderWithProviders(
    <MemoryRouter>
      <ClientQrModal
        open
        client={managed}
        inboundsById={{}}
        subSettings={settings}
        onOpenChange={vi.fn()}
      />
    </MemoryRouter>,
  );

  expect(await screen.findByRole('link', { name: portal })).toBeTruthy();
  expect(screen.queryByText(settings.subURI + managed.subId)).toBeNull();
});

it('lists ordinary subscription links in a mixed bulk export dialog', async () => {
  renderWithProviders(
    <SubLinksModal
      open
      emails={[managed.email, ordinary.email]}
      clients={[managed, ordinary]}
      subSettings={settings}
      onOpenChange={vi.fn()}
    />,
  );

  expect(await screen.findByRole('link', { name: portal })).toBeTruthy();
  expect(screen.getByText(settings.subURI + ordinary.subId)).toBeTruthy();
  expect(screen.queryByText(settings.subURI + managed.subId)).toBeNull();
});
