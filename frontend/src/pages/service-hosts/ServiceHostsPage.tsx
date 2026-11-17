import { useEffect, useMemo, useState } from 'react';
import { Link, useSearchParams } from 'react-router';
import { useTranslation } from 'react-i18next';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import {
  Alert,
  Button,
  Card,
  ConfigProvider,
  Descriptions,
  Empty,
  Form,
  Input,
  Layout,
  Select,
  Space,
  Spin,
  Switch,
  Table,
  Tabs,
  Tag,
  Typography,
  message,
} from 'antd';
import {
  ArrowLeftOutlined,
  CloudServerOutlined,
  PlusOutlined,
  ReloadOutlined,
} from '@ant-design/icons';
import type { z } from 'zod';

import AppSidebar from '@/layouts/AppSidebar';
import { selectTagRender } from '@/components/form/SelectTag';
import { useTheme } from '@/hooks/useTheme';
import { useNodesQuery } from '@/api/queries/useNodesQuery';
import { HttpUtil } from '@/utils';
import {
  ServiceHostDetailSchema,
  ServiceHostsSchema,
  HostRuntimesSchema,
  type ServiceHost,
  type HostLine,
  type HostRuntime,
  type EgressPolicy,
  type Connection,
} from '@/schemas/service-host';
import './ServiceHostsPage.css';

const API = '/panel/api/serviceHosts';
const JSON_OPTIONS = { headers: { 'Content-Type': 'application/json' }, silent: true };
const TAB_KEYS = ['services', 'lines', 'egress', 'connections', 'settings'];

async function read<T>(path: string, schema: z.ZodType<T>): Promise<T> {
  const msg = await HttpUtil.get(path, undefined, { silent: true });
  if (!msg?.success) throw new Error(msg?.msg || 'Host information unavailable');
  return schema.parse(msg.obj);
}

async function write(path: string, body: unknown): Promise<void> {
  const msg = await HttpUtil.post(path, body, JSON_OPTIONS);
  if (!msg?.success) throw new Error(msg?.msg || 'Host settings could not be saved');
}

export function connectionRows(reports: HostRuntime[]): Connection[] {
  return reports
    .flatMap((report) =>
      report.observer.records.map((record) => {
        const identity = /^([1-9][0-9]*)\./.exec(record.user)?.[1];
        return {
          ...record,
          nodeId: report.nodeId,
          service: report.name,
          assignedIPv6:
            record.outbound.startsWith('mz6-') && report.policy?.inbounds.includes(record.inbound)
              ? report.status.users.find((user) => user.identity === identity)?.address
              : undefined,
          allocationVerified: report.status.verified,
        };
      }),
    )
    .sort((a, b) => b.timestamp.localeCompare(a.timestamp));
}

