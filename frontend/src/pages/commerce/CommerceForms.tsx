import { useCallback, useMemo, useState } from 'react';
import { HostChoiceSchema } from '@/generated/zod';
import { useQuery } from '@tanstack/react-query';
import { useTranslation } from 'react-i18next';
import { Alert, Button, Card, Form, Input, InputNumber, Select, Space, Switch } from 'antd';
import { z } from 'zod';
import { PlanEntryPicker } from './PlanEntryPicker';
import PlanSubscriptionFields from '@/pages/subscriptions/PlanSubscriptionFields';
import { commerceRequest as request, emptyResponse } from '@/api/commerce';
import { useInboundOptions } from '@/api/queries/useInboundOptions';
import { PlanSchema, type Plan, type Settings, type SettingsViewSchema } from '@/schemas/commerce';
import { planEntryCatalog, planGroupMembers, resolvePlanEntries } from './planEntryGrants';
export const useLabel = () => {
  const { t } = useTranslation();
  return useCallback((key: string) => t(`pages.commerce.${key}`), [t]);
};
export function PlanEditor({ plan, close }: { plan: Plan | null; close: () => void }) {
  const l = useLabel();
  const [error, setError] = useState(false);
  const [busy, setBusy] = useState(false);
  const inbounds = useInboundOptions();
  const hosts = useQuery({
    queryKey: ['commerce', 'hostOptions'],
    queryFn: () => request('subscriptions/catalog', z.array(HostChoiceSchema)),
  });
  const catalog = useMemo(
    () => planEntryCatalog(plan, hosts.data || [], inbounds.data || []),
    [plan, hosts.data, inbounds.data],
  );
  const [form] = Form.useForm();
  const entries: string[] = Form.useWatch('entries', form) ?? catalog.initial;
  const grantsFor = (keys: string[]) =>
    resolvePlanEntries(keys, catalog.byValue, plan?.inbound_ids);
  if (!hosts.data || !inbounds.data)
    return (
      <Card>
        <Alert
          type={hosts.error || inbounds.error ? 'error' : 'info'}
          title={hosts.error || inbounds.error ? l('failed') : l('hostRules')}
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
                entries: catalog.initial,
                external_selections: plan.external_selections || [],
                group_members: plan.group_members || {},
              }
            : {
                currency: 'CNY',
                price_minor: 0,
                duration_days: 30,
                data_limit: 0,
                entries: [],
                external_selections: [],
                group_members: {},
                is_visible: false,
              }
        }
        disabled={busy}
        onFinish={async (v) => {
          setBusy(true);
          setError(false);
          try {
            const { entries: selected, ...fields } = v;
            if (selected.some((key: string) => !catalog.byValue.has(key))) {
              form.setFields([{ name: 'entries', errors: [l('entryUnavailable')] }]);
              return;
            }
            const grants = grantsFor(selected);
            await request(
              plan ? `plans/${plan.id}` : 'plans',
              PlanSchema,
              {
                ...fields,
                ...grants,
                group_members: planGroupMembers(fields.group_members, grants.inbound_ids),
                description: v.description || '',
                currency: 'CNY',
              },
              plan ? 'PUT' : 'POST',
            );
            close();
          } catch {
            setError(true);
            void hosts.refetch();
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
        <Form.Item
          name="entries"
          label={l('hostRules')}
          dependencies={['external_selections']}
          rules={[
            {
              validator: (_, keys: string[] = []) => {
                if (keys.some((key) => !catalog.byValue.has(key)))
                  return Promise.reject(new Error(l('entryUnavailable')));
                const grants = grantsFor(keys);
                if (
                  !grants.host_ids.length &&
                  !grants.auto_host_inbound_ids.length &&
                  (keys.length || !form.getFieldValue('external_selections')?.length)
                )
                  return Promise.reject(new Error(l('entryRequired')));
                return Promise.resolve();
              },
            },
          ]}
        >
          <PlanEntryPicker catalog={catalog} />
        </Form.Item>
        <PlanSubscriptionFields inboundIDs={grantsFor(entries).inbound_ids} />
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
