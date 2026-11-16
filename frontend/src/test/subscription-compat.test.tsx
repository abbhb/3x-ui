import { fireEvent, screen, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import ExternalSources from '@/pages/subscriptions/ExternalSources';
import SubscriptionPolicy from '@/pages/subscriptions/SubscriptionPolicy';
import SubscriptionRoutes from '@/pages/subscriptions/SubscriptionRoutes';
import { commerceRequest } from '@/api/commerce';
import { renderWithProviders } from './test-utils';

vi.mock('@/api/commerce', async (original) => ({
  ...(await original<typeof import('@/api/commerce')>()),
  commerceRequest: vi.fn(),
}));

beforeEach(() => {
  sessionStorage.clear();
  localStorage.clear();
  window.X_UI_BASE_PATH = '/';
});
afterEach(() => {
  vi.unstubAllGlobals();
  vi.resetAllMocks();
});

it('creates a private source, publishes YAML and clears both write-only inputs without persisting them', async () => {
  const source = {
    id: 12,
    owner_account_id: 5,
    owner_username: '',
    name: 'My source',
    group_name: 'My source',
    enabled: true,
    refresh_interval_minutes: 360,
    credential_configured: true,
    created_at: 0,
    updated_at: 0,
    last_attempt_at: 0,
    last_success_at: 0,
    next_refresh_at: 0,
    node_count: 0,
    skipped_node_count: 0,
    status: 'empty',
    last_error_code: '',
  };
  const writes: { path: string; body: string }[] = [];
  vi.stubGlobal(
    'fetch',
    vi.fn(async (url: string, options: RequestInit) => {
      expect((options.headers as Record<string, string>).Authorization).toBe(
        'Bearer private-session',
      );
      expect(url.startsWith('/api/portal/external-subscriptions')).toBe(true);
      if (options.method === 'POST') writes.push({ path: url, body: String(options.body) });
      const data = url.includes('/nodes?') || url.includes('?page=') ? [] : source;
      return new Response(JSON.stringify(data), {
        headers: { 'Content-Type': 'application/json' },
      });
    }),
  );
  renderWithProviders(<ExternalSources token="private-session" />);
  fireEvent.click(await screen.findByRole('button', { name: 'Add source' }));
  fireEvent.change(screen.getByLabelText('Name'), { target: { value: 'My source' } });
  const secret = 'https://synthetic.example.test/sub?token=private-secret';
  fireEvent.change(screen.getByLabelText('Subscription credential'), { target: { value: secret } });
  fireEvent.submit(screen.getByLabelText('Name').closest('form')!);
  await screen.findByRole('button', { name: 'Replace credential' });
  expect(writes[0].body).toContain(secret);
  expect(document.querySelector(`input[value="${secret}"]`)).toBeNull();
  const replacement = screen.getByLabelText('New subscription URL') as HTMLInputElement;
  fireEvent.change(replacement, { target: { value: secret + '-replacement' } });
  fireEvent.click(screen.getByRole('button', { name: 'Replace credential' }));
  await waitFor(() =>
    expect((screen.getByLabelText('New subscription URL') as HTMLInputElement).value).toBe(''),
  );
  const yaml = screen.getByLabelText('Or paste YAML') as HTMLTextAreaElement;
  fireEvent.change(yaml, {
    target: { value: 'proxies: [{name: only-me, type: socks5, server: test, port: 1080}]' },
  });
  fireEvent.click(screen.getByRole('button', { name: 'Publish snapshot' }));
  await waitFor(() => expect(yaml.value).toBe(''));
  expect(writes.map((w) => w.path)).toEqual([
    '/api/portal/external-subscriptions',
    '/api/portal/external-subscriptions/12/credential',
    '/api/portal/external-subscriptions/12/snapshot',
  ]);
  expect(JSON.stringify({ ...sessionStorage, ...localStorage })).not.toContain('private-secret');
});

it('submits the loaded policy revision and keeps edited rules visible when a concurrent edit is rejected', async () => {
  const policy = {
    revision: 7,
    enabled: true,
    secure_dns: true,
    primary_group: 'Primary',
    group_order: [],
    document: JSON.stringify({
      'proxy-groups': [{ name: 'Primary', type: 'select', proxies: ['__MGMA_LOCAL_NODES__'] }],
      rules: ['MATCH,Primary'],
    }),
  };
  vi.mocked(commerceRequest).mockImplementation(async (_path, _schema, _body, method) => {
    if (method === 'PUT') throw new Error('revision conflict');
    return policy;
  });
  renderWithProviders(<SubscriptionPolicy />);
  const rules = await screen.findByLabelText('Rules (one per line)');
  fireEvent.change(rules, { target: { value: 'DOMAIN,example.test,Primary\nMATCH,Primary' } });
  fireEvent.click(screen.getByRole('button', { name: 'Save policy' }));
  await screen.findByText(/Save failed. Check rule targets/);
  const call = vi.mocked(commerceRequest).mock.calls.find((args) => args[3] === 'PUT');
  expect(call?.[2]).toMatchObject({ revision: 7 });
  expect((rules as HTMLTextAreaElement).value).toContain('DOMAIN,example.test,Primary');
});

it('preserves an untouched XHTTP connection when saving other route settings', async () => {
  const stream = JSON.stringify({
    address: 'up.example.test',
    port: 443,
    network: 'xhttp',
    security: 'none',
    xhttpSettings: { path: '/x/', mode: 'packet-up' },
  });
  const route = {
    host_id: 10002,
    inbound_id: 102,
    remark: 'XHTTP',
    address: 'up.example.test',
    path: '/x/',
    network: 'xhttp',
    settings: {
      host_id: 10002,
      revision: 3,
      cascade_mode: 'off',
      entry_configured: false,
      entry_node_id: null,
      client_stream: stream,
      mihomo_only: true,
    },
  };
  vi.mocked(commerceRequest).mockImplementation(async (_path, _schema, _body, method) =>
    method === 'PUT' ? route.settings : route,
  );
  renderWithProviders(<SubscriptionRoutes selected={10002} select={() => {}} />);
  fireEvent.click(await screen.findByRole('button', { name: 'Save route settings' }));
  await waitFor(() =>
    expect(vi.mocked(commerceRequest).mock.calls.some((call) => call[3] === 'PUT')).toBe(true),
  );
  const body = vi.mocked(commerceRequest).mock.calls.find((call) => call[3] === 'PUT')?.[2];
  expect(body).toMatchObject({ revision: 3, client_stream: stream });
});