export function ConnectionsPanel({ reports }: { reports: HostRuntime[] }) {
  const { t } = useTranslation();
  const label = (key: string) => t(`pages.serviceHosts.${key}`);
  const [user, setUser] = useState('');
  const [node, setNode] = useState<number>();
  const rows = useMemo(
    () =>
      connectionRows(reports).filter(
        (row) =>
          (node === undefined || row.nodeId === node) &&
          row.user.toLowerCase().includes(user.toLowerCase()),
      ),
    [reports, node, user],
  );
  return (
    <Space orientation="vertical" size="middle" className="service-host-full">
      <Alert
        type="info"
        showIcon
        title={label('recentDialTitle')}
        description={label('recentDialHelp')}
      />
      {reports
        .filter((r) => r.error || !r.observer.ready)
        .map((report) => (
          <Alert
            key={report.nodeId}
            type="warning"
            showIcon
            title={`${report.name}: ${label('observerMissing')}`}
            description={report.error || label('observerHelp')}
          />
        ))}
      <Space wrap>
        <Input
          aria-label={label('filterUser')}
          placeholder={label('filterUser')}
          value={user}
          onChange={(e) => setUser(e.target.value)}
          allowClear
        />
        <Select
          aria-label={label('filterService')}
          placeholder={label('filterService')}
          value={node}
          allowClear
          onChange={setNode}
          options={reports.map((r) => ({ value: r.nodeId, label: r.name }))}
          style={{ minWidth: 200 }}
        />
      </Space>
      <div
        className="service-host-table-scroll"
        role="region"
        /* oxlint-disable-next-line jsx-a11y/no-noninteractive-tabindex -- The named overflow region must receive keyboard focus so arrow keys can scroll the table. */
        tabIndex={0}
        aria-label={label('connections')}
      >
        <Table<Connection>
          rowKey={(r) => `${r.nodeId}:${r.id}`}
          dataSource={rows}
          pagination={{ pageSize: 20, showSizeChanger: false }}
          style={{ minWidth: 1500 }}
          locale={{ emptyText: <Typography.Text>{label('noDials')}</Typography.Text> }}
          columns={[
            {
              title: label('time'),
              dataIndex: 'timestamp',
              render: (v: string) => new Date(v).toLocaleString(),
            },
            { title: label('user'), dataIndex: 'user' },
            { title: label('service'), dataIndex: 'service' },
            {
              title: label('direction'),
              render: (_, r) => (
                <Tag>
                  {r.clientFamily} → {r.egressFamily}
                </Tag>
              ),
            },
            { title: label('clientAddress'), dataIndex: 'source' },
            { title: label('target'), dataIndex: 'target' },
            { title: label('actualRemote'), dataIndex: 'remote' },
            { title: label('socketSource'), dataIndex: 'local' },
            {
              title: label('assignedIPv6'),
              render: (_, r) =>
                r.assignedIPv6 ? (
                  <span>
                    {r.assignedIPv6} {!r.allocationVerified && <Tag>{label('unverified')}</Tag>}
                  </span>
                ) : (
                  '—'
                ),
            },
            { title: label('outbound'), dataIndex: 'outbound' },
          ]}
        />
      </div>
    </Space>
  );
}

function PolicyPanel({
  report,
  hostId,
  refresh,
}: {
  report: HostRuntime;
  hostId: number;
  refresh: () => void;
}) {
  const { t } = useTranslation();
  const label = (key: string) => t(`pages.serviceHosts.${key}`);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState('');
  const [form] = Form.useForm<EgressPolicy>();
  useEffect(() => {
    if (report.policy) form.setFieldsValue(report.policy);
  }, [report.policy, form]);
  const submit = async (values: EgressPolicy) => {
    if (!report.policy) return;
    setSaving(true);
    setError('');
    try {
      await write(`${API}/policy/${hostId}/${report.nodeId}`, { ...report.policy, ...values });
      refresh();
    } catch (e) {
      setError(e instanceof Error ? e.message : label('saveFailed'));
    } finally {
      setSaving(false);
    }
  };
  return (
    <Card title={report.name} size="small">
      {report.error ? (
        <Alert type="error" title={label('serviceUnavailable')} description={report.error} />
      ) : !report.installed || !report.policy ? (
        <Alert type="info" title={label('notInstalled')} description={label('installHelp')} />
      ) : (
        <Space orientation="vertical" size="middle" className="service-host-full">
          <Descriptions
            size="small"
            column={{ xs: 1, sm: 2 }}
            items={[
              { key: 'prefix', label: label('prefix'), children: report.policy.prefix },
              { key: 'interface', label: label('interface'), children: report.policy.interface },
              {
                key: 'state',
                label: label('verification'),
                children: (
                  <Tag color={report.status.verified ? 'success' : 'warning'}>
                    {label(report.status.verified ? 'verified' : 'unverified')}
                  </Tag>
                ),
              },
              {
                key: 'at',
                label: label('lastVerified'),
                children: report.status.updatedAt
                  ? new Date(report.status.updatedAt).toLocaleString()
                  : '—',
              },
            ]}
          />
          <Alert
            type="info"
            showIcon
            title={label('egressHelp')}
            description={label('sharedInboundHelp')}
          />
          {report.status.blockedUsers.length > 0 && (
            <Alert
              type="warning"
              title={label('blockedTitle')}
              description={report.status.blockedUsers.join(', ')}
            />
          )}
          {error && <Alert type="error" title={label('saveFailed')} description={error} />}
          <Form form={form} layout="vertical" onFinish={(values) => void submit(values)}>
            <Form.Item name="enabled" label={label('enablePolicy')} valuePropName="checked">
              <Switch />
            </Form.Item>
            <Form.Item
              name="inbounds"
              label={label('managedInbounds')}
              rules={[{ required: true, message: label('selectInbound') }]}
            >
              <Select
                mode="multiple"
                tagRender={selectTagRender}
                options={report.inbounds
                  .filter((i) =>
                    ['vless', 'vmess', 'trojan', 'shadowsocks', 'hysteria'].includes(i.protocol),
                  )
                  .map((i) => ({ value: i.tag, label: `${i.tag} · ${i.protocol}` }))}
              />
            </Form.Item>
            <Button htmlType="submit" type="primary" loading={saving}>
              {label('applyPolicy')}
            </Button>
            <Typography.Paragraph type="secondary" className="service-host-help">
              {label('restartHelp')}
            </Typography.Paragraph>
          </Form>
          <Table
            rowKey="identity"
            size="small"
            dataSource={report.status.users}
            pagination={{ pageSize: 10, showSizeChanger: false }}
            columns={[
              { title: label('permanentId'), dataIndex: 'identity' },
              { title: label('assignedIPv6'), dataIndex: 'address' },
            ]}
          />
        </Space>
      )}
    </Card>
  );
}

