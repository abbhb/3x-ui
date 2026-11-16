import { useState } from 'react';
import { selectTagRender } from '@/components/form/SelectTag';
import { useQuery } from '@tanstack/react-query';
import { Alert, Button, Card, Form, Input, Select, Space, Switch, Typography } from 'antd';
import { z } from 'zod';
import { commerceRequest as request } from '@/api/commerce';
import { PolicySchema, PolicyNamesSchema, type Policy } from '@/schemas/subscriptionCompat';
import { useSubscriptionLabel } from './ExternalSources';

export default function SubscriptionPolicy() {
  const l = useSubscriptionLabel();
  const value = useQuery({
    queryKey: ['subscriptionPolicy'],
    queryFn: () => request('subscriptions/policy', PolicySchema),
  });
  return (
    <div className="commerce-stack">
      <Alert showIcon type="info" title={l('policyHelp')} />
      {value.error && <Alert showIcon type="error" title={l('failed')} />}
      {value.data && (
        <PolicyEditor key={value.data.revision} value={value.data} saved={() => value.refetch()} />
      )}
    </div>
  );
}
const RuleLinesSchema = z.array(z.string());
function policyNames(raw: string) {
  try {
    return PolicyNamesSchema.parse(JSON.parse(raw))['proxy-groups'].map((g) => g.name);
  } catch {
    return [];
  }
}
function policyRules(raw: string) {
  try {
    const doc: unknown = JSON.parse(raw);
    return z.object({ rules: RuleLinesSchema }).parse(doc).rules.join('\n');
  } catch {
    return '';
  }
}
function PolicyEditor({ value, saved }: { value: Policy; saved: () => Promise<unknown> }) {
  const l = useSubscriptionLabel();
  const [form] = Form.useForm();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState(false);
  const [success, setSuccess] = useState(false);
  const document: string = Form.useWatch('document', form) || value.document;
  const names = policyNames(document);
  return (
    <Card title={l('policy')}>
      {error && <Alert showIcon type="error" title={l('policySaveFailed')} />}
      {success && <Alert showIcon type="success" title={l('saved')} />}
      <Form
        form={form}
        layout="vertical"
        initialValues={{ ...value, rules: policyRules(value.document) }}
        disabled={busy}
        onFinish={async (v) => {
          setBusy(true);
          setError(false);
          setSuccess(false);
          try {
            const doc = z.record(z.string(), z.unknown()).parse(JSON.parse(v.document));
            doc.rules = String(v.rules)
              .split('\n')
              .map((line) => line.trim())
              .filter(Boolean);
            await request(
              'subscriptions/policy',
              PolicySchema,
              { ...v, revision: value.revision, document: JSON.stringify(doc) },
              'PUT',
            );
            setSuccess(true);
            await saved();
          } catch {
            setError(true);
          } finally {
            setBusy(false);
          }
        }}
      >
        <Form.Item name="enabled" label={l('enablePolicy')} valuePropName="checked">
          <Switch />
        </Form.Item>
        <Form.Item name="primary_group" label={l('primaryGroup')} rules={[{ required: true }]}>
          <Select options={names.map((value) => ({ value, label: value }))} />
        </Form.Item>
        <Form.Item
          name="secure_dns"
          label={l('secureDNS')}
          valuePropName="checked"
          extra={l('secureDNSHelp')}
        >
          <Switch />
        </Form.Item>
        <Form.Item
          name="rules"
          label={l('rules')}
          extra={l('rulesHelp')}
          rules={[{ required: true }]}
        >
          <Input.TextArea rows={14} maxLength={1000000} spellCheck={false} />
        </Form.Item>
        <Form.Item name="group_order" label={l('groupOrder')} extra={l('groupOrderHelp')}>
          <Select
            mode="multiple"
            tagRender={selectTagRender}
            options={names
              .filter((name) => name !== 'MGMA · External')
              .map((value) => ({ value, label: value }))}
          />
        </Form.Item>
        <Form.Item
          name="document"
          label={l('templateJSON')}
          extra={l('templateHelp')}
          rules={[{ required: true }]}
        >
          <Input.TextArea rows={14} maxLength={1000000} spellCheck={false} />
        </Form.Item>
        <Typography.Paragraph type="secondary">{l('policyBoundary')}</Typography.Paragraph>
        <Space>
          <Button type="primary" htmlType="submit" loading={busy}>
            {l('savePolicy')}
          </Button>
          <Button onClick={() => form.resetFields()} disabled={busy}>
            {l('resetDraft')}
          </Button>
        </Space>
      </Form>
    </Card>
  );
}
