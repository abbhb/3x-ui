import { fireEvent, screen, waitFor, within } from '@testing-library/react';
import { beforeEach, expect, it, vi } from 'vitest';
import { commerceRequest } from '@/api/commerce';
import { PlanEditor } from '@/pages/commerce/CommerceForms';
import { PlanSchema, type Plan } from '@/schemas/commerce';
import { renderWithProviders } from './test-utils';

vi.mock('@/api/commerce', async (original) => ({
  ...(await original<typeof import('@/api/commerce')>()),
  commerceRequest: vi.fn(),
}));
vi.mock('@/api/queries/useInboundOptions', () => ({
  useInboundOptions: () => ({
    data: [
      { id: 1, protocol: 'vless', enable: true, remark: 'West' },
      { id: 2, protocol: 'hysteria', enable: true, remark: 'East' },
      { id: 3, protocol: 'vless', enable: true, remark: 'North' },
      { id: 4, protocol: 'vless', enable: false, remark: 'Offline' },
      { id: 5, protocol: 'vmess', enable: true, remark: 'Unsupported' },
    ],
  }),
}));

const catalog = [
  [11, 1, 'City direct'],
  [12, 1, 'City relay'],
  [21, 2, 'Island'],
  [31, 3, 'Stopped'],
  [41, 4, 'Offline host'],
  [51, 5, 'Unsupported host'],
].map(([host_id, inbound_id, name]) => ({
  host_id,
  inbound_id,
  name,
  address: `host-${host_id}.example`,
  signature: `sig-${host_id}`,
  disabled: host_id === 31,
}));
const plan = PlanSchema.parse({
  id: 9,
  name: 'Monthly',
  description: '',
  price_minor: 1000,
  currency: 'CNY',
  duration_days: 30,
  data_limit: 0,
  inbound_ids: [],
  host_ids: [],
  is_visible: false,
  has_routes: true,
  created_at: 0,
  updated_at: 0,
});

beforeEach(() => {
  vi.mocked(commerceRequest)
    .mockReset()
    .mockImplementation(async (path) => {
      if (path === 'subscriptions/catalog') return catalog;
      if (path === 'subscriptions/policy')
        return { document: '{"proxy-groups":[{"name":"Default"}]}' };
      return [];
    });
});

async function openPlan(value: Plan | null) {
  renderWithProviders(<PlanEditor plan={value} close={vi.fn()} />);
  return screen.findByRole('button', { name: 'Choose entries' });
}
async function edit(trigger: HTMLElement) {
  fireEvent.click(trigger);
  return within(await screen.findByRole('dialog'));
}
function search(dialog: ReturnType<typeof within>, value: string) {
  fireEvent.change(dialog.getByRole('textbox', { name: /Search entry/ }), { target: { value } });
}
async function save(path: string) {
  fireEvent.click(screen.getByRole('button', { name: 'Save' }));
  await waitFor(() =>
    expect(vi.mocked(commerceRequest).mock.calls.some(([p]) => p === path)).toBe(true),
  );
  return vi.mocked(commerceRequest).mock.calls.find(([p]) => p === path)?.[2];
}

it('searches across inbounds by entry name or address and only commits a confirmed selection', async () => {
  const trigger = await openPlan(null);
  fireEvent.change(screen.getByLabelText('Name'), { target: { value: 'Monthly' } });
  const dialog = await edit(trigger);
  expect(dialog.queryByRole('button', { name: /Offline|Unsupported/ })).toBeNull();
  search(dialog, 'host-12.example');
  fireEvent.click(dialog.getByRole('checkbox', { name: /City relay/ }));
  search(dialog, 'City direct');
  fireEvent.click(dialog.getByRole('checkbox', { name: /City direct/ }));
  search(dialog, 'Island');
  fireEvent.click(dialog.getByRole('checkbox', { name: /Island/ }));
  fireEvent.click(dialog.getByRole('button', { name: 'Confirm' }));
  await screen.findByText('Included active entries · 3');
  expect(vi.mocked(commerceRequest).mock.calls.some(([p]) => p === 'plans')).toBe(false);
  const body = await save('plans');
  expect(body).toMatchObject({
    inbound_ids: [1, 2],
    host_ids: [12, 11, 21],
    host_signatures: { 11: 'sig-11', 12: 'sig-12', 21: 'sig-21' },
    auto_host_inbound_ids: [],
  });
  expect(body).not.toHaveProperty('entries');
});