function HostSettings({
  host,
  initialNode,
  options,
  saved,
}: {
  host?: ServiceHost;
  initialNode?: number;
  options: { value: number; label: string; disabled?: boolean }[];
  saved: (id?: number) => void;
}) {
  const { t } = useTranslation();
  const label = (key: string) => t(`pages.serviceHosts.${key}`);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState('');
  const submit = async (values: { name: string; note: string; nodeIds: number[] }) => {
    setSaving(true);
    setError('');
    try {
      const msg = await HttpUtil.post<{ id: number }>(
        `${API}/save/${host?.id ?? 0}`,
        values,
        JSON_OPTIONS,
      );
      if (!msg?.success) throw new Error(msg?.msg || label('saveFailed'));
      saved(msg.obj?.id);
    } catch (e) {
      setError(e instanceof Error ? e.message : label('saveFailed'));
    } finally {
      setSaving(false);
    }
  };
  return (
    <Card title={label(host ? 'settings' : 'addHost')}>
      {error && <Alert type="error" title={label('saveFailed')} description={error} />}
      <Form
        layout="vertical"
        initialValues={{
          name: host?.name ?? '',
          note: host?.note ?? '',
          nodeIds:
            host?.services.map((n) => n.id) ?? (initialNode === undefined ? [] : [initialNode]),
        }}
        onFinish={(v) => void submit(v)}
      >
        <Form.Item name="name" label={label('hostName')} rules={[{ required: true }, { max: 128 }]}>
          <Input />
        </Form.Item>
        <Form.Item
          name="nodeIds"
          label={label('services')}
          rules={[{ required: true, message: label('selectService') }]}
          extra={label('groupingHelp')}
        >
          <Select mode="multiple" tagRender={selectTagRender} options={options} />
        </Form.Item>
        <Form.Item name="note" label={label('note')} rules={[{ max: 2000 }]}>
          <Input.TextArea rows={3} />
        </Form.Item>
        <Button type="primary" htmlType="submit" loading={saving}>
          {t('save')}
        </Button>
      </Form>
    </Card>
  );
}

