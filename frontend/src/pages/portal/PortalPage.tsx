import { useState, useSyncExternalStore } from 'react';
import { useTranslation } from 'react-i18next';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import {
  Alert,
  Button,
  Card,
  ConfigProvider,
  Empty,
  Form,
  Input,
  Popconfirm,
  Progress,
  Space,
  Spin,
  Statistic,
  Table,
  Tabs,
  Tag,
  Typography,
} from 'antd';
import { z } from 'zod';
import { useTheme } from '@/hooks/useTheme';
import {
  portalRequest,
  PortalRequestError,
  readPurchaseIntent,
  savePurchaseIntent,
  emptyResponse,
  type PurchaseIntent,
} from '@/api/commerce';
import {
  AccountSchema,
  PlanSchema,
  WalletSchema,
  IssuedSchema,
  type Account,
} from '@/schemas/commerce';
import MGMAControls from '@/pages/commerce/MGMAControls';
import ExternalSources, { useSubscriptionLabel } from '@/pages/subscriptions/ExternalSources';
import { money, date, bytes } from '@/pages/commerce/commerceFormat';
import '@/pages/commerce/Commerce.css';

const SESSION_KEY = 'xui.portal.session';
export default function PortalPage() {
  const { t } = useTranslation();
  const sl = useSubscriptionLabel();
  const l = (key: string) => t(`pages.commerce.${key}`);
  const { antdThemeConfig } = useTheme();
  const qc = useQueryClient();
  const [token, setToken] = useState(() => sessionStorage.getItem(SESSION_KEY) || '');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const [page, setPage] = useState(1);
  const config = useQuery({
    queryKey: ['portal', 'config'],
    queryFn: () => portalRequest('config', z.object({ enabled: z.boolean() })),
    retry: false,
  });
  const me = useQuery({
    queryKey: ['portal', token, 'me'],
    queryFn: async () => {
      try {
        return await portalRequest('me', AccountSchema, token);
      } catch (e) {
        if (e instanceof PortalRequestError && e.status === 401) logout();
        throw e;
      }
    },
    enabled: !!token,
    retry: false,
    refetchInterval: 15000,
  });
  const plans = useQuery({
    queryKey: ['portal', token, 'plans'],
    queryFn: () => portalRequest('plans', z.array(PlanSchema), token),
    enabled: !!token,
    retry: false,
  });
  const ledger = useQuery({
    queryKey: ['portal', token, 'ledger', page],
    queryFn: () => portalRequest(`wallet/transactions?page=${page}`, z.array(WalletSchema), token),
    enabled: !!token,
    retry: false,
  });
  const pendingJSON = useSyncExternalStore(
    (notify) => {
      window.addEventListener('xui-purchase-intent', notify);
      return () => window.removeEventListener('xui-purchase-intent', notify);
    },
    () => (me.data ? sessionStorage.getItem(`xui.purchase.${me.data.id}`) : null),
  );
  const pending: PurchaseIntent | null =
    pendingJSON && me.data ? readPurchaseIntent(me.data.id) : null;
  function logout() {
    sessionStorage.removeItem(SESSION_KEY);
    setToken('');
    qc.removeQueries({ queryKey: ['portal'] });
  }
  async function auth(
    values: { username: string; password: string; invitation_code?: string },
    register = false,
  ) {
    setBusy(true);
    setError('');
    try {
      if (register) await portalRequest('register', AccountSchema, '', values);
      const result = await portalRequest('token', z.object({ access_token: z.string() }), '', {
        username: values.username,
        password: values.password,
      });
      sessionStorage.setItem(SESSION_KEY, result.access_token);
      setToken(result.access_token);
    } catch (e) {
      setError(
        e instanceof PortalRequestError && e.status === 429 ? l('throttled') : l('authFailed'),
      );
    } finally {
      setBusy(false);
    }
  }
  async function buy(account: Account, planID: number) {
    const intent = pending || { plan_id: planID, key: crypto.randomUUID() };
    if (intent.plan_id !== planID) return;
    savePurchaseIntent(account.id, intent);
    setBusy(true);
    setError('');
    try {
      await portalRequest(
        'purchase',
        AccountSchema,
        token,
        { plan_id: planID },
        'POST',
        intent.key,
      );
      savePurchaseIntent(account.id, null);
      await Promise.all([me.refetch(), ledger.refetch()]);
    } catch (e) {
      if (e instanceof PortalRequestError && [400, 401, 402, 404, 409].includes(e.status)) {
        savePurchaseIntent(account.id, null);
      }
      setError(
        e instanceof PortalRequestError && e.status === 402
          ? l('insufficient')
          : l('purchaseFailed'),
      );
    } finally {
      setBusy(false);
    }
  }
  const a = me.data;
  return (
    <ConfigProvider theme={antdThemeConfig}>
      <main className="commerce-portal">
        {!token ? (
          <div className="commerce-login">
            <Typography.Title level={1}>{l('portalTitle')}</Typography.Title>
            {config.isPending ? (
              <Spin />
            ) : config.error ? (
              <Alert
                type="error"
                showIcon
                title={l('failed')}
                action={<Button onClick={() => void config.refetch()}>{l('refresh')}</Button>}
              />
            ) : !config.data?.enabled ? (
              <Alert type="info" showIcon title={l('portalClosed')} />
            ) : (
              <Card>
                {error && <Alert type="error" showIcon title={error} />}
                <Tabs
                  items={['login', 'register'].map((kind) => ({
                    key: kind,
                    label: l(kind),
                    children: (
                      <Form layout="vertical" onFinish={(v) => void auth(v, kind === 'register')}>
                        <Form.Item
                          label={l('username')}
                          name="username"
                          rules={[{ required: true }]}
                        >
                          <Input autoComplete="username" maxLength={32} />
                        </Form.Item>
                        <Form.Item
                          label={l('password')}
                          name="password"
                          rules={[
                            { required: true },
                            ...(kind === 'register'
                              ? [{ min: 10, message: l('passwordHelp') }]
                              : []),
                          ]}
                          extra={kind === 'register' ? l('passwordHelp') : undefined}
                        >
                          <Input.Password
                            autoComplete={kind === 'register' ? 'new-password' : 'current-password'}
                            maxLength={512}
                          />
                        </Form.Item>
                        {kind === 'register' && (
                          <Form.Item
                            label={l('invitationCode')}
                            name="invitation_code"
                            rules={[{ required: true }]}
                          >
                            <Input autoComplete="off" />
                          </Form.Item>
                        )}
                        <Button type="primary" htmlType="submit" loading={busy} block>
                          {l(kind)}
                        </Button>
                      </Form>
                    ),
                  }))}
                />
              </Card>
            )}
          </div>
        ) : (
          <div className="commerce-page commerce-stack">
            <header className="commerce-toolbar">
              <Typography.Title level={1}>{l('portalTitle')}</Typography.Title>
              <Space wrap>
                <Typography.Text>{a?.username}</Typography.Text>
                <Button
                  onClick={() => {
                    void me.refetch();
                    void plans.refetch();
                    void ledger.refetch();
                  }}
                >
                  {l('refresh')}
                </Button>
                <Button onClick={logout}>{l('logout')}</Button>
              </Space>
            </header>
            {(error || me.error || plans.error || ledger.error) && (
              <Alert type="error" showIcon title={error || l('failed')} />
            )}
            {!a ? (
              <Spin />
            ) : (
              <>
                <div className="commerce-grid">
                  <Card>
                    <Statistic title={l('balance')} value={money(a.wallet_balance_minor)} />
                    <Typography.Paragraph>{l('rechargeHelp')}</Typography.Paragraph>
                  </Card>
                  <Card title={l('currentPlan')}>
                    <Typography.Title level={2}>
                      {a.subscription?.plan_name || l('not_activated')}
                    </Typography.Title>
                    <Tag>{l(a.usage.status)}</Tag>
                    <Typography.Paragraph>
                      {l('expires')}: {date(a.usage.expire)}
                    </Typography.Paragraph>
                  </Card>
                  <Card title={l('usage')}>
                    <Typography.Paragraph>
                      {bytes(a.usage.used_traffic)} /{' '}
                      {a.usage.data_limit ? bytes(a.usage.data_limit) : l('unlimited')}
                    </Typography.Paragraph>
                    {a.usage.data_limit > 0 && (
                      <Progress
                        aria-label={l('usage')}
                        percent={Math.min(
                          100,
                          Math.round((a.usage.used_traffic / a.usage.data_limit) * 100),
                        )}
                      />
                    )}
                    <Typography.Paragraph>
                      {l('lifetime')}: {bytes(a.usage.lifetime_used_traffic)}
                    </Typography.Paragraph>
                  </Card>
                </div>
                {a.runtime_pending && <Alert type="info" showIcon title={l('pendingRuntime')} />}
                {pending && (
                  <Alert
                    type="warning"
                    showIcon
                    title={l('unresolvedPurchase')}
                    action={
                      <Button
                        aria-label={l('retryPurchase')}
                        loading={busy}
                        onClick={() => void buy(a, pending.plan_id)}
                      >
                        {l('retryPurchase')}
                      </Button>
                    }
                  />
                )}
                <Tabs
                  defaultActiveKey="plans"
                  destroyOnHidden
                  items={[
                    {
                      key: 'external',
                      label: sl('mySources'),
                      children: <ExternalSources token={token} />,
                    },
                    {
                      key: 'plans',
                      label: l('plans'),
                      children: (
                        <div className="commerce-grid">
                          {plans.data?.length ? (
                            plans.data.map((p) => (
                              <Card key={p.id} title={p.name}>
                                <Typography.Paragraph>{p.description}</Typography.Paragraph>
                                <Statistic value={money(p.price_minor)} />
                                <Typography.Paragraph>
                                  {t('pages.commerce.planTerms', {
                                    days: p.duration_days,
                                    traffic: p.data_limit ? bytes(p.data_limit) : l('unlimited'),
                                  })}
                                </Typography.Paragraph>
                                <Popconfirm
                                  title={l('purchaseConfirm')}
                                  onConfirm={() => buy(a, p.id)}
                                  okText={l('confirm')}
                                  cancelText={l('cancel')}
                                >
                                  <Button
                                    type="primary"
                                    disabled={
                                      !!pending ||
                                      busy ||
                                      p.compatibility_pending ||
                                      p.price_minor > a.wallet_balance_minor
                                    }
                                  >
                                    {l('purchase')}
                                  </Button>
                                </Popconfirm>
                              </Card>
                            ))
                          ) : (
                            <Empty description={l('noPlans')} />
                          )}
                        </div>
                      ),
                    },
                    {
                      key: 'subscription',
                      label: l('subscriptionLink'),
                      children: (
                        <MGMAControls
                          disabled={!a.client_id}
                          issue={() => portalRequest('mgma', IssuedSchema, token, {})}
                          revoke={() =>
                            portalRequest('mgma', emptyResponse, token, undefined, 'DELETE')
                          }
                          regenerate={async () => {
                            await portalRequest(
                              'subscription/regenerate',
                              emptyResponse,
                              token,
                              {},
                            );
                            await me.refetch();
                          }}
                        />
                      ),
                    },
                    {
                      key: 'wallet',
                      label: l('wallet'),
                      children: (
                        <Space orientation="vertical" className="commerce-full">
                          <Table
                            rowKey="id"
                            dataSource={ledger.data || []}
                            pagination={false}
                            scroll={{ x: 650 }}
                            columns={[
                              { title: l('created'), dataIndex: 'created_at', render: date },
                              {
                                title: l('operation'),
                                dataIndex: 'kind',
                                render: (v: string) => l(v),
                              },
                              { title: l('amount'), dataIndex: 'amount_minor', render: money },
                              {
                                title: l('balance'),
                                dataIndex: 'balance_after_minor',
                                render: money,
                              },
                              { title: l('note'), dataIndex: 'note' },
                            ]}
                          />
                          <Space>
                            <Button disabled={page === 1} onClick={() => setPage(page - 1)}>
                              {l('previous')}
                            </Button>
                            <span>{page}</span>
                            <Button
                              disabled={(ledger.data?.length || 0) < 100}
                              onClick={() => setPage(page + 1)}
                            >
                              {l('next')}
                            </Button>
                          </Space>
                        </Space>
                      ),
                    },
                    {
                      key: 'password',
                      label: l('changePassword'),
                      children: (
                        <Card title={l('changePassword')}>
                          <Form
                            layout="vertical"
                            onFinish={async (v) => {
                              setBusy(true);
                              try {
                                await portalRequest('password/change', emptyResponse, token, v);
                                logout();
                              } catch {
                                setError(l('failed'));
                              } finally {
                                setBusy(false);
                              }
                            }}
                          >
                            <Form.Item
                              label={l('currentPassword')}
                              name="current_password"
                              rules={[{ required: true }]}
                            >
                              <Input.Password autoComplete="current-password" />
                            </Form.Item>
                            <Form.Item
                              label={l('newPassword')}
                              name="new_password"
                              rules={[{ required: true }, { min: 10, message: l('passwordHelp') }]}
                              extra={l('passwordHelp')}
                            >
                              <Input.Password autoComplete="new-password" maxLength={72} />
                            </Form.Item>
                            <Button type="primary" htmlType="submit" loading={busy}>
                              {l('save')}
                            </Button>
                          </Form>
                        </Card>
                      ),
                    },
                  ]}
                />
              </>
            )}
          </div>
        )}
      </main>
    </ConfigProvider>
  );
}
