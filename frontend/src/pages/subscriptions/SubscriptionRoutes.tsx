import { useState } from 'react';
import { selectTagRender } from '@/components/form/SelectTag';
import { useQuery } from '@tanstack/react-query';
import {
  Alert,
  Button,
  Card,
  Collapse,
  Form,
  Input,
  InputNumber,
  Select,
  Space,
  Switch,
  Table,
  Tag,
  Typography,
} from 'antd';
import { z } from 'zod';
import { commerceRequest as request } from '@/api/commerce';
import { RouteSchema, type Route } from '@/schemas/subscriptionCompat';
import { SubscriptionHostSchema, EntryChoiceSchema } from '@/generated/zod';
import { PageButtons } from '@/pages/commerce/CommerceShared';
import { useSubscriptionLabel } from './ExternalSources';

const ObjectSchema = z.record(z.string(), z.unknown());
function obj(value: unknown): Record<string, unknown> {
  return ObjectSchema.safeParse(value).data ?? {};
}
function parseStream(raw: string) {
  try {
    return obj(JSON.parse(raw || '{}'));
  } catch {
    return {};
  }
}
function legFields(raw: Record<string, unknown>) {
  const security = typeof raw.security === 'string' ? raw.security : 'none';
  const tls = obj(raw[`${security}Settings`]);
  const transport = obj(raw.xhttpSettings || raw.splithttpSettings);
  const effective = transport.extra ? obj(transport.extra) : transport;
  const reuse = obj(effective.xmux);
  return {
    address: raw.address,
    port: raw.port || 443,
    security,
    serverName: tls.serverName || '',
    fingerprint: tls.fingerprint || 'chrome',
    alpn: tls.alpn || ['h2'],
    publicKey: tls.publicKey || tls.password || '',
    shortId: tls.shortId || '',
    allowInsecure: tls.allowInsecure === true,
    host: transport.host || '',
    maxConcurrency: reuse.maxConcurrency || '',
    maxConnections: reuse.maxConnections || '',
    hMaxReusableSecs: reuse.hMaxReusableSecs || '',
    hKeepAlivePeriod: reuse.hKeepAlivePeriod,
  };
}
function buildConnection(
  fields: Record<string, unknown>,
  previous: Record<string, unknown>,
  path: string,
): Record<string, unknown> {
  const security = fields.security || 'none';
  const next: Record<string, unknown> = {
    ...previous,
    address: fields.address,
    port: fields.port,
    network: 'xhttp',
    security,
  };
  delete next.tlsSettings;
  delete next.realitySettings;
  delete next.splithttpSettings;
  if (security !== 'none')
    next[`${String(security)}Settings`] = {
      serverName: fields.serverName,
      fingerprint: fields.fingerprint || 'chrome',
      alpn: fields.alpn || ['h2'],
      allowInsecure: fields.allowInsecure === true,
      ...(security === 'reality'
        ? { publicKey: fields.publicKey, shortId: fields.shortId || '' }
        : {}),
    };
  next.xhttpSettings = {
    ...obj(previous.xhttpSettings || previous.splithttpSettings),
    path,
    host: fields.host || '',
  };
  const tr = obj(next.xhttpSettings);
  const effective = tr.extra ? obj(tr.extra) : tr;
  const reuse = { ...obj(effective.xmux) };
  for (const key of ['maxConcurrency', 'maxConnections', 'hMaxReusableSecs', 'hKeepAlivePeriod']) {
    if (fields[key] !== undefined && fields[key] !== '') reuse[key] = fields[key];
    else delete reuse[key];
  }
  if (Object.keys(reuse).length) effective.xmux = reuse;
  else delete effective.xmux;
  return next;
}
export default function SubscriptionRoutes({
  selected,
  select,
}: {
  selected: number;
  select: (id: number) => void;
}) {
  const l = useSubscriptionLabel();
  const [page, setPage] = useState(1);
  const [search, setSearch] = useState('');
  const rows = useQuery({
    queryKey: ['subscriptionRoutes', page, search],
    queryFn: () =>
      request(
        `subscriptions/hosts?page=${page}&search=${encodeURIComponent(search)}`,
        z.array(RouteSchema),
      ),
    enabled: selected === 0,
  });
  const current = useQuery({
    queryKey: ['subscriptionRoute', selected],
    queryFn: () => request(`subscriptions/hosts/${selected}`, RouteSchema),
    enabled: selected > 0,
  });
  return (
    <div className="commerce-stack">
      <Alert type="info" showIcon title={l('routesHelp')} />
      {(rows.error || current.error) && <Alert type="error" showIcon title={l('failed')} />}
      {selected > 0 ? (
        current.data && (
          <RouteEditor
            key={`${selected}:${current.data.settings.revision}`}
            route={current.data}
            saved={() => current.refetch()}
            back={() => select(0)}
          />
        )
      ) : (
        <>
          <Input.Search
            aria-label={l('searchRoute')}
            placeholder={l('searchRoute')}
            onSearch={(v) => {
              setSearch(v);
              setPage(1);
            }}
            allowClear
          />
          <Table
            rowKey="host_id"
            loading={rows.isFetching}
            dataSource={rows.data || []}
            pagination={false}
            scroll={{ x: 650 }}
            columns={[
              {
                title: l('route'),
                render: (_, route: Route) => (
                  <Button type="link" onClick={() => select(route.host_id)}>
                    {route.remark || `#${route.host_id}`}
                  </Button>
                ),
              },
              { title: l('inbound'), dataIndex: 'inbound_id' },
              { title: l('address'), dataIndex: 'address' },
              {
                title: l('transport'),
                render: (_, route) => (
                  <Space wrap>
                    <Tag>{route.network}</Tag>
                    {route.settings.client_stream && <Tag>{l('customXHTTP')}</Tag>}
                    {route.settings.cascade_mode !== 'off' && (
                      <Tag>{l(route.settings.cascade_mode)}</Tag>
                    )}
                  </Space>
                ),
              },
            ]}
          />
          <PageButtons page={page} count={rows.data?.length || 0} change={setPage} />
        </>
      )}
    </div>
  );
}
function ConnectionFields({ prefix, down }: { prefix: string; down?: boolean }) {
  const l = useSubscriptionLabel();
  const form = Form.useFormInstance();
  const security: string = Form.useWatch([prefix, 'security'], form) || 'none';
  return (
    <div className="commerce-grid">
      <Form.Item
        name={[prefix, 'address']}
        label={l(down ? 'downAddress' : 'upAddress')}
        rules={[{ required: true }]}
        extra={l('bareAddress')}
      >
        <Input maxLength={512} />
      </Form.Item>
      <Form.Item name={[prefix, 'port']} label={l('port')} rules={[{ required: true }]}>
        <InputNumber min={1} max={65535} precision={0} />
      </Form.Item>
      <Form.Item name={[prefix, 'security']} label={l('security')}>
        <Select
          options={['none', 'tls', 'reality'].map((value) => ({
            value,
            label: value.toUpperCase(),
          }))}
        />
      </Form.Item>
      <Form.Item name={[prefix, 'host']} label={l('httpHost')}>
        <Input maxLength={1024} />
      </Form.Item>
      {security !== 'none' && (
        <>
          <Form.Item name={[prefix, 'serverName']} label="SNI" rules={[{ required: true }]}>
            <Input maxLength={512} />
          </Form.Item>
          <Form.Item name={[prefix, 'alpn']} label="ALPN">
            <Select
              mode="multiple"
              tagRender={selectTagRender}
              options={['h2', 'http/1.1', 'h3'].map((value) => ({ value, label: value }))}
            />
          </Form.Item>
          <Form.Item name={[prefix, 'fingerprint']} label={l('fingerprint')}>
            <Input maxLength={64} />
          </Form.Item>
          <Form.Item
            name={[prefix, 'allowInsecure']}
            valuePropName="checked"
            label={l('allowInsecure')}
          >
            <Switch />
          </Form.Item>
        </>
      )}
      <Form.Item name={[prefix, 'maxConcurrency']} label={l('reuseConcurrency')}>
        <Input maxLength={24} />
      </Form.Item>
      <Form.Item name={[prefix, 'maxConnections']} label={l('reuseConnections')}>
        <Input maxLength={24} />
      </Form.Item>
      <Form.Item name={[prefix, 'hMaxReusableSecs']} label={l('reuseSeconds')}>
        <Input maxLength={24} />
      </Form.Item>
      <Form.Item name={[prefix, 'hKeepAlivePeriod']} label={l('keepAlive')}>
        <InputNumber min={0} precision={0} />
      </Form.Item>
      {security === 'reality' && (
        <>
          <Form.Item
            name={[prefix, 'publicKey']}
            label={l('publicKey')}
            rules={[{ required: true }]}
          >
            <Input maxLength={43} />
          </Form.Item>
          <Form.Item name={[prefix, 'shortId']} label="Short ID">
            <Input maxLength={16} />
          </Form.Item>
        </>
      )}
    </div>
  );
}
function RouteEditor({
  route,
  saved,
  back,
}: {
  route: Route;
  saved: () => Promise<unknown>;
  back: () => void;
}) {
  const l = useSubscriptionLabel();
  const [form] = Form.useForm();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState(false);
  const [success, setSuccess] = useState(false);
  const [advanced, setAdvanced] = useState(route.settings.client_stream || '{}');
  const [advancedTouched, setAdvancedTouched] = useState(false);
  const [connectionTouched, setConnectionTouched] = useState(false);
  const original = parseStream(route.settings.client_stream);
  const tr = obj(original.xhttpSettings || original.splithttpSettings);
  const effective = tr.extra ? obj(tr.extra) : tr;
  const down = obj(effective.downloadSettings);
  const inherit = Form.useWatch('inherit', form) ?? !route.settings.client_stream;
  const enableDown = Form.useWatch('enableDown', form) ?? Object.keys(down).length > 0;
  const mode = Form.useWatch('cascade_mode', form) || 'off';
  const entryMethod =
    Form.useWatch('entry_method', form) || (route.settings.entry_configured ? 'keep' : 'node');
  const [entrySearch, setEntrySearch] = useState('');
  const entryChoices = useQuery({
    queryKey: ['cascadeEntries', entrySearch, route.settings.entry_node_id],
    queryFn: () =>
      request(
        `subscriptions/cascade-entries?search=${encodeURIComponent(entrySearch)}&selected_id=${route.settings.entry_node_id || 0}`,
        z.array(EntryChoiceSchema),
      ),
    enabled: mode !== 'off',
  });
  async function save(v: Record<string, unknown>) {
    setBusy(true);
    setError(false);
    setSuccess(false);
    try {
      let clientStream = v.inherit ? '' : route.settings.client_stream;
      if (
        !v.inherit &&
        ['xhttp', 'splithttp'].includes(route.network) &&
        (connectionTouched || advancedTouched)
      ) {
        let connection = buildConnection(
          obj(v.up),
          advancedTouched ? parseStream(advanced) : original,
          route.path,
        );
        const transport = obj(connection.xhttpSettings);
        transport.mode = v.mode || 'auto';
        const effective = transport.extra ? obj(transport.extra) : transport;
        if (v.enableDown)
          effective.downloadSettings = buildConnection(obj(v.down), down, route.path);
        else {
          delete transport.downloadSettings;
          delete effective.downloadSettings;
        }
        connection.xhttpSettings = transport;
        if (advancedTouched) {
          connection = obj(JSON.parse(advanced));
          if (!Object.keys(connection).length) throw new Error('Empty connection');
        }
        clientStream = JSON.stringify(connection);
      }
      await request(
        `subscriptions/hosts/${route.host_id}`,
        SubscriptionHostSchema,
        {
          revision: route.settings.revision,
          cascade_mode: v.cascade_mode,
          entry_node_id: v.entry_method === 'node' ? v.entry_node_id || null : null,
          entry_yaml: v.entry_method === 'yaml' ? v.entry_yaml || '' : '',
          clear_entry: v.entry_method !== 'keep',
          client_stream: clientStream,
          mihomo_only: v.mihomo_only,
        },
        'PUT',
      );
      form.setFieldValue('entry_yaml', '');
      setSuccess(true);
      await saved();
    } catch {
      setError(true);
    } finally {
      setBusy(false);
    }
  }
  return (
    <Card
      title={route.remark || `#${route.host_id}`}
      extra={<Button onClick={back}>{l('back')}</Button>}
    >
      <Typography.Paragraph>
        {l('inbound')} #{route.inbound_id} · {route.address} · {route.network}
      </Typography.Paragraph>
      {error && <Alert type="error" showIcon title={l('routeSaveFailed')} />}
      {success && <Alert type="success" showIcon title={l('saved')} />}
      <Form
        form={form}
        layout="vertical"
        disabled={busy}
        onFinish={save}
        onValuesChange={(changed) => {
          if (
            Object.keys(changed).some((key) =>
              ['inherit', 'up', 'down', 'mode', 'enableDown'].includes(key),
            )
          )
            setConnectionTouched(true);
        }}
        initialValues={{
          ...route.settings,
          inherit: !route.settings.client_stream,
          enableDown: Object.keys(down).length > 0,
          up: { ...legFields(original), address: original.address || route.address },
          down: legFields(down),
          mode: tr.mode || 'auto',
          entry_yaml: '',
          entry_method: route.settings.entry_configured ? 'keep' : 'node',
        }}
      >
        <Card size="small" title={l('cascade')}>
          <Form.Item name="cascade_mode" label={l('cascadeMode')}>
            <Select
              options={['off', 'replace', 'additional'].map((value) => ({
                value,
                label: l(value),
              }))}
            />
          </Form.Item>
          {mode !== 'off' && (
            <>
              <Alert type="info" showIcon title={l('cascadeHelp')} />
              {route.settings.entry_configured && (
                <Typography.Paragraph>{l('entryStored')}</Typography.Paragraph>
              )}
              <Form.Item name="entry_method" label={l('entryMethod')}>
                <Select
                  options={[
                    {
                      value: 'keep',
                      label: l('keepEntry'),
                      disabled: !route.settings.entry_configured,
                    },
                    { value: 'node', label: l('chooseSharedEntry') },
                    { value: 'yaml', label: l('pasteEntry') },
                  ]}
                />
              </Form.Item>
              {entryMethod === 'node' && (
                <>
                  <Input.Search
                    aria-label={l('searchEntry')}
                    placeholder={l('searchEntry')}
                    onSearch={setEntrySearch}
                  />
                  <Form.Item
                    name="entry_node_id"
                    label={l('sharedEntryID')}
                    rules={[{ required: true }]}
                  >
                    <Select
                      showSearch
                      optionFilterProp="label"
                      loading={entryChoices.isFetching}
                      options={(entryChoices.data || []).map((n) => ({
                        value: n.id,
                        label: `${n.source_name} / ${n.name} · ${n.proxy_type}`,
                      }))}
                    />
                  </Form.Item>
                </>
              )}
              {entryMethod === 'yaml' && (
                <Form.Item
                  name="entry_yaml"
                  label={l('entryYAML')}
                  extra={l('entryYAMLHelp')}
                  rules={[{ required: true }]}
                >
                  <Input.TextArea rows={5} maxLength={32768} autoComplete="off" />
                </Form.Item>
              )}
            </>
          )}
        </Card>
        {['xhttp', 'splithttp'].includes(route.network) && (
          <Card size="small" title={l('xhttp')}>
            <Form.Item
              htmlFor="subscription-backend-path"
              label={l('backendPath')}
              extra={l('backendPathHelp')}
            >
              <Input id="subscription-backend-path" value={route.path} readOnly />
            </Form.Item>
            <Form.Item name="mihomo_only" label={l('mihomoOnly')} valuePropName="checked">
              <Switch />
            </Form.Item>
            <Form.Item name="inherit" label={l('inheritConnection')} valuePropName="checked">
              <Switch />
            </Form.Item>
            {!inherit && (
              <>
                <Typography.Title level={3}>{l('uplink')}</Typography.Title>
                <ConnectionFields prefix="up" />
                <Form.Item name="mode" label={l('xhttpMode')}>
                  <Select
                    options={['auto', 'packet-up', 'stream-up', 'stream-one'].map((value) => ({
                      value,
                      label: value,
                    }))}
                  />
                </Form.Item>
                <Form.Item name="enableDown" label={l('separateDownlink')} valuePropName="checked">
                  <Switch />
                </Form.Item>
                {enableDown && (
                  <>
                    <Typography.Title level={3}>{l('downlink')}</Typography.Title>
                    <ConnectionFields prefix="down" down />
                  </>
                )}
                <Collapse
                  items={[
                    {
                      key: 'advanced',
                      label: l('advancedClientJSON'),
                      children: (
                        <>
                          <Typography.Paragraph>{l('advancedJSONHelp')}</Typography.Paragraph>
                          <Button
                            disabled={!advancedTouched}
                            onClick={() => {
                              setAdvancedTouched(false);
                              setConnectionTouched(true);
                            }}
                          >
                            {l('useForm')}
                          </Button>
                          <Input.TextArea
                            aria-label={l('advancedClientJSON')}
                            value={advanced}
                            rows={12}
                            onChange={(event) => {
                              setAdvanced(event.target.value);
                              setAdvancedTouched(true);
                            }}
                            autoComplete="off"
                          />
                        </>
                      ),
                    },
                  ]}
                />
              </>
            )}
          </Card>
        )}
        <Space className="subscription-save">
          <Button type="primary" htmlType="submit" loading={busy}>
            {l('saveRoute')}
          </Button>
          <Typography.Text type="secondary">{l('noCoreRestart')}</Typography.Text>
        </Space>
      </Form>
    </Card>
  );
}
