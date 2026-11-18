import { useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import { useTranslation } from 'react-i18next';
import {
  Alert,
  Button,
  Card,
  Form,
  Input,
  InputNumber,
  Popconfirm,
  Space,
  Switch,
  Table,
  Tag,
  Typography,
} from 'antd';
import { z } from 'zod';
import { commerceRequest, portalRequest, emptyResponse } from '@/api/commerce';
import { SourceSchema, NodeSchema, type Source } from '@/schemas/subscriptionCompat';
import { date } from '@/pages/commerce/commerceFormat';
import { PageButtons } from '@/pages/commerce/CommerceShared';

export function useSubscriptionLabel() {
  const { t } = useTranslation();
  return (key: string) => t(`pages.subscriptionCompat.${key}`);
}
export function useExternalRequest(token?: string, clientId?: number) {
  return <T,>(path: string, schema: z.ZodType<T>, body?: unknown, method?: string) =>
    token === undefined
      ? commerceRequest(
          clientId ? `subscriptions/clients/${clientId}/${path}` : path,
          schema,
          body,
          method,
        )
      : portalRequest(path, schema, token, body, method);
}
export default function ExternalSources({
  token,
  clientId,
  selected,
  select,
}: {
  token?: string;
  clientId?: number;
  selected?: number;
  select?: (id: number) => void;
}) {
  const l = useSubscriptionLabel();
  const request = useExternalRequest(token, clientId);
  const [page, setPage] = useState(1);
  const [search, setSearch] = useState('');
  const [localID, setLocalID] = useState(0);
  const [creating, setCreating] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState(false);
  const id = selected ?? localID;
  function choose(next: number) {
    setError(false);
    setCreating(false);
    if (select) select(next);
    else setLocalID(next);
  }
  const rows = useQuery({
    queryKey: ['externalSources', token ?? clientId ?? 'admin', page, search],
    queryFn: () =>
      request(
        `external-subscriptions?page=${page}&search=${encodeURIComponent(search)}`,
        z.array(SourceSchema),
      ),
    enabled: id === 0,
  });
  const current = useQuery({
    queryKey: ['externalSource', token ?? clientId ?? 'admin', id],
    queryFn: () => request(`external-subscriptions/${id}`, SourceSchema),
    enabled: id > 0,
  });
  async function create(v: {
    name: string;
    group_name: string;
    url?: string;
    enabled: boolean;
    refresh_interval_minutes: number;
  }) {
    setBusy(true);
    setError(false);
    try {
      const next = await request('external-subscriptions', SourceSchema, v);
      choose(next.id);
      await rows.refetch();
    } catch {
      setError(true);
    } finally {
      setBusy(false);
    }
  }
  return (
    <div className="commerce-stack">
      <Alert
        type="info"
        showIcon
        title={l(token === undefined && !clientId ? 'adminSourcesHelp' : 'privateSourcesHelp')}
      />
      {(error || rows.error || current.error) && (
        <Alert
          type="error"
          showIcon
          title={l('failed')}
          action={
            <Button
              onClick={() => {
                void rows.refetch();
                void current.refetch();
              }}
            >
              {l('reload')}
            </Button>
          }
        />
      )}
      {id > 0 ? (
        current.data && (
          <SourceDetail
            key={`${token ?? clientId ?? 'admin'}:${id}`}
            token={token}
            clientId={clientId}
            source={current.data}
            changed={() => current.refetch()}
            back={() => choose(0)}
          />
        )
      ) : (
        <>
          <Space wrap>
            <Input.Search
              aria-label={l('search')}
              placeholder={l('search')}
              onSearch={(value) => {
                setSearch(value);
                setPage(1);
              }}
              allowClear
            />
            <Button type="primary" onClick={() => setCreating(!creating)}>
              {creating ? l('cancel') : l('createSource')}
            </Button>
          </Space>
          {creating && (
            <Card title={l('createSource')}>
              <Form
                layout="vertical"
                initialValues={{ enabled: true, refresh_interval_minutes: 360 }}
                onFinish={create}
                disabled={busy}
              >
                <Form.Item name="name" label={l('name')} rules={[{ required: true }]}>
                  <Input maxLength={128} />
                </Form.Item>
                <Form.Item name="group_name" label={l('groupName')} extra={l('immutableGroup')}>
                  <Input maxLength={128} />
                </Form.Item>
                <Form.Item name="url" label={l('writeOnlyURL')} extra={l('credentialHelp')}>
                  <Input.Password autoComplete="new-password" maxLength={8192} />
                </Form.Item>
                <Form.Item
                  name="enabled"
                  label={l(token === undefined && !clientId ? 'autoRefresh' : 'mixAndRefresh')}
                  valuePropName="checked"
                >
                  <Switch />
                </Form.Item>
                <Form.Item
                  name="refresh_interval_minutes"
                  label={l('refreshMinutes')}
                  rules={[{ required: true }]}
                >
                  <InputNumber min={15} max={43200} precision={0} />
                </Form.Item>
                <Button type="primary" htmlType="submit" loading={busy}>
                  {l('createSource')}
                </Button>
              </Form>
            </Card>
          )}
          <Table
            rowKey="id"
            loading={rows.isFetching}
            dataSource={rows.data || []}
            pagination={false}
            scroll={{ x: 750 }}
            columns={[
              {
                title: l('name'),
                render: (_, source: Source) => (
                  <Button type="link" onClick={() => choose(source.id)}>
                    {source.name}
                  </Button>
                ),
              },
              {
                title: l('owner'),
                render: (_, source) =>
                  source.owner_account_id
                    ? source.owner_username || `#${source.owner_account_id}`
                    : l('publicSource'),
              },
              { title: l('nodeCount'), dataIndex: 'node_count' },
              {
                title: l('status'),
                render: (_, source) => (
                  <Space orientation="vertical" size={0}>
                    <Tag color={source.last_error_code ? 'warning' : undefined}>
                      {source.last_error_code
                        ? l('usingLastGood')
                        : l(source.node_count ? 'ready' : 'empty')}
                    </Tag>
                    <Typography.Text type="secondary">
                      {l(source.credential_configured ? 'credentialConfigured' : 'noCredential')}
                    </Typography.Text>
                  </Space>
                ),
              },
              { title: l('lastSuccess'), dataIndex: 'last_success_at', render: date },
            ]}
          />
          <PageButtons page={page} count={rows.data?.length || 0} change={setPage} />
        </>
      )}
    </div>
  );
}
function SourceDetail({
  source,
  token,
  clientId,
  changed,
  back,
}: {
  source: Source;
  token?: string;
  clientId?: number;
  changed: () => Promise<unknown>;
  back: () => void;
}) {
  const l = useSubscriptionLabel();
  const request = useExternalRequest(token, clientId);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState(false);
  const [success, setSuccess] = useState(false);
  const [page, setPage] = useState(1);
  const [yaml, setYaml] = useState('');
  const [credentialForm] = Form.useForm();
  const nodes = useQuery({
    queryKey: ['externalNodes', token ?? clientId ?? 'admin', source.id, page, source.updated_at],
    queryFn: () =>
      request(`external-subscriptions/${source.id}/nodes?page=${page}`, z.array(NodeSchema)),
  });
  const root = `external-subscriptions/${source.id}`;
  async function perform(work: () => Promise<unknown>) {
    setBusy(true);
    setError(false);
    setSuccess(false);
    try {
      await work();
      setSuccess(true);
      await changed();
      await nodes.refetch();
    } catch {
      setError(true);
    } finally {
      setBusy(false);
    }
  }
  return (
    <div className="commerce-stack">
      <Card title={source.name} extra={<Button onClick={back}>{l('back')}</Button>}>
        {error && <Alert type="error" showIcon title={l('refreshFailed')} />}
        {success && <Alert type="success" showIcon title={l('saved')} />}
        <Typography.Paragraph>
          {l('lastSuccess')}: {date(source.last_success_at)} · {l('nodeCount')}: {source.node_count}
        </Typography.Paragraph>
        {source.last_error_code && (
          <Alert
            type="warning"
            showIcon
            title={l('usingLastGood')}
            description={`${l('safeErrorCode')}: ${source.last_error_code}`}
          />
        )}
        <Form
          key={`${source.id}:${source.updated_at}`}
          layout="vertical"
          initialValues={source}
          onFinish={(v) => void perform(() => request(root, SourceSchema, v, 'PUT'))}
          disabled={busy}
        >
          <Form.Item name="name" label={l('name')} rules={[{ required: true }]}>
            <Input maxLength={128} />
          </Form.Item>
          <Form.Item name="group_name" label={l('groupName')} extra={l('immutableGroup')}>
            <Input readOnly />
          </Form.Item>
          <Form.Item
            name="enabled"
            valuePropName="checked"
            label={l(source.owner_account_id ? 'mixAndRefresh' : 'autoRefresh')}
            extra={l(source.owner_account_id ? 'privateDisableHelp' : 'publicDisableHelp')}
          >
            <Switch />
          </Form.Item>
          <Form.Item name="refresh_interval_minutes" label={l('refreshMinutes')}>
            <InputNumber min={15} max={43200} precision={0} />
          </Form.Item>
          <Space wrap>
            <Button type="primary" htmlType="submit" loading={busy}>
              {l('save')}
            </Button>
            <Button
              disabled={!source.credential_configured}
              loading={busy}
              onClick={() => void perform(() => request(`${root}/refresh`, SourceSchema, {}))}
            >
              {l('refreshNow')}
            </Button>
          </Space>
        </Form>
      </Card>
      <Card title={l('writeOnlyURL')}>
        <Typography.Paragraph>{l('credentialHelp')}</Typography.Paragraph>
        <Form
          form={credentialForm}
          layout="vertical"
          disabled={busy}
          onFinish={(v) =>
            void perform(async () => {
              await request(`${root}/credential`, SourceSchema, v);
              credentialForm.resetFields();
            })
          }
        >
          <Form.Item name="url" label={l('newURL')} rules={[{ required: true }]}>
            <Input.Password autoComplete="new-password" maxLength={8192} />
          </Form.Item>
          <Space wrap>
            <Button type="primary" htmlType="submit" loading={busy}>
              {l('replaceCredential')}
            </Button>
            <Popconfirm
              title={l('removeCredentialConfirm')}
              onConfirm={() =>
                perform(() => request(`${root}/credential`, SourceSchema, undefined, 'DELETE'))
              }
            >
              <Button disabled={busy || !source.credential_configured}>
                {l('removeCredential')}
              </Button>
            </Popconfirm>
          </Space>
        </Form>
      </Card>
      <Card title={l('importSnapshot')}>
        <Space orientation="vertical" className="commerce-full">
          <Typography.Paragraph>{l('snapshotHelp')}</Typography.Paragraph>
          <label htmlFor={`source-file-${source.id}`}>{l('yamlFile')}</label>
          <input
            id={`source-file-${source.id}`}
            type="file"
            accept=".yaml,.yml,.txt"
            disabled={busy}
            onChange={(event) => {
              const file = event.target.files?.[0];
              if (file) {
                if (file.size > 2 * 1024 * 1024) setError(true);
                else void file.text().then(setYaml);
              }
              event.target.value = '';
            }}
          />
          <label htmlFor={`source-yaml-${source.id}`}>{l('yamlText')}</label>
          <Input.TextArea
            id={`source-yaml-${source.id}`}
            rows={7}
            value={yaml}
            maxLength={2 * 1024 * 1024}
            onChange={(event) => setYaml(event.target.value)}
            disabled={busy}
            autoComplete="off"
          />
          <Button
            type="primary"
            loading={busy}
            disabled={!yaml.trim()}
            onClick={() =>
              void perform(async () => {
                await request(`${root}/snapshot`, SourceSchema, { yaml });
                setYaml('');
              })
            }
          >
            {l('publishSnapshot')}
          </Button>
        </Space>
      </Card>
      <Card title={l('nodes')}>
        <Table
          rowKey="id"
          dataSource={nodes.data || []}
          loading={nodes.isFetching}
          pagination={false}
          columns={[
            { title: 'ID', dataIndex: 'id' },
            { title: l('name'), dataIndex: 'name' },
            { title: l('protocol'), dataIndex: 'proxy_type' },
            { title: l('status'), render: (_, node) => l(node.is_active ? 'active' : 'missing') },
          ]}
        />
        <PageButtons page={page} count={nodes.data?.length || 0} change={setPage} />
      </Card>
      <Card title={l('deleteSource')}>
        <Popconfirm
          title={l('deleteSourceConfirm')}
          onConfirm={() =>
            perform(async () => {
              await request(root, emptyResponse, undefined, 'DELETE');
              back();
            })
          }
        >
          <Button danger loading={busy}>
            {l('deleteSource')}
          </Button>
        </Popconfirm>
      </Card>
    </div>
  );
}
