import { useContext, useId, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { useQuery } from '@tanstack/react-query';
import {
  Alert,
  Button,
  Checkbox,
  Form,
  Input,
  Modal,
  Select,
  Space,
  Table,
  Tag,
  message,
} from 'antd';
import { XrayScope, useXrayHttp } from '@/hooks/xrayScope';
import type { XraySettingsValue, SetTemplate } from '@/hooks/useXraySetting';
import { NativeIPv6ReportSchema, NativeIPv6AddressSchema } from '@/generated/zod';
import type { NativeIPv6Address } from '@/generated/types';
import { z } from 'zod';
import {
  disableNativeUserIPv6Auto,
  enableNativeUserIPv6Auto,
  nativeUserIPv6AutoEnabled,
  planNativeUserIPv6,
} from './native-user-ipv6';
import { selectTagRender } from '@/components/form/SelectTag';

const JSON_OPTIONS = {
  headers: { 'Content-Type': 'application/json' },
  silent: true,
  timeout: 45000,
};

export default function NativeUserIPv6Modal({
  open,
  onClose,
  template,
  setTemplate,
  inboundTags,
  hasInjectedOutbounds,
}: {
  open: boolean;
  onClose: () => void;
  template: XraySettingsValue | null;
  setTemplate: SetTemplate;
  inboundTags: string[];
  hasInjectedOutbounds: boolean;
}) {
  const { t } = useTranslation();
  const uid = useId();
  const label = (key: string) => t(`pages.xray.user6.${key}`);
  const http = useXrayHttp();
  const nodeId = useContext(XrayScope);
  const [poolForm] = Form.useForm();
  const [inbound, setInbound] = useState<string>();
  const [clients, setClients] = useState<number[]>([]);
  const [followNewUsers, setFollowNewUsers] = useState(false);
  const [busy, setBusy] = useState(false);
  const [messages, context] = message.useMessage();
  const report = useQuery({
    queryKey: ['native-ipv6', nodeId],
    enabled: open,
    queryFn: async () => {
      const msg = await http.get('/panel/api/xray/ipv6Pool', undefined, { silent: true });
      if (!msg.success) throw new Error(msg.msg);
      return NativeIPv6ReportSchema.parse(msg.obj);
    },
  });
  const available = report.data?.clients.filter((c) => c.inbounds.includes(inbound ?? '')) ?? [];
  const autoEnabled = !!template && !!inbound && nativeUserIPv6AutoEnabled(template, inbound);
  async function configure() {
    try {
      const value = await poolForm.validateFields();
      setBusy(true);
      const msg = await http.post('/panel/api/xray/ipv6Pool', value, JSON_OPTIONS);
      if (!msg.success) throw new Error(msg.msg);
      await report.refetch();
    } catch (e) {
      void messages.error(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  }
  async function generate() {
    try {
      if (!template || !inbound) throw new Error(label('selectClients'));
      const selected = available.filter(
        (c) =>
          (followNewUsers || clients.includes(c.id)) &&
          (!followNewUsers ||
            !template.routing?.rules?.some((rule) => rule.ruleTag === `user6-${c.id}-${inbound}`)),
      );
      const apply =
        followNewUsers && !selected.length
          ? () => enableNativeUserIPv6Auto(template, inbound, hasInjectedOutbounds)
          : planNativeUserIPv6(template, inbound, selected, hasInjectedOutbounds, followNewUsers);
      setBusy(true);
      const leases: NativeIPv6Address[] = [];
      for (let offset = 0; offset < selected.length; offset += 8) {
        const msg = await http.post(
          '/panel/api/xray/ipv6Pool/allocate',
          { clientIds: selected.slice(offset, offset + 8).map((c) => c.id) },
          JSON_OPTIONS,
        );
        if (!msg.success) throw new Error(msg.msg);
        leases.push(...z.array(NativeIPv6AddressSchema).parse(msg.obj));
      }
      setTemplate(apply(leases));
      void messages.success(label('generated'));
      onClose();
    } catch (e) {
      void messages.error(t(e instanceof Error ? e.message : String(e)));
    } finally {
      setBusy(false);
    }
  }
  return (
    <Modal
      open={open}
      title={label('title')}
      width={780}
      onCancel={onClose}
      closable={!busy}
      mask={{ closable: !busy }}
      confirmLoading={busy}
      okText={label('generate')}
      onOk={() => void generate()}
      okButtonProps={{
        disabled:
          !report.data?.pool ||
          !!report.data?.error ||
          !inbound ||
          (!followNewUsers && !clients.length) ||
          autoEnabled ||
          report.isFetching,
      }}
      cancelButtonProps={{ disabled: busy }}
    >
      {context}
      <Space orientation="vertical" style={{ width: '100%' }} size="middle">
        <Alert type="info" showIcon title={label('help')} />
        {(report.error || report.data?.error) && (
          <Alert type="error" showIcon title={report.error?.message || report.data?.error} />
        )}
        {report.data && !report.data.pool ? (
          <Form form={poolForm} layout="vertical">
            <Alert type="warning" title={label('poolHelp')} />
            <Form.Item name="interface" label={label('interface')} rules={[{ required: true }]}>
              <Select options={report.data.interfaces.map((value) => ({ value }))} />
            </Form.Item>
            <Form.Item name="prefix" label={label('prefix')} rules={[{ required: true }]}>
              <Input placeholder="2001:db8:1234::/64" />
            </Form.Item>
            <Button loading={busy} disabled={!!report.data.error} onClick={() => void configure()}>
              {label('configure')}
            </Button>
          </Form>
        ) : (
          report.data?.pool && (
            <Tag>
              {report.data.pool.interface} · {report.data.pool.prefix}
            </Tag>
          )
        )}
        <Form layout="vertical">
          <Form.Item label={label('inbound')} htmlFor={`${uid}-inbound`}>
            <Select
              id={`${uid}-inbound`}
              value={inbound}
              disabled={busy}
              onChange={(value) => {
                setInbound(value);
                setClients([]);
                setFollowNewUsers(false);
              }}
              options={inboundTags.map((value) => ({ value }))}
            />
          </Form.Item>
          <Form.Item label={label('users')} htmlFor={`${uid}-clients`}>
            <Select
              id={`${uid}-clients`}
              mode="multiple"
              maxCount={128}
              tagRender={selectTagRender}
              value={followNewUsers ? available.map((client) => client.id) : clients}
              disabled={busy || followNewUsers || autoEnabled}
              onChange={setClients}
              optionFilterProp="label"
              options={available.map((c) => ({ value: c.id, label: c.email }))}
            />
          </Form.Item>
          {autoEnabled ? (
            <Space orientation="vertical">
              <Alert type="success" showIcon title={label('autoEnabled')} />
              <Button
                disabled={busy}
                onClick={() => {
                  if (template && inbound) {
                    setTemplate(disableNativeUserIPv6Auto(template, inbound));
                    void messages.success(label('generated'));
                    onClose();
                  }
                }}
              >
                {label('disableAuto')}
              </Button>
            </Space>
          ) : (
            <Checkbox
              checked={followNewUsers}
              disabled={busy || !inbound}
              onChange={(event) => setFollowNewUsers(event.target.checked)}
            >
              {label('followNewUsers')}
            </Checkbox>
          )}
        </Form>
        <Table
          size="small"
          pagination={{ pageSize: 5 }}
          rowKey="clientId"
          dataSource={report.data?.leases ?? []}
          columns={[
            {
              title: label('users'),
              render: (_, lease) =>
                report.data?.clients.find((c) => c.id === lease.clientId)?.email ?? lease.clientId,
            },
            { title: label('address'), dataIndex: 'address' },
            {
              title: label('status'),
              render: (_, lease) => (
                <Tag color={lease.ready ? 'green' : 'orange'}>
                  {label(lease.ready ? 'ready' : 'pending')}
                </Tag>
              ),
            },
          ]}
        />
      </Space>
    </Modal>
  );
}
