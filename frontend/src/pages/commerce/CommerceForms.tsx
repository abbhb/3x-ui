import { useState } from 'react';
import { HostChoiceSchema } from '@/generated/zod';
import { useQuery } from '@tanstack/react-query';
import { useTranslation } from 'react-i18next';
import { Alert, Button, Card, Form, Input, InputNumber, Select, Space, Switch } from 'antd';
import { z } from 'zod';
import { selectTagRender } from '@/components/form/SelectTag';
import PlanSubscriptionFields from '@/pages/subscriptions/PlanSubscriptionFields';
import { commerceRequest as request, emptyResponse } from '@/api/commerce';
import { useInboundOptions } from '@/api/queries/useInboundOptions';
import { PlanSchema, type Plan, type Settings, type SettingsViewSchema } from '@/schemas/commerce';
export const useLabel = () => {
  const { t } = useTranslation();
  return (key: string) => t(`pages.commerce.${key}`);
};
export function PlanEditor({ plan, close }: { plan: Plan | null; close: () => void }) {
  const l = useLabel();
  const [error, setError] = useState(false);
  const [busy, setBusy] = useState(false);
  const { data: inbounds = [] } = useInboundOptions();
  const hosts = useQuery({
    queryKey: ['commerce', 'hostOptions'],
    queryFn: async () => {
      const rows = await request('subscriptions/catalog', z.array(HostChoiceSchema));
      return rows.map((h) => ({
        id: h.host_id,
        inboundId: h.inbound_id,
        remark: h.name,
        address: h.address,
        isDisabled: h.disabled,
        signature: h.signature,
      }));
    },
  });
  const [form] = Form.useForm();
  const inboundIDs: number[] = Form.useWatch('inbound_ids', form) || [];
  const autoInboundIDs: number[] = Form.useWatch('auto_host_inbound_ids', form) || [];
  if (!hosts.data)
    return (
      <Card>
        <Alert
          type={hosts.error ? 'error' : 'info'}
          title={hosts.error ? l('failed') : l('hostRules')}
        />
      </Card>
    );
  return (
    <Card
      title={plan ? l('editPlan') : l('createPlan')}
      extra={<Button onClick={close}>{l('back')}</Button>}
    >
      {(error || hosts.error) && <Alert type="error" showIcon title={l('failed')} />}
      {plan && !plan.has_routes && (
        <Alert
          type="info"
          showIcon
          title={l('unconfiguredPlan')}
          description={l('unconfiguredPlanHelp')}
        />
      )}
      <Form
        form={form}
        layout="vertical"
        initialValues={
          plan
            ? {
                ...plan,
                host_ids: plan.host_ids.filter(
                  (id) =>
                    !plan.host_signatures?.[String(id)] ||
                    hosts.data?.some(
                      (h) => h.id === id && h.signature === plan.host_signatures?.[String(id)],
                    ),
                ),
                auto_host_inbound_ids: plan.auto_host_inbound_ids || [],
                external_selections: plan.external_selections || [],
                group_members: plan.group_members || {},
              }
            : {
                currency: 'CNY',
                price_minor: 0,
                duration_days: 30,
                data_limit: 0,
                inbound_ids: [],
                host_ids: [],
                external_selections: [],
                auto_host_inbound_ids: [],
                group_members: {},
                is_visible: false,
              }
        }
        disabled={busy}
        onFinish={async (v) => {
          setBusy(true);
          setError(false);
          try {
            const chosen: number[] = v.host_ids || [];
            const fresh = await request('subscriptions/catalog', z.array(HostChoiceSchema));
            if (
              chosen.some(
                (id) =>
                  hosts.data?.find((h) => h.id === id)?.signature !==
                  fresh.find((h) => h.host_id === id)?.signature,
              )
            )
              throw new Error('Route changed');
            const signatures = Object.fromEntries(
              chosen.map((id) => [
                String(id),
                fresh.find((h) => h.host_id === id)?.signature || '',
              ]),
            );
            await request(
              plan ? `plans/${plan.id}` : 'plans',
              PlanSchema,
              {
                ...v,
                host_signatures: signatures,
                description: v.description || '',
                currency: 'CNY',
              },
              plan ? 'PUT' : 'POST',
            );
            close();
          } catch {
            setError(true);
          } finally {
            setBusy(false);
          }
        }}
      >
        <Form.Item name="name" label={l('name')} rules={[{ required: true }]}>
          <Input maxLength={128} />
        </Form.Item>
        <Form.Item name="description" label={l('descriptionLabel')}>
          <Input.TextArea maxLength={1000} />
        </Form.Item>
        <Form.Item name="price_minor" label={l('priceCents')} rules={[{ required: true }]}>
          <InputNumber min={0} max={1e12} precision={0} className="commerce-full" />
        </Form.Item>
        <Form.Item name="duration_days" label={l('days')} rules={[{ required: true }]}>
          <InputNumber min={1} max={3650} precision={0} />
        </Form.Item>
        <Form.Item
          name="data_limit"
          label={l('dataBytes')}
          rules={[{ required: true }]}
          extra={l('zeroUnlimited')}
        >
          <InputNumber
            min={0}
            max={Number.MAX_SAFE_INTEGER}
            precision={0}
            className="commerce-full"
          />
        </Form.Item>
        <Form.Item name="inbound_ids" label={l('inbounds')} rules={[]}>
          <Select
            mode="multiple"
            tagRender={selectTagRender}
            options={inbounds
              .filter((i) => ['vless', 'hysteria'].includes(i.protocol || ''))
              .map((i) => ({
                value: i.id,
                label: `${i.id} · ${i.remark || i.tag} · ${i.protocol}`,
              }))}
            onChange={(ids: number[]) =>
              form.setFieldValue(
                'host_ids',
                (form.getFieldValue('host_ids') || []).filter((id: number) =>
                  hosts.data?.some((h) => h.id === id && ids.includes(h.inboundId)),
                ),
              )
            }
          />
        </Form.Item>
        <Form.Item
          name="host_ids"
          label={l('hostRules')}
          rules={[
            {
              validator: (_, value) =>
                inboundIDs.some((id) => !autoInboundIDs.includes(id)) && !value?.length
                  ? Promise.reject(new Error(l('strictGrantHelp')))
                  : Promise.resolve(),
            },
          ]}
          extra={l('strictGrantHelp')}
        >
          <Select
            mode="multiple"
            tagRender={selectTagRender}
            loading={hosts.isPending}
            options={(hosts.data || [])
              .filter((h) => inboundIDs.includes(h.inboundId))
              .map((h) => ({
                value: h.id,
                label: `#${h.id} · ${h.remark || h.address} · ${l('inbound')} ${h.inboundId}`,
              }))}
          />
        </Form.Item>
        <Form.Item name="auto_host_inbound_ids" label={l('autoHosts')} extra={l('autoHostsHelp')}>
          <Select
            mode="multiple"
            tagRender={selectTagRender}
            options={inboundIDs.map((id) => ({ value: id, label: `${l('inbound')} #${id}` }))}
          />
        </Form.Item>
        <PlanSubscriptionFields inboundIDs={inboundIDs} />
        <Form.Item name="is_visible" label={l('visible')} valuePropName="checked">
          <Switch />
        </Form.Item>
        <Button type="primary" htmlType="submit" loading={busy}>
          {l('save')}
        </Button>
      </Form>
    </Card>
  );
}
export function SettingsEditor({
  value,
  saved,
}: {
  value: z.infer<typeof SettingsViewSchema>;
  saved: () => void;
}) {
  const l = useLabel();
  const [error, setError] = useState(false);
  const [busy, setBusy] = useState(false);
  return (
    <Card title={l('settings')}>
      <Space orientation="vertical" className="commerce-full" size="middle">
        <Alert
          type={value.pepper_configured ? 'info' : 'warning'}
          showIcon
          title={value.pepper_configured ? l('pepperReady') : l('pepperMissing')}
          description={value.legacy_key_configured ? l('legacyKeyReady') : l('legacyKeyMissing')}
        />
        <Alert type="info" showIcon title={l('proxyTrustHelp')} />
        {error && <Alert type="error" showIcon title={l('failed')} />}
        <Form<Settings & { cidr_text: string }>
          layout="vertical"
          initialValues={{ ...value.settings, cidr_text: value.settings.custom_cidrs.join('\n') }}
          onFinish={async (v) => {
            setBusy(true);
            setError(false);
            const { cidr_text, ...fields } = v;
            try {
              await request(
                'settings',
                emptyResponse,
                { ...fields, custom_cidrs: cidr_text.split(/\s+/).filter(Boolean) },
                'PUT',
              );
              saved();
            } catch {
              setError(true);
            } finally {
              setBusy(false);
            }
          }}
        >
          <Form.Item label={l('portalEnabled')} name="enabled" valuePropName="checked">
            <Switch disabled={!value.pepper_configured} />
          </Form.Item>
          <Form.Item label={l('publicURL')} name="public_url" extra={l('publicURLHelp')}>
            <Input type="url" />
          </Form.Item>
          <Form.Item label={l('mgmaMode')} name="mode">
            <Select
              options={['legacy', 'dual', 'ephemeral'].map((v) => ({ value: v, label: l(v) }))}
            />
          </Form.Item>
          <Form.Item
            label={l('ttlSeconds')}
            name="ttl_seconds"
            rules={[
              { required: true },
              {
                validator: (_, v) =>
                  v === 0 || (v >= 30 && v <= 315360000)
                    ? Promise.resolve()
                    : Promise.reject(new Error(l('ttlHelp'))),
              },
            ]}
            extra={l('ttlHelp')}
          >
            <InputNumber min={0} max={315360000} precision={0} />
          </Form.Item>
          <Form.Item label={l('singleUse')} name="single_use" valuePropName="checked">
            <Switch />
          </Form.Item>
          <Form.Item label={l('sourceMode')} name="source_mode">
            <Select
              options={['any', 'china', 'custom', 'china_or_custom'].map((v) => ({
                value: v,
                label: l(v),
              }))}
            />
          </Form.Item>
          <Form.Item label={l('customCIDRs')} name="cidr_text" extra={l('chinaSnapshot')}>
            <Input.TextArea rows={5} />
          </Form.Item>
          <Form.Item label={l('autoBlock')} name="auto_block_enabled" valuePropName="checked">
            <Switch />
          </Form.Item>
          {(
            [
              'login_failure_limit',
              'registration_failure_limit',
              'login_window_seconds',
              'registration_window_seconds',
              'auto_block_seconds',
            ] as const
          ).map((key) => (
            <Form.Item key={key} label={l(key)} name={key} rules={[{ required: true }]}>
              <InputNumber
                min={key.endsWith('limit') ? 2 : key === 'auto_block_seconds' ? 0 : 60}
                max={key.endsWith('limit') ? 100 : key === 'auto_block_seconds' ? 2592000 : 86400}
                precision={0}
              />
            </Form.Item>
          ))}
          <Button type="primary" htmlType="submit" loading={busy}>
            {l('save')}
          </Button>
        </Form>
      </Space>
    </Card>
  );
}