export default function ServiceHostsPage() {
  const { t } = useTranslation();
  const label = (key: string) => t(`pages.serviceHosts.${key}`);
  const { isDark, isUltra, antdThemeConfig } = useTheme();
  const [params, setParams] = useSearchParams();
  const hostId = Number(params.get('id') || 0);
  const newHost = params.get('new') === '1';
  const initialNode = params.has('node') ? Number(params.get('node')) : undefined;
  const tab = TAB_KEYS.includes(params.get('tab') || '') ? params.get('tab')! : 'services';
  const client = useQueryClient();
  const [messages, messageHolder] = message.useMessage();
  const { nodes } = useNodesQuery();
  const hosts = useQuery({
    queryKey: ['serviceHosts'],
    queryFn: () => read(`${API}/list`, ServiceHostsSchema),
  });
  const detail = useQuery({
    queryKey: ['serviceHost', hostId],
    queryFn: () => read(`${API}/get/${hostId}`, ServiceHostDetailSchema),
    enabled: hostId > 0,
  });
  const runtime = useQuery({
    queryKey: ['hostRuntime', hostId],
    queryFn: () => read(`${API}/runtime/${hostId}`, HostRuntimesSchema),
    enabled: hostId > 0 && ['egress', 'connections'].includes(tab),
    refetchInterval: 10000,
  });
  useEffect(() => {
    if (initialNode === undefined || hostId || newHost || !hosts.data) return;
    const linked = hosts.data.find((host) =>
      host.services.some((instance) => instance.id === initialNode),
    );
    setParams(
      linked ? { id: String(linked.id), tab: 'services' } : { new: '1', node: String(initialNode) },
      { replace: true },
    );
  }, [hosts.data, initialNode, hostId, newHost, setParams]);
  const used = new Map(
    (hosts.data ?? []).flatMap((host) =>
      host.services.map((instance) => [instance.id, host.id] as const),
    ),
  );
  const options = [
    { value: 0, label: label('panelCore') },
    ...nodes
      .filter((node) => !node.transitive)
      .map((node) => ({ value: node.id, label: node.name || String(node.id) })),
  ].map((node) => ({ ...node, disabled: used.has(node.value) && used.get(node.value) !== hostId }));
  const saved = (id?: number) => {
    void client.invalidateQueries({ queryKey: ['serviceHosts'] });
    void client.invalidateQueries({ queryKey: ['serviceHost'] });
    void messages.success(t('save'));
    if (id) setParams({ id: String(id), tab: 'services' });
  };
  const reports = runtime.data ?? [];
  const requestError =
    hosts.error || detail.error || (['egress', 'connections'].includes(tab) ? runtime.error : null);
  const content =
    !hostId && !newHost ? (
      <Card
        title={label('title')}
        extra={
          <Button
            icon={<PlusOutlined aria-hidden />}
            type="primary"
            onClick={() => setParams({ new: '1' })}
          >
            {label('addHost')}
          </Button>
        }
      >
        <Typography.Paragraph type="secondary">{label('intro')}</Typography.Paragraph>
        <Table<ServiceHost>
          rowKey="id"
          loading={hosts.isFetching}
          dataSource={hosts.data ?? []}
          pagination={false}
          columns={[
            {
              title: label('hostName'),
              render: (_, host) => <Link to={`?id=${host.id}&tab=services`}>{host.name}</Link>,
            },
            {
              title: label('services'),
              render: (_, host) =>
                host.services.map((instance) => <Tag key={instance.id}>{instance.name}</Tag>),
            },
            { title: label('note'), dataIndex: 'note' },
          ]}
        />
      </Card>
    ) : newHost ? (
      <HostSettings key="new" initialNode={initialNode} options={options} saved={saved} />
    ) : !detail.data ? (
      <Spin aria-live="off" spinning={detail.isFetching}>
        {!detail.isFetching && <Empty />}
      </Spin>
    ) : (
      <>
        <Typography.Title level={2}>{detail.data.name}</Typography.Title>
        {detail.data.note && (
          <Typography.Paragraph type="secondary">{detail.data.note}</Typography.Paragraph>
        )}
        <Tabs
          activeKey={tab}
          onChange={(key) => setParams({ id: String(hostId), tab: key })}
          items={TAB_KEYS.map((key) => ({
            key,
            label: label(key),
            children:
              key === 'settings' ? (
                <HostSettings key={hostId} host={detail.data} options={options} saved={saved} />
              ) : key === 'services' ? (
                <Card>
                  <Table
                    rowKey="id"
                    dataSource={detail.data.services}
                    pagination={false}
                    columns={[
                      { title: label('service'), dataIndex: 'name' },
                      { title: label('controlAddress'), dataIndex: 'address' },
                      { title: label('status'), dataIndex: 'status' },
                      { title: label('coreStatus'), dataIndex: 'xrayState' },
                    ]}
                  />
                </Card>
              ) : key === 'lines' ? (
                <Card>
                  <Alert
                    type="info"
                    showIcon
                    title={label('lineHelp')}
                    description={label('classificationHelp')}
                    className="service-host-help"
                  />
                  <div
                    className="service-host-table-scroll"
                    role="region"
                    /* oxlint-disable-next-line jsx-a11y/no-noninteractive-tabindex -- The named overflow region must receive keyboard focus so arrow keys can scroll the table. */
                    tabIndex={0}
                    aria-label={label('lines')}
                  >
                    <Table<HostLine>
                      rowKey="id"
                      dataSource={detail.data.lines}
                      pagination={false}
                      style={{ minWidth: 900 }}
                      columns={[
                        {
                          title: label('line'),
                          render: (_, line) => (
                            <Space orientation="vertical" size={0}>
                              <span>{line.name || line.tag}</span>
                              <Typography.Text type="secondary">{line.tag}</Typography.Text>
                            </Space>
                          ),
                        },
                        {
                          title: label('service'),
                          render: (_, line) =>
                            detail.data?.services.find((s) => s.id === line.nodeId)?.name,
                        },
                        {
                          title: t('protocol'),
                          render: (_, line) => `${line.protocol} / ${line.transport || 'tcp'}`,
                        },
                        {
                          title: label('entries'),
                          render: (_, line) =>
                            line.endpoints.length ? (
                              <Space orientation="vertical" size={4}>
                                {line.endpoints.map((entry) => (
                                  <span key={entry.id}>
                                    <Tag>{entry.family}</Tag>
                                    {entry.name} ·{' '}
                                    {entry.family === 'IPv6'
                                      ? `[${entry.address.replace(/^\[|\]$/g, '')}]`
                                      : entry.address}
                                    :{entry.port}
                                    {entry.disabled && <Tag>{label('disabled')}</Tag>}
                                  </span>
                                ))}
                              </Space>
                            ) : (
                              label('noEntry')
                            ),
                        },
                        {
                          title: label('declaredMode'),
                          render: (_, line) => <Tag>{label(line.egressMode)}</Tag>,
                        },
                      ]}
                    />
                  </div>
                </Card>
              ) : key === 'egress' ? (
                <Spin aria-live="off" spinning={runtime.isFetching}>
                  <Space orientation="vertical" size="middle" className="service-host-full">
                    {reports.map((report) => (
                      <PolicyPanel
                        key={report.nodeId}
                        report={report}
                        hostId={hostId}
                        refresh={() => {
                          void runtime.refetch();
                        }}
                      />
                    ))}
                  </Space>
                </Spin>
              ) : (
                <Spin aria-live="off" spinning={runtime.isFetching}>
                  <ConnectionsPanel reports={reports} />
                </Spin>
              ),
          }))}
        />
      </>
    );
  return (
    <ConfigProvider theme={antdThemeConfig}>
      {messageHolder}
      <Layout className={`page-layout${isDark ? ' dark' : ''}${isUltra ? ' ultra' : ''}`}>
        <AppSidebar />
        <Layout className="content-shell">
          <Layout.Content className="content-area service-host-content" id="content-layout">
            <Space className="service-host-toolbar" wrap>
              <CloudServerOutlined aria-hidden />
              {(hostId > 0 || newHost) && (
                <Button icon={<ArrowLeftOutlined aria-hidden />} onClick={() => setParams({})}>
                  {label('back')}
                </Button>
              )}
              <Button
                icon={<ReloadOutlined aria-hidden />}
                loading={hosts.isFetching || detail.isFetching || runtime.isFetching}
                onClick={() => {
                  void hosts.refetch();
                  if (hostId > 0) {
                    void detail.refetch();
                    if (['egress', 'connections'].includes(tab)) void runtime.refetch();
                  }
                }}
              >
                {t('refresh')}
              </Button>
            </Space>
            {requestError && (
              <Alert
                type="error"
                showIcon
                title={label('loadFailed')}
                description={
                  requestError instanceof Error ? requestError.message : label('serviceUnavailable')
                }
                className="service-host-help"
              />
            )}
            {content}
          </Layout.Content>
        </Layout>
      </Layout>
    </ConfigProvider>
  );
}
