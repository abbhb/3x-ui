import { fireEvent, screen, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import PortalPage from '@/pages/portal/PortalPage';
import MGMAControls from '@/pages/commerce/MGMAControls';
import { renderWithProviders } from './test-utils';
import type { Issued } from '@/schemas/commerce';

const account = {
  id: 5,
  username: 'synthetic-buyer',
  wallet_balance_minor: 3000,
  is_active: true,
  client_id: 7,
  user_id: 7,
  created_at: 1793491200000,
  subscription: null,
  usage: {
    status: 'active',
    used_traffic: 1234,
    lifetime_used_traffic: 1234,
    data_limit: 1000000,
    expire: 4102444800000,
  },
  runtime_pending: false,
};
const plan = {
  id: 4,
  name: 'Synthetic plan',
  description: 'Synthetic only',
  price_minor: 1299,
  currency: 'CNY',
  duration_days: 30,
  data_limit: 1000000,
  inbound_ids: [101],
  host_ids: [1001],
  is_visible: true,
  compatibility_pending: false,
  created_at: 0,
  updated_at: 0,
};
beforeEach(() => {
  sessionStorage.clear();
  window.X_UI_BASE_PATH = '/';
});
afterEach(() => {
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

describe('customer portal', () => {
  it('retries an uncertain purchase with exactly the same key and clears the pending intent only after confirmation', async () => {
    sessionStorage.setItem('xui.portal.session', 'synthetic-session');
    const keys: string[] = [];
    let fail = true;
    vi.stubGlobal(
      'fetch',
      vi.fn(async (url: string, options: RequestInit) => {
        const path = new URL(url, 'http://example.test').pathname;
        let data: unknown = null;
        if (path.endsWith('/config')) data = { enabled: true };
        else if (path.endsWith('/me')) data = account;
        else if (path.endsWith('/plans')) data = [plan];
        else if (path.endsWith('/transactions')) data = [];
        else if (path.endsWith('/purchase')) {
          keys.push((options.headers as Record<string, string>)['Idempotency-Key']);
          if (fail) {
            fail = false;
            throw new Error('synthetic unknown network result');
          }
          data = { ...account, wallet_balance_minor: 1701 };
        }
        return new Response(JSON.stringify(data), {
          status: 200,
          headers: { 'Content-Type': 'application/json' },
        });
      }),
    );
    renderWithProviders(<PortalPage />);
    await screen.findByText('Synthetic plan');
    fireEvent.click(screen.getByRole('button', { name: 'Purchase' }));
    fireEvent.click(await screen.findByRole('button', { name: 'Confirm' }));
    await screen.findByText(/previous transaction needs confirmation/);
    expect(sessionStorage.getItem('xui.purchase.5')).toContain(keys[0]);
    fireEvent.click(screen.getByRole('button', { name: 'Retry pending transaction' }));
    await waitFor(() => expect(keys).toHaveLength(2));
    expect(keys[1]).toBe(keys[0]);
    await waitFor(() => expect(sessionStorage.getItem('xui.purchase.5')).toBeNull());
  });

  it('restores an unresolved purchase after reload and blocks a second purchase', async () => {
    sessionStorage.setItem('xui.portal.session', 'synthetic-session');
    sessionStorage.setItem(
      'xui.purchase.5',
      JSON.stringify({ key: '11111111-1111-4111-8111-111111111111', plan_id: 4 }),
    );
    vi.stubGlobal(
      'fetch',
      vi.fn(
        async (url: string) =>
          new Response(
            JSON.stringify(
              url.endsWith('/config')
                ? { enabled: true }
                : url.endsWith('/me')
                  ? account
                  : url.endsWith('/plans')
                    ? [plan]
                    : [],
            ),
            { headers: { 'Content-Type': 'application/json' } },
          ),
      ),
    );
    renderWithProviders(<PortalPage />);
    await screen.findByText(/previous transaction needs confirmation/);
    expect((await screen.findByRole('button', { name: 'Purchase' })).hasAttribute('disabled')).toBe(
      true,
    );
  });
});

describe('MGMA one-time display', () => {
  const grant = (): Issued => ({
    url: 'https://portal.example.test/sub/synthetic/?token=' + 'G'.repeat(43),
    issued_at: new Date().toISOString(),
    expires_at: null,
    ttl_seconds: 0,
  });
  it('clears the displayed URL on hide and page visibility changes, without storing it', async () => {
    renderWithProviders(
      <MGMAControls
        issue={async () => grant()}
        revoke={async () => {}}
        regenerate={async () => {}}
      />,
    );
    fireEvent.click(screen.getByRole('button', { name: 'Generate link' }));
    await screen.findByRole('textbox', { name: 'Subscription access' });
    expect(JSON.stringify({ ...sessionStorage, ...localStorage })).not.toContain('token=');
    fireEvent.click(screen.getByRole('button', { name: 'Hide and clear' }));
    expect(screen.queryByRole('textbox')).toBeNull();
    fireEvent.click(screen.getByRole('button', { name: 'Generate link' }));
    await screen.findByRole('textbox');
    vi.spyOn(document, 'hidden', 'get').mockReturnValue(true);
    fireEvent(document, new Event('visibilitychange'));
    expect(screen.queryByRole('textbox')).toBeNull();
  });
  it('discards an already expired issuance on its next timer tick', async () => {
    renderWithProviders(
      <MGMAControls
        issue={async () => ({
          ...grant(),
          expires_at: new Date(Date.now() - 1000).toISOString(),
          ttl_seconds: 30,
        })}
        revoke={async () => {}}
        regenerate={async () => {}}
      />,
    );
    fireEvent.click(screen.getByRole('button', { name: 'Generate link' }));
    await screen.findByRole('textbox');
    await waitFor(() => expect(screen.queryByRole('textbox')).toBeNull(), { timeout: 2200 });
  });
});
