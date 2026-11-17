import dayjs from 'dayjs';
import { useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import { z } from 'zod';
import {
  Alert,
  Button,
  Card,
  DatePicker,
  Descriptions,
  Empty,
  Form,
  Input,
  InputNumber,
  Popconfirm,
  Select,
  Space,
  Spin,
  Switch,
  Table,
  Tabs,
  Tag,
  Typography,
} from 'antd';
import { HttpError } from '@/api/http-init';
import { commerceRequest as request, emptyResponse } from '@/api/commerce';
import {
  AccountSchema,
  PurchaseSchema,
  WalletSchema,
  IssuedSchema,
  NextPlanSchema,
  type Plan,
} from '@/schemas/commerce';
import MGMAControls from './MGMAControls';
import { useLabel } from './CommerceForms';
import { AccountClientLink, SecretNotice, PageButtons } from './CommerceShared';
import { money, date, bytes } from './commerceFormat';
type Intent = { path: string; body: Record<string, unknown>; key: string };
export default function AccountDetail({
  id,
  plans,
  back,
}: {
  id: number;
  plans: Plan[];
  back: () => void;
}) {
  const l = useLabel();
  const [tab, setTab] = useState('summary');
  const [error, setError] = useState(false);
  const [busy, setBusy] = useState(false);
  const [secret, setSecret] = useState('');
  const [page, setPage] = useState(1);
  const [form] = Form.useForm();
  const a = useQuery({
    queryKey: ['commerce', 'account', id],
    queryFn: () => request(`accounts/${id}`, AccountSchema),
    refetchInterval: 15000,
    retry: false,
  });
  const wallet = useQuery({
    queryKey: ['commerce', id, 'wallet', page],
    queryFn: () => request(`accounts/${id}/wallet?page=${page}`, z.array(WalletSchema)),
    enabled: tab === 'wallet',
  });
  const purchases = useQuery({
    queryKey: ['commerce', id, 'purchases', page],
    queryFn: () => request(`accounts/${id}/purchases?page=${page}`, z.array(PurchaseSchema)),
    enabled: tab === 'purchases',
  });
  const clientID = a.data?.client_id;
  const queued = useQuery({
    queryKey: ['commerce', id, 'nextPlan', clientID],
    queryFn: () => request(`clients/${clientID}/next-plan`, NextPlanSchema),
    enabled: !!clientID && tab === 'nextPlan',
    retry: false,
  });
  const storageKey = `xui.commerce.pending.${id}`;
  const [intent, setIntent] = useState<Intent | null>(() => {
    try {
      return z
        .object({
          path: z.string(),
          body: z.record(z.string(), z.unknown()),
          key: z.string().uuid(),
        })
        .nullable()
        .parse(JSON.parse(sessionStorage.getItem(storageKey) || 'null'));
    } catch {
      return null;
    }
  });
  async function operation(work: () => Promise<unknown>) {
    setBusy(true);
    setError(false);
    try {
      await work();
      await a.refetch();
    } catch {
      setError(true);
    } finally {
      setBusy(false);
    }
  }
  async function financial(next: Intent) {
    const current = intent || next;
    setIntent(current);
    sessionStorage.setItem(storageKey, JSON.stringify(current));
    await operation(async () => {
      try {
        await request(current.path, emptyResponse, current.body, 'POST', current.key);
        sessionStorage.removeItem(storageKey);
        setIntent(null);
      } catch (e) {
        if (e instanceof HttpError && [400, 402, 404, 409].includes(e.status)) {
          sessionStorage.removeItem(storageKey);
          setIntent(null);
        }
        throw e;
      }
    });
  }
  const account = a.data;
  return (
    <Space orientation="vertical" size="large" className="commerce-full">
      <Button onClick={back}>{l('back')}</Button>
      {(a.error || error || wallet.error || purchases.error) && (
        <Alert type="error" showIcon title={l('failed')} />
      )}
      {!account ? (
        <Spin />
      ) : (
        <>
          <Typography.Title level={2}>
            {account.username} <Tag>#{account.id}</Tag>
          </Typography.Title>
          <SecretNotice secret={secret} clear={() => setSecret('')} />
          {intent && (
            <Alert
              type="warning"
              showIcon
              title={l('unresolvedPurchase')}
              action={
                <Button loading={busy} onClick={() => void financial(intent)}>
                  {l('retryPurchase')}
                </Button>
              }
            />
          )}
          {account.runtime_pending && <Alert type="info" showIcon title={l('pendingRuntime')} />}
          <Tabs
            activeKey={tab}
            onChange={(key) => {
              setTab(key);
              setPage(1);
              setSecret('');
            }}
            destroyOnHidden
            items={[
              {
                key: 'summary',
                label: l('account'),
                children: (
                  <div className="commerce-stack">
                    <Card>
                      <Descriptions
                        column={{ xs: 1, sm: 2 }}
                        items={[
                          {
                            key: 'balance',
                            label: l('balance'),
                            children: money(account.wallet_balance_minor),
                          },
                          {
                            key: 'user',
                            label: l('permanentID'),
                            children: account.user_id || '—',
                          },
                          {
                            key: 'client',
                            label: l('client'),
                            children: <AccountClientLink clientID={account.client_id} />,
                          },
                          {
                            key: 'plan',
                            label: l('currentPlan'),
                            children: account.subscription?.plan_name || l('not_activated'),
                          },
                          { key: 'status', label: l('status'), children: l(account.usage.status) },
                          {
                            key: 'expiry',
                            label: l('expires'),
                            children: date(account.usage.expire),
                          },
                          {
                            key: 'usage',
                            label: l('usage'),
                            children: `${bytes(account.usage.used_traffic)} / ${account.usage.data_limit ? bytes(account.usage.data_limit) : l('unlimited')}`,
                          },
                        ]}
                      />
                      <Space wrap>
                        <Popconfirm
                          title={l('accountToggleConfirm')}
                          onConfirm={() =>
                            operation(() =>
                              request(`accounts/${id}/enabled`, emptyResponse, {
                                enabled: !account.is_active,
                              }),
                            )
                          }
                        >
                          <Button loading={busy}>
                            {account.is_active ? l('disableAccount') : l('enableAccount')}
                          </Button>
                        </Popconfirm>
                        <Popconfirm
                          title={l('resetPasswordConfirm')}
                          onConfirm={() =>
                            operation(async () => {
                              const v = await request(
                                `accounts/${id}/reset-password`,
                                z.object({ password: z.string() }),
                                {},
                              );
                              setSecret(v.password);
                            })
                          }
                        >
                          <Button disabled={busy}>{l('resetPassword')}</Button>
                        </Popconfirm>
                      </Space>
                    </Card>
                    <Card title={l('transactions')}>
                      <Alert type="info" title={l('financialHelp')} showIcon />
                      <Form
                        form={form}
                        layout="vertical"
                        disabled={busy || !!intent}
                        initialValues={{ operation: 'credit', amount_minor: 1000, days: 30 }}
                        onFinish={(v) => {
                          const body =
                            v.operation === 'credit'
                              ? { amount_minor: v.amount_minor, note: v.note || '' }
                              : v.operation === 'grant'
                                ? { plan_id: v.plan_id }
                                : v.operation === 'renew'
                                  ? { days: v.days }
                                  : {};
                          void financial({
                            path: `accounts/${id}/${v.operation}`,
                            body,
                            key: crypto.randomUUID(),
                          });
                        }}
                      >
                        <Form.Item name="operation" label={l('operation')}>
                          <Select
                            options={['credit', 'grant', 'renew', 'disable'].map((v) => ({
                              value: v,
                              label: l(v),
                            }))}
                          />
                        </Form.Item>
                        <Form.Item noStyle shouldUpdate={(p, n) => p.operation !== n.operation}>
                          {({ getFieldValue }) =>
                            getFieldValue('operation') === 'credit' ? (
                              <>
                                <Form.Item
                                  name="amount_minor"
                                  label={l('creditCents')}
                                  rules={[{ required: true }]}
                                >
                                  <InputNumber min={1} max={1e12} precision={0} />
                                </Form.Item>
                                <Form.Item name="note" label={l('note')}>
                                  <Input maxLength={500} />
                                </Form.Item>
                              </>
                            ) : getFieldValue('operation') === 'grant' ? (
                              <Form.Item
                                name="plan_id"
                                label={l('plans')}
                                rules={[{ required: true }]}
                              >
                                <Select
                                  options={plans
                                    .filter((p) => !p.compatibility_pending)
                                    .map((p) => ({ value: p.id, label: p.name }))}
                                />
                              </Form.Item>
                            ) : getFieldValue('operation') === 'renew' ? (
                              <Form.Item name="days" label={l('days')} rules={[{ required: true }]}>
                                <InputNumber min={1} max={3650} precision={0} />
                              </Form.Item>
                            ) : (
                              <Alert type="warning" title={l('disableGrantHelp')} showIcon />
                            )
                          }
                        </Form.Item>
                        <Form.Item>
                          <Popconfirm
                            title={l('transactionConfirm')}
                            onConfirm={() => form.submit()}
                          >
                            <Button type="primary" loading={busy}>
                              {l('apply')}
                            </Button>
                          </Popconfirm>
                        </Form.Item>
                      </Form>
                    </Card>
                  </div>
                ),
              },
              {
                key: 'mgma',
                label: l('subscriptionLink'),
                children: (
                  <MGMAControls
                    disabled={!clientID}
                    issue={() => request(`clients/${clientID}/mgma`, IssuedSchema, {})}
                    revoke={() =>
                      request(`clients/${clientID}/mgma`, emptyResponse, undefined, 'DELETE')
                    }
                    regenerate={async () => {
                      await request(`clients/${clientID}/regenerate`, emptyResponse, {});
                      await a.refetch();
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
                      loading={wallet.isFetching}
                      rowKey="id"
                      dataSource={wallet.data || []}
                      pagination={false}
                      scroll={{ x: 700 }}
                      columns={[
                        { title: l('created'), dataIndex: 'created_at', render: date },
                        { title: l('operation'), dataIndex: 'kind', render: (v: string) => l(v) },
                        { title: l('amount'), dataIndex: 'amount_minor', render: money },
                        { title: l('balance'), dataIndex: 'balance_after_minor', render: money },
                        { title: l('note'), dataIndex: 'note' },
                      ]}
                    />
                    <PageButtons page={page} count={wallet.data?.length || 0} change={setPage} />
                  </Space>
                ),
              },
              {
                key: 'purchases',
                label: l('purchases'),
                children: (
                  <Space orientation="vertical" className="commerce-full">
                    <Table
                      loading={purchases.isFetching}
                      rowKey="id"
                      dataSource={purchases.data || []}
                      pagination={false}
                      scroll={{ x: 700 }}
                      columns={[
                        { title: l('created'), dataIndex: 'created_at', render: date },
                        { title: l('operation'), dataIndex: 'kind', render: (v: string) => l(v) },
                        { title: l('currentPlan'), dataIndex: 'plan_name' },
                        { title: l('amount'), dataIndex: 'amount_minor', render: money },
                        { title: l('expires'), dataIndex: 'effective_expires_at', render: date },
                      ]}
                    />
                    <PageButtons page={page} count={purchases.data?.length || 0} change={setPage} />
                  </Space>
                ),
              },
              {
                key: 'nextPlan',
                label: l('nextPlan'),
                children: !clientID ? (
                  <Empty />
                ) : queued.isFetching ? (
                  <Spin />
                ) : (
                  <Card title={l('nextPlan')}>
                    <Alert type="info" title={l('nextPlanHelp')} showIcon />
                    {queued.data?.last_error && <Alert type="error" title={l('queuedFailed')} />}
                    <Form
                      layout="vertical"
                      key={JSON.stringify(queued.data || {})}
                      initialValues={{
                        ...(queued.data || {
                          data_limit: 0,
                          add_remaining_traffic: true,
                          fire_on_either: true,
                          paused: false,
                        }),
                        expiry_date: queued.data?.expire ? dayjs.unix(queued.data.expire) : null,
                      }}
                      onFinish={(v) =>
                        void operation(async () => {
                          const { expiry_date, ...fields } = v;
                          await request(
                            `clients/${clientID}/next-plan`,
                            emptyResponse,
                            { ...fields, expire: expiry_date?.unix() || 0 },
                            'PUT',
                          );
                          await queued.refetch();
                        })
                      }
                    >
                      <Form.Item
                        name="data_limit"
                        label={l('dataBytes')}
                        rules={[{ required: true }]}
                      >
                        <InputNumber min={0} max={Number.MAX_SAFE_INTEGER} precision={0} />
                      </Form.Item>
                      <Form.Item
                        name="expiry_date"
                        label={l('absoluteExpirySeconds')}
                        extra={l('zeroPermanent')}
                      >
                        <DatePicker showTime />
                      </Form.Item>
                      <Form.Item
                        name="add_remaining_traffic"
                        label={l('discardRemainder')}
                        valuePropName="checked"
                      >
                        <Switch />
                      </Form.Item>
                      <Form.Item
                        name="fire_on_either"
                        label={l('fireOnEither')}
                        valuePropName="checked"
                      >
                        <Switch />
                      </Form.Item>
                      <Form.Item name="paused" label={l('paused')} valuePropName="checked">
                        <Switch />
                      </Form.Item>
                      <Space>
                        <Button type="primary" htmlType="submit" loading={busy}>
                          {l('save')}
                        </Button>
                        <Button
                          danger
                          disabled={!queued.data || busy}
                          onClick={() =>
                            void operation(async () => {
                              await request(
                                `clients/${clientID}/next-plan`,
                                emptyResponse,
                                undefined,
                                'DELETE',
                              );
                              await queued.refetch();
                            })
                          }
                        >
                          {l('remove')}
                        </Button>
                      </Space>
                    </Form>
                  </Card>
                ),
              },
            ]}
          />
        </>
      )}
    </Space>
  );
}
