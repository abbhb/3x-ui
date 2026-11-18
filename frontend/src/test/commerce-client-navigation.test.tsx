import { fireEvent, screen, waitFor } from '@testing-library/react';
import { MemoryRouter, Route, Routes, useLocation, useNavigate } from 'react-router';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import CommercePage from '@/pages/commerce/CommercePage';
import ClientsPage from '@/pages/clients/ClientsPage';
import { AccountClientLink } from '@/pages/commerce/CommerceShared';
import { commerceRequest } from '@/api/commerce';
import { HttpUtil, Msg } from '@/utils';
import { renderWithProviders } from './test-utils';

vi.mock('@/layouts/AppSidebar', () => ({ default: () => null }));
vi.mock('@/hooks/useWebSocket', () => ({ useWebSocket: () => ({}) }));
vi.mock('@/api/commerce', async (original) => ({
  ...(await original<typeof import('@/api/commerce')>()),
  commerceRequest: vi.fn(),
}));

const account = {
  id: 5,
  username: 'portal-name-differs-from-client-email',
  wallet_balance_minor: 0,
  is_active: true,
  client_id: 7,
  user_id: 7,
  created_at: 0,
  subscription: null,
  usage: { status: 'active', used_traffic: 0, lifetime_used_traffic: 0, data_limit: 0, expire: 0 },
  runtime_pending: false,
};

function Navigation() {
  const location = useLocation();
  const navigate = useNavigate();
  return (
    <>
      <output data-testid="location">{location.pathname + location.search}</output>
      <button onClick={() => navigate('/clients?clientId=8')}>Another account</button>
      <button onClick={() => navigate(-1)}>Browser back</button>
    </>
  );
}

beforeEach(() => {
  localStorage.clear();
  sessionStorage.clear();
  window.X_UI_BASE_PATH = '/custom/';
  vi.mocked(commerceRequest).mockImplementation(async (path) => {
    if (path.startsWith('accounts?')) return [account];
    if (path === 'accounts/5') return account;
    return [];
  });
});
afterEach(() => {
  window.X_UI_BASE_PATH = '/';
  vi.restoreAllMocks();
  vi.mocked(commerceRequest).mockReset();
});

it('links accounts to exact clients, resets remembered filters, supports a second link and clearing it', async () => {
  localStorage.setItem(
    'clientsFilterState',
    JSON.stringify({
      searchKey: 'old search',
      filters: { buckets: ['depleted'], nodeIds: [99], groups: ['old'] },
      sort: '',
      pageSize: 25,
    }),
  );
  const queries: URLSearchParams[] = [];
  vi.spyOn(HttpUtil, 'get').mockImplementation(async (url: string) => {
    if (!url.includes('/clients/list/paged')) return new Msg(true, '', []);
    const query = new URL(url, 'http://panel.test').searchParams;
    queries.push(query);
    const id = query.get('clientId');
    const items = [7, 8]
      .filter((n) => !id || n === Number(id))
      .map((n) => ({
        id: n,
        email: `native-client-${n}`,
        enable: true,
        inboundIds: [],
      }));
    return new Msg(true, '', {
      items,
      total: 2,
      filtered: items.length,
      page: 1,
      pageSize: 25,
      groups: [],
      summary: { total: 2, active: 2, online: [], depleted: [], expiring: [], deactive: [] },
    });
  });
  vi.spyOn(HttpUtil, 'post').mockImplementation(
    async (url: string) =>
      new Msg(true, '', url.includes('/setting/defaultSettings') ? { pageSize: 25 } : []),
  );
  renderWithProviders(
    <MemoryRouter
      basename="/custom/admin"
      initialEntries={['/custom/admin/commerce?tab=routes&route=10001']}
    >
      <Navigation />
      <Routes>
        <Route path="/commerce" element={<CommercePage />} />
        <Route path="/clients" element={<ClientsPage />} />
      </Routes>
    </MemoryRouter>,
  );
  await screen.findByRole('button', { name: account.username });
  expect(screen.queryByRole('tab', { name: /cascades|XHTTP/i })).toBeNull();
  expect(screen.getByTestId('location').textContent).toBe('/commerce?tab=accounts');
  expect(screen.getByRole('link', { name: 'View client' }).getAttribute('href')).toBe(
    '/custom/admin/clients?clientId=7',
  );
  fireEvent.click(screen.getByRole('button', { name: account.username }));
  fireEvent.click(await screen.findByRole('link', { name: 'View client' }));
  await screen.findByText('native-client-7');
  expect(screen.queryByText('native-client-8')).toBeNull();
  expect(queries[0].get('clientId')).toBe('7');
  for (const key of ['search', 'filter', 'inbound', 'group'])
    expect(queries[0].has(key)).toBe(false);
  expect(screen.getByText('Client #7')).toBeTruthy();
  fireEvent.click(screen.getByRole('button', { name: 'Browser back' }));
  await screen.findByRole('link', { name: 'View client' });
  expect(screen.getByTestId('location').textContent).toBe('/commerce?account=5');
  fireEvent.click(screen.getByRole('link', { name: 'View client' }));
  await screen.findByText('native-client-7');
  fireEvent.click(screen.getByRole('button', { name: 'Another account' }));
  await screen.findByText('native-client-8');
  await waitFor(() => expect(screen.queryByText('native-client-7')).toBeNull());
  expect(queries.at(-1)?.get('clientId')).toBe('8');
  fireEvent.click(screen.getByRole('button', { name: 'Clear all' }));
  await screen.findByText('native-client-7');
  expect(screen.getByText('native-client-8')).toBeTruthy();
  expect(queries.at(-1)?.has('clientId')).toBe(false);
  expect(screen.getByTestId('location').textContent).toBe('/clients');
  expect(
    vi.mocked(commerceRequest).mock.calls.some(([path]) => path.startsWith('subscriptions/hosts')),
  ).toBe(false);
});

it('does not offer an unfiltered client link for an account without a client', () => {
  renderWithProviders(
    <MemoryRouter>
      <AccountClientLink clientID={null} />
    </MemoryRouter>,
  );
  expect(screen.getByText('No linked client')).toBeTruthy();
  expect(screen.queryByRole('link')).toBeNull();
});
