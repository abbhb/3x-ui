import { fireEvent, screen, waitFor } from '@testing-library/react';
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
  return screen.findByRole('combobox', { name: 'Subscription entries' });
}

async function choose(input: HTMLElement, search: string, option: RegExp) {
  fireEvent.mouseDown(input);
  fireEvent.change(input, { target: { value: search } });
  const row = await screen.findByText(option, { selector: '.ant-select-item-option-content' });
  fireEvent.click(row);
}

async function save(path: string) {
  fireEvent.click(screen.getByRole('button', { name: 'Save' }));
  await waitFor(() =>
    expect(vi.mocked(commerceRequest).mock.calls.some(([p]) => p === path)).toBe(true),
  );
  return vi.mocked(commerceRequest).mock.calls.find(([p]) => p === path)?.[2];
}

it('creates a plan by searching entries across inbounds and derives distinct parent bindings', async () => {
  const input = await openPlan(null);
  fireEvent.change(screen.getByLabelText('Name'), { target: { value: 'Monthly' } });
  fireEvent.mouseDown(input);
  expect(screen.queryByText(/Stopped ·|Unsupported host ·|Offline host ·/)).toBeNull();
  await choose(input, 'host-12.example', /^City relay ·/);
  await choose(input, 'City direct', /^City direct ·/);
  await choose(input, 'Island', /^Island ·/);
  const body = await save('plans');
  expect(body).toMatchObject({
    inbound_ids: [1, 2],
    host_ids: [12, 11, 21],
    host_signatures: { 11: 'sig-11', 12: 'sig-12', 21: 'sig-21' },
    auto_host_inbound_ids: [],
  });
  expect(body).not.toHaveProperty('entries');
});

it('preserves saved binding order, disabled entries, automatic grants and an inbound-only binding', async () => {
  await openPlan({ ...plan, inbound_ids: [2, 1, 3], host_ids: [31], auto_host_inbound_ids: [2] });
  expect(screen.getByRole('button', { name: /Remove All entries · East/ })).toBeTruthy();
  expect(screen.getByRole('button', { name: /Remove Existing binding only · West/ })).toBeTruthy();
  expect(await save('plans/9')).toMatchObject({
    inbound_ids: [2, 1, 3],
    host_ids: [31],
    host_signatures: { 31: 'sig-31' },
    auto_host_inbound_ids: [2],
  });
});

it('narrows a whole inbound to one entry and removes obsolete group members when its last grant is removed', async () => {
  const input = await openPlan({
    ...plan,
    inbound_ids: [2, 1],
    host_ids: [11],
    auto_host_inbound_ids: [2],
    group_members: { Default: ['inbound:1', 'inbound:2'] },
  });
  await choose(input, 'Island', /^Island ·/);
  fireEvent.click(screen.getByRole('button', { name: /Remove All entries · East/ }));
  fireEvent.click(screen.getByRole('button', { name: /Remove City direct/ }));
  expect(await save('plans/9')).toMatchObject({
    inbound_ids: [2],
    host_ids: [21],
    auto_host_inbound_ids: [],
    group_members: { Default: ['inbound:2'] },
  });
});

it('requires explicit reselection when an entry signature has changed', async () => {
  const input = await openPlan({
    ...plan,
    inbound_ids: [1],
    host_ids: [11],
    host_signatures: { 11: 'old' },
  });
  fireEvent.click(screen.getByRole('button', { name: 'Save' }));
  await screen.findByText(/Remove the unavailable grant/);
  expect(vi.mocked(commerceRequest).mock.calls.some(([p]) => p === 'plans/9')).toBe(false);
  fireEvent.click(screen.getByRole('button', { name: /Remove Unavailable grant #11/ }));
  await choose(input, 'City direct', /^City direct ·/);
  expect(await save('plans/9')).toMatchObject({
    inbound_ids: [1],
    host_signatures: { 11: 'sig-11' },
  });
});

it('saves an external-only plan without granting a local entry', async () => {
  const selections = [{ source_id: 6, mode: 'all' as const, node_ids: [] }];
  await openPlan({ ...plan, external_selections: selections });
  expect(await save('plans/9')).toMatchObject({
    inbound_ids: [],
    host_ids: [],
    auto_host_inbound_ids: [],
    external_selections: [{ source_id: 6, mode: 'all' }],
  });
});

it('adds a future-entry grant explicitly and keeps a group through a temporary empty selection', async () => {
  const input = await openPlan({
    ...plan,
    inbound_ids: [1],
    host_ids: [11],
    group_members: { Default: ['inbound:1'] },
  });
  fireEvent.click(screen.getByRole('button', { name: /Remove City direct/ }));
  await choose(input, 'West', /^All entries · West/);
  expect(await save('plans/9')).toMatchObject({
    inbound_ids: [1],
    host_ids: [],
    host_signatures: {},
    auto_host_inbound_ids: [1],
    group_members: { Default: ['inbound:1'] },
  });
});