it('shows inherited, overlapping and disabled grants and discards cancelled edits', async () => {
  const trigger = await openPlan({
    ...plan,
    inbound_ids: [2, 1, 3],
    host_ids: [11, 31],
    auto_host_inbound_ids: [1],
  });
  const dialog = await edit(trigger);
  expect(dialog.getByText('Included by inbound')).toBeTruthy();
  expect(dialog.getByText('Inbound + individual')).toBeTruthy();
  expect((dialog.getByRole('checkbox', { name: /City relay/ }) as HTMLInputElement).checked).toBe(
    true,
  );
  expect((dialog.getByRole('checkbox', { name: /City relay/ }) as HTMLInputElement).disabled).toBe(
    true,
  );
  fireEvent.click(dialog.getByRole('checkbox', { name: 'Include all entries on this inbound' }));
  expect((dialog.getByRole('checkbox', { name: /City direct/ }) as HTMLInputElement).checked).toBe(
    true,
  );
  expect((dialog.getByRole('checkbox', { name: /City relay/ }) as HTMLInputElement).checked).toBe(
    false,
  );
  fireEvent.click(dialog.getByRole('button', { name: /^East/ }));
  expect(
    (dialog.getByRole('checkbox', { name: 'Existing binding only' }) as HTMLInputElement).checked,
  ).toBe(true);
  fireEvent.click(dialog.getByRole('button', { name: /^North/ }));
  expect(dialog.getByRole('checkbox', { name: /Stopped.*Disabled/ })).toBeTruthy();
  fireEvent.click(dialog.getByRole('button', { name: 'Cancel' }));
  expect(await save('plans/9')).toMatchObject({
    inbound_ids: [2, 1, 3],
    host_ids: [11, 31],
    host_signatures: { 11: 'sig-11', 31: 'sig-31' },
    auto_host_inbound_ids: [1],
  });
});

it('narrows an inbound grant to a single entry and prunes obsolete group members', async () => {
  const dialog = await edit(
    await openPlan({
      ...plan,
      inbound_ids: [2, 1],
      host_ids: [21],
      auto_host_inbound_ids: [1],
      group_members: { Default: ['inbound:1', 'inbound:2'] },
    }),
  );
  fireEvent.click(dialog.getByRole('checkbox', { name: /Island/ }));
  fireEvent.click(dialog.getByRole('button', { name: /^West/ }));
  fireEvent.click(dialog.getByRole('checkbox', { name: 'Include all entries on this inbound' }));
  fireEvent.click(dialog.getByRole('checkbox', { name: /City direct/ }));
  fireEvent.click(dialog.getByRole('checkbox', { name: 'Included only' }));
  expect(dialog.queryByText('City relay')).toBeNull();
  expect(dialog.queryByRole('button', { name: /^East/ })).toBeNull();
  fireEvent.click(dialog.getByRole('button', { name: 'Confirm' }));
  expect(await save('plans/9')).toMatchObject({
    inbound_ids: [1],
    host_ids: [11],
    auto_host_inbound_ids: [],
    group_members: { Default: ['inbound:1'] },
  });
});

it('requires explicit reselection of an entry whose signature changed', async () => {
  const trigger = await openPlan({
    ...plan,
    inbound_ids: [1],
    host_ids: [11],
    host_signatures: { 11: 'old' },
  });
  fireEvent.click(screen.getByRole('button', { name: 'Save' }));
  await screen.findByText(/Remove the unavailable grant/);
  expect(vi.mocked(commerceRequest).mock.calls.some(([p]) => p === 'plans/9')).toBe(false);
  const dialog = await edit(trigger);
  fireEvent.click(dialog.getByRole('button', { name: /Remove · Unavailable grant #11/ }));
  fireEvent.click(dialog.getByRole('checkbox', { name: /City direct/ }));
  fireEvent.click(dialog.getByRole('button', { name: 'Confirm' }));
  expect(await save('plans/9')).toMatchObject({
    inbound_ids: [1],
    host_signatures: { 11: 'sig-11' },
  });
});

it('saves an external-only plan without granting a local entry', async () => {
  await openPlan({ ...plan, external_selections: [{ source_id: 6, mode: 'all', node_ids: [] }] });
  expect(await save('plans/9')).toMatchObject({
    inbound_ids: [],
    host_ids: [],
    auto_host_inbound_ids: [],
    external_selections: [{ source_id: 6, mode: 'all' }],
  });
});

it('follows all entries on an inbound from filtered results and counts each active entry once', async () => {
  const dialog = await edit(
    await openPlan({
      ...plan,
      inbound_ids: [1],
      host_ids: [11],
      group_members: { Default: ['inbound:1'] },
    }),
  );
  search(dialog, 'host-12.example');
  fireEvent.click(dialog.getByRole('checkbox', { name: 'Include all entries on this inbound' }));
  expect(dialog.getByText('2 of 2 active entries included')).toBeTruthy();
  fireEvent.click(dialog.getByRole('button', { name: 'Confirm' }));
  await screen.findByText('Included active entries · 2');
  expect(await save('plans/9')).toMatchObject({
    inbound_ids: [1],
    host_ids: [11],
    auto_host_inbound_ids: [1],
    group_members: { Default: ['inbound:1'] },
  });
});
