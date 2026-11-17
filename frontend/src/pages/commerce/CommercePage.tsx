import { useEffect, useState } from 'react';
import { useSearchParams } from 'react-router';
import { useQuery } from '@tanstack/react-query';
import { z } from 'zod';
import {
  Alert,
  Button,
  Card,
  ConfigProvider,
  DatePicker,
  Form,
  Input,
  InputNumber,
  Layout,
  Select,
  Space,
  Spin,
  Table,
  Tabs,
  Tag,
  Typography,
} from 'antd';
import AppSidebar from '@/layouts/AppSidebar';
import { useTheme } from '@/hooks/useTheme';
import { commerceRequest as request, emptyResponse } from '@/api/commerce';
import {
  AccountSchema,
  PlanSchema,
  SettingsViewSchema,
  InvitationSchema,
  BlockSchema,
  RuntimeSchema,
  type Account,
  type Plan,
} from '@/schemas/commerce';
import { useLabel, PlanEditor, SettingsEditor } from './CommerceForms';
import { AccountClientLink, SecretNotice, PageButtons } from './CommerceShared';
import AccountDetail from './AccountDetail';
import ExternalSources from '@/pages/subscriptions/ExternalSources';
import SubscriptionPolicy from '@/pages/subscriptions/SubscriptionPolicy';
import { useSubscriptionLabel } from '@/pages/subscriptions/ExternalSources';
import { money, date, bytes } from './commerceFormat';
import './Commerce.css';
export default function CommercePage() {
  const l = useLabel();
  const sl = useSubscriptionLabel();
  const { antdThemeConfig, isDark, isUltra } = useTheme();
  const [params, setParams] = useSearchParams();
  const requestedTab = params.get('tab') || 'accounts';
  const tab = requestedTab === 'routes' ? 'accounts' : requestedTab;
  const accountID = Number(params.get('account') || 0);
  const [page, setPage] = useState(1);
  const [search, setSearch] = useState('');
  const [plan, setPlan] = useState<Plan | null | undefined>();
  const [secret, setSecret] = useState('');
  const [error, setError] = useState(false);
  const [busy, setBusy] = useState(false);
  useEffect(() => {
    if (requestedTab === 'routes') {
      const next = new URLSearchParams(params);
      next.set('tab', 'accounts');
      next.delete('route');
      setParams(next, { replace: true });
    }
  }, [requestedTab, params, setParams]);
  const accounts = useQuery({
    queryKey: ['commerce', 'accounts', page, search],
    queryFn: () =>
      request(`accounts?page=${page}&search=${encodeURIComponent(search)}`, z.array(AccountSchema)),
    enabled: tab === 'accounts' && !accountID,
  });
  const plans = useQuery({
    queryKey: ['commerce', 'plans'],
    queryFn: () => request('plans', z.array(PlanSchema)),
  });
  const settings = useQuery({
    queryKey: ['commerce', 'settings'],
    queryFn: () => request('settings', SettingsViewSchema),
    enabled: tab === 'settings',
  });
  const invitations = useQuery({
    queryKey: ['commerce', 'invitations', page],
    queryFn: () => request(`invitations?page=${page}`, z.array(InvitationSchema)),
    enabled: tab === 'invitations',
  });
  const blocks = useQuery({
    queryKey: ['commerce', 'blocks', page],
    queryFn: () => request(`security/blocks?page=${page}`, z.array(BlockSchema)),
    enabled: tab === 'security',
  });
  const runtime = useQuery({
    queryKey: ['commerce', 'runtime', page],
    queryFn: () => request(`runtime?page=${page}`, z.array(RuntimeSchema)),
    enabled: tab === 'runtime',
    refetchInterval: tab === 'runtime' ? 10000 : false,
  });
  async function perform(work: () => Promise<unknown>) {
    setError(false);
    setBusy(true);
    try {
      await work();
    } catch {
      setError(true);
    } finally {
      setBusy(false);
    }
  }
  function changeTab(key: string) {
    setParams({ tab: key });
    setPage(1);
    setPlan(undefined);
    setSecret('');
    setError(false);
  }
  const failed =
    error ||
    accounts.error ||
    plans.error ||
    (tab === 'settings' && settings.error) ||
    (tab === 'invitations' && invitations.error) ||
    (tab === 'security' && blocks.error) ||
    (tab === 'runtime' && runtime.error);
  return (
    <ConfigProvider theme={antdThemeConfig}>
      <Layout className={`page-layout${isDark ? ' dark' : ''}${isUltra ? ' ultra' : ''}`}>
        <AppSidebar />
        <Layout className="content-shell">
          <Layout.Content id="content-layout" className="content-area">
            <div className="commerce-page commerce-stack">
              <header className="commerce-toolbar">
                <Typography.Title level={1}>{l('adminTitle')}</Typography.Title>
                <Button
                  href={`${window.X_UI_BASE_PATH || '/'}portal/`}
                  target="_blank"
                  rel="noopener noreferrer"
                >
                  {l('openPortal')}
                </Button>
              </header>
              {failed && <Alert type="error" showIcon title={l('failed')} />}
              <SecretNotice secret={secret} clear={() => setSecret('')} />
              {accountID > 0 ? (
                <AccountDetail
                  key={accountID}
                  id={accountID}
                  plans={plans.data || []}
                  back={() => setParams({ tab: 'accounts' })}
                />
              ) : (
                <Tabs
                  activeKey={tab}
                  onChange={changeTab}
                  destroyOnHidden
                  items={[
                    {
                      key: 'external',
                      label: sl('sources'),
                      children: (
                        <ExternalSources
                          selected={Number(params.get('source') || 0)}
                          select={(id) =>
                            setParams(
                              id ? { tab: 'external', source: String(id) } : { tab: 'external' },
                            )
                          }
                        />
                      ),
                    },
                    { key: 'policy', label: sl('policy'), children: <SubscriptionPolicy /> },
                    {
                      key: 'accounts',
                      label: l('accounts'),
                      children: (
                        <Space orientation="vertical" size="large" className="commerce-full">
                          <Input.Search
                            aria-label={l('searchAccount')}
                            placeholder={l('searchAccount')}
                            allowClear
                            onSearch={(v) => {
                              setSearch(v);
                              setPage(1);
                            }}
                          />
                          <Table
                            loading={accounts.isFetching}
                            rowKey="id"
                            dataSource={accounts.data || []}
                            pagination={false}
                            scroll={{ x: 700 }}
                            columns={[
                              {
                                title: l('username'),
                                render: (_, a: Account) => (
                                  <Button
                                    type="link"
                                    onClick={() => setParams({ account: String(a.id) })}
                                  >
                                    {a.username}
                                  </Button>
                                ),
                              },
                              {
                                title: l('client'),
                                render: (_, a: Account) => (
                                  <AccountClientLink clientID={a.client_id} />
                                ),
                              },
                              {
                                title: l('balance'),
                                dataIndex: 'wallet_balance_minor',
                                render: money,
                              },
                              {
                                title: l('currentPlan'),
                                render: (_, a) => a.subscription?.plan_name || '—',
                              },
                              { title: l('status'), render: (_, a) => l(a.usage.status) },
                              { title: l('expires'), render: (_, a) => date(a.usage.expire) },
                            ]}
                          />
                          <PageButtons
                            page={page}
                            count={accounts.data?.length || 0}
                            change={setPage}
                          />
                          <Card title={l('createAccount')}>
                            <Form
                              layout="vertical"
                              onFinish={(v) =>
                                void perform(async () => {
                                  const r = await request(
                                    'accounts',
                                    z.object({ username: z.string(), password: z.string() }),
                                    v,
                                  );
                                  setSecret(`${r.username}\n${r.password}`);
                                  await accounts.refetch();
                                })
                              }
                            >
                              <Form.Item
                                label={l('username')}
                                name="username"
                                rules={[{ required: true }, { pattern: /^[a-zA-Z0-9_@.-]{3,32}$/ }]}
                              >
                                <Input autoComplete="off" maxLength={32} />
                              </Form.Item>
                              <Form.Item
                                label={l('plans')}
                                name="plan_id"
                                rules={[{ required: true }]}
                              >
                                <Select
                                  options={(plans.data || [])
                                    .filter((p) => p.has_routes)
                                    .map((p) => ({ value: p.id, label: p.name }))}
                                />
                              </Form.Item>
                              <Button type="primary" htmlType="submit" loading={busy}>
                                {l('createAccount')}
                              </Button>
                            </Form>
                          </Card>
                        </Space>
                      ),
                    },
                    {
                      key: 'plans',
                      label: l('plans'),
                      children:
                        plan !== undefined ? (
                          <PlanEditor
                            key={plan?.id || 'new'}
                            plan={plan}
                            close={() => {
                              setPlan(undefined);
                              void plans.refetch();
                            }}
                          />
                        ) : (
                          <Space orientation="vertical" size="large" className="commerce-full">
                            <Button type="primary" onClick={() => setPlan(null)}>
                              {l('createPlan')}
                            </Button>
                            <Table
                              rowKey="id"
                              dataSource={plans.data || []}
                              pagination={{ pageSize: 20 }}
                              scroll={{ x: 650 }}
                              columns={[
                                {
                                  title: l('name'),
                                  render: (_, p: Plan) => (
                                    <Button type="link" onClick={() => setPlan(p)}>
                                      {p.name}
                                    </Button>
                                  ),
                                },
                                { title: l('price'), dataIndex: 'price_minor', render: money },
                                { title: l('days'), dataIndex: 'duration_days' },
                                {
                                  title: l('usage'),
                                  dataIndex: 'data_limit',
                                  render: (v: number) => (v ? bytes(v) : l('unlimited')),
                                },
                                {
                                  title: l('status'),
                                  render: (_, p) =>
                                    !p.has_routes ? (
                                      <Tag>{l('unconfiguredPlan')}</Tag>
                                    ) : (
                                      l(p.is_visible ? 'visible' : 'hidden')
                                    ),
                                },
                              ]}
                            />
                          </Space>
                        ),
                    },
                    {
                      key: 'invitations',
                      label: l('invitations'),
                      children: (
                        <Space orientation="vertical" size="large" className="commerce-full">
                          <Card title={l('createInvitation')}>
                            <Form
                              layout="vertical"
                              initialValues={{
                                note: '',
                                max_uses: 1,
                                starts_at: null,
                                expires_at: null,
                              }}
                              onFinish={(v) =>
                                void perform(async () => {
                                  const r = await request(
                                    'invitations',
                                    z.object({ code: z.string() }),
                                    {
                                      ...v,
                                      starts_at: v.starts_at?.valueOf() || 0,
                                      expires_at: v.expires_at?.valueOf() || 0,
                                    },
                                  );
                                  setSecret(r.code);
                                  await invitations.refetch();
                                })
                              }
                            >
                              <Form.Item name="note" label={l('note')}>
                                <Input maxLength={500} />
                              </Form.Item>
                              <Form.Item
                                name="max_uses"
                                label={l('maxUses')}
                                extra={l('zeroUnlimited')}
                              >
                                <InputNumber min={0} precision={0} />
                              </Form.Item>
                              <Form.Item name="starts_at" label={l('validFromMs')}>
                                <DatePicker showTime className="commerce-full" />
                              </Form.Item>
                              <Form.Item
                                name="expires_at"
                                label={l('expiresMs')}
                                extra={l('zeroPermanent')}
                              >
                                <DatePicker showTime className="commerce-full" />
                              </Form.Item>
                              <Button type="primary" htmlType="submit" loading={busy}>
                                {l('createInvitation')}
                              </Button>
                            </Form>
                          </Card>
                          <Table
                            rowKey="id"
                            dataSource={invitations.data || []}
                            pagination={false}
                            scroll={{ x: 650 }}
                            columns={[
                              { title: l('prefix'), dataIndex: 'prefix' },
                              { title: l('note'), dataIndex: 'note' },
                              {
                                title: l('uses'),
                                render: (_, r) =>
                                  `${r.use_count} / ${r.max_uses || l('unlimited')}`,
                              },
                              { title: l('expires'), dataIndex: 'expires_at', render: date },
                              {
                                title: l('status'),
                                render: (_, r) =>
                                  r.disabled_at ? (
                                    l('disabled')
                                  ) : (
                                    <Button
                                      onClick={() =>
                                        void perform(async () => {
                                          await request(
                                            `invitations/${r.id}/disable`,
                                            emptyResponse,
                                            {},
                                          );
                                          await invitations.refetch();
                                        })
                                      }
                                    >
                                      {l('disable')}
                                    </Button>
                                  ),
                              },
                            ]}
                          />
                          <PageButtons
                            page={page}
                            count={invitations.data?.length || 0}
                            change={setPage}
                          />
                        </Space>
                      ),
                    },
                    {
                      key: 'security',
                      label: l('security'),
                      children: (
                        <Space orientation="vertical" size="large" className="commerce-full">
                          <Card title={l('createBlock')}>
                            <Form
                              layout="vertical"
                              initialValues={{ expires_at: null }}
                              onFinish={(v) =>
                                void perform(async () => {
                                  await request('security/blocks', emptyResponse, {
                                    ...v,
                                    expires_at: v.expires_at?.valueOf() || 0,
                                  });
                                  await blocks.refetch();
                                })
                              }
                            >
                              <Form.Item
                                name="network"
                                label={l('network')}
                                rules={[{ required: true }]}
                              >
                                <Input />
                              </Form.Item>
                              <Form.Item
                                name="reason"
                                label={l('reason')}
                                rules={[{ required: true }]}
                              >
                                <Input maxLength={500} />
                              </Form.Item>
                              <Form.Item
                                name="expires_at"
                                label={l('expiresMs')}
                                extra={l('zeroPermanent')}
                              >
                                <DatePicker showTime className="commerce-full" />
                              </Form.Item>
                              <Button type="primary" htmlType="submit" loading={busy}>
                                {l('createBlock')}
                              </Button>
                            </Form>
                          </Card>
                          <Table
                            rowKey="id"
                            dataSource={blocks.data || []}
                            pagination={false}
                            scroll={{ x: 700 }}
                            columns={[
                              { title: l('network'), dataIndex: 'network' },
                              { title: l('reason'), dataIndex: 'reason' },
                              { title: l('source'), dataIndex: 'source' },
                              { title: l('expires'), dataIndex: 'expires_at', render: date },
                              {
                                title: l('status'),
                                render: (_, r) =>
                                  r.revoked_at ? (
                                    l('revoked')
                                  ) : (
                                    <Button
                                      onClick={() =>
                                        void perform(async () => {
                                          await request(
                                            `security/blocks/${r.id}/revoke`,
                                            emptyResponse,
                                            {},
                                          );
                                          await blocks.refetch();
                                        })
                                      }
                                    >
                                      {l('revokeLink')}
                                    </Button>
                                  ),
                              },
                            ]}
                          />
                          <PageButtons
                            page={page}
                            count={blocks.data?.length || 0}
                            change={setPage}
                          />
                        </Space>
                      ),
                    },
                    {
                      key: 'runtime',
                      label: l('runtime'),
                      children: (
                        <Space orientation="vertical" className="commerce-full" size="large">
                          <Alert type="info" showIcon title={l('runtimeHelp')} />
                          <Button
                            loading={busy}
                            onClick={() =>
                              void perform(async () => {
                                await request('runtime/retry', emptyResponse, {});
                                await runtime.refetch();
                              })
                            }
                          >
                            {l('retryRuntime')}
                          </Button>
                          <Table
                            rowKey="id"
                            dataSource={runtime.data || []}
                            pagination={false}
                            scroll={{ x: 700 }}
                            columns={[
                              { title: 'ID', dataIndex: 'id' },
                              { title: l('account'), dataIndex: 'account_id' },
                              { title: l('created'), dataIndex: 'created_at', render: date },
                              {
                                title: l('status'),
                                render: (_, r) => l(r.finished_at ? 'delivered' : 'pending'),
                              },
                              { title: l('reason'), dataIndex: 'last_error' },
                            ]}
                          />
                          <PageButtons
                            page={page}
                            count={runtime.data?.length || 0}
                            change={setPage}
                          />
                        </Space>
                      ),
                    },
                    {
                      key: 'settings',
                      label: l('settings'),
                      children: settings.data ? (
                        <SettingsEditor
                          key={JSON.stringify(settings.data)}
                          value={settings.data}
                          saved={() => void settings.refetch()}
                        />
                      ) : (
                        <Spin />
                      ),
                    },
                  ]}
                />
              )}
            </div>
          </Layout.Content>
        </Layout>
      </Layout>
    </ConfigProvider>
  );
}
