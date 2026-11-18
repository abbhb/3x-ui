import { useState } from 'react';
import { selectTagRender } from '@/components/form/SelectTag';
import { useQuery } from '@tanstack/react-query';
import { Button, Card, Form, Input, Select, Space, Typography } from 'antd';
import { z } from 'zod';
import { commerceRequest as request } from '@/api/commerce';
import {
  SourceSchema,
  NodeSchema,
  PolicySchema,
  PolicyNamesSchema,
} from '@/schemas/subscriptionCompat';
import { planGroupMembers } from '@/pages/commerce/planEntryGrants';
import { useSubscriptionLabel } from './ExternalSources';

export default function PlanSubscriptionFields({ inboundIDs }: { inboundIDs: number[] }) {
  const l = useSubscriptionLabel();
  const [search, setSearch] = useState('');
  const sources = useQuery({
    queryKey: ['planExternalSources', search],
    queryFn: () =>
      request(`external-subscriptions?search=${encodeURIComponent(search)}`, z.array(SourceSchema)),
  });
  return (
    <Card size="small" title={l('planExternal')}>
      <Typography.Paragraph>{l('planExternalHelp')}</Typography.Paragraph>
      <Input.Search
        aria-label={l('searchPublicSource')}
        placeholder={l('searchPublicSource')}
        onSearch={setSearch}
        allowClear
      />
      <Form.List name="external_selections">
        {(fields, { add, remove }) => (
          <Space orientation="vertical" className="commerce-full">
            {fields.map((field) => (
              <SourceSelection
                key={field.key}
                index={field.name}
                sources={(sources.data || [])
                  .filter((s) => s.owner_account_id == null)
                  .map((s) => ({ value: s.id, label: `${s.name} · #${s.id}` }))}
                remove={() => remove(field.name)}
              />
            ))}
            <Button onClick={() => add({ mode: 'all', node_ids: [] })}>
              {l('addSourceGrant')}
            </Button>
          </Space>
        )}
      </Form.List>
      <Form.Item name="group_members" label={l('planGroups')} extra={l('planGroupsHelp')}>
        <GroupMembers inboundIDs={inboundIDs} />
      </Form.Item>
    </Card>
  );
}
function SourceSelection({
  index,
  sources,
  remove,
}: {
  index: number;
  sources: { value: number; label: string }[];
  remove: () => void;
}) {
  const l = useSubscriptionLabel();
  const form = Form.useFormInstance();
  const source: number = Form.useWatch(['external_selections', index, 'source_id'], form) || 0;
  const mode: string = Form.useWatch(['external_selections', index, 'mode'], form) || 'all';
  const [page, setPage] = useState(1);
  const nodes = useQuery({
    queryKey: ['planSourceNodes', source, page],
    queryFn: () =>
      request(`external-subscriptions/${source}/nodes?page=${page}`, z.array(NodeSchema)),
    enabled: source > 0 && mode === 'selected',
  });
  return (
    <Card
      size="small"
      extra={
        <Button danger onClick={remove}>
          {l('remove')}
        </Button>
      }
    >
      <Form.Item name={[index, 'source_id']} label={l('publicSource')} rules={[{ required: true }]}>
        <Select
          showSearch
          optionFilterProp="label"
          options={sources}
          onChange={() => {
            form.setFieldValue(['external_selections', index, 'node_ids'], []);
            setPage(1);
          }}
        />
      </Form.Item>
      <Form.Item name={[index, 'mode']} label={l('grantMode')}>
        <Select
          options={[
            { value: 'all', label: l('allFutureNodes') },
            { value: 'selected', label: l('selectedNodes') },
          ]}
          onChange={(value) => {
            if (value === 'all') form.setFieldValue(['external_selections', index, 'node_ids'], []);
          }}
        />
      </Form.Item>
      {mode === 'selected' && (
        <>
          <Form.Item
            name={[index, 'node_ids']}
            label={l('nodes')}
            rules={[{ required: true, type: 'array', min: 1 }]}
          >
            <Select
              mode="multiple"
              tagRender={selectTagRender}
              showSearch
              optionFilterProp="label"
              loading={nodes.isFetching}
              options={(nodes.data || []).map((n) => ({
                value: n.id,
                label: `#${n.id} · ${n.name}${n.is_active ? '' : ` · ${l('missing')}`}`,
              }))}
            />
          </Form.Item>
          <Space>
            <Button disabled={page === 1} onClick={() => setPage(page - 1)}>
              {l('previous')}
            </Button>
            <span>{page}</span>
            <Button disabled={(nodes.data?.length || 0) < 100} onClick={() => setPage(page + 1)}>
              {l('next')}
            </Button>
          </Space>
        </>
      )}
    </Card>
  );
}
function GroupMembers({
  value,
  onChange,
  inboundIDs,
}: {
  value?: Record<string, string[]> | null;
  onChange?: (v: Record<string, string[]>) => void;
  inboundIDs: number[];
}) {
  const l = useSubscriptionLabel();
  const [next, setNext] = useState<string>();
  const policy = useQuery({
    queryKey: ['subscriptionPolicy'],
    queryFn: () => request('subscriptions/policy', PolicySchema),
  });
  let groups: string[] = [];
  try {
    groups = PolicyNamesSchema.parse(JSON.parse(policy.data?.document || '{}'))
      ['proxy-groups'].map((g) => g.name)
      .filter((n) => n !== 'MGMA · External');
  } catch {
    groups = [];
  }
  const members = value || {};
  const visibleMembers = planGroupMembers(members, inboundIDs);
  return (
    <Space orientation="vertical" className="commerce-full">
      {Object.entries(visibleMembers).map(([name, selected]) => (
        <div key={name}>
          <Typography.Text>{name}</Typography.Text>
          <Space wrap>
            <Select
              aria-label={`${l('planGroups')} · ${name}`}
              mode="multiple"
              tagRender={selectTagRender}
              style={{ minWidth: 250 }}
              value={selected}
              options={[
                { value: 'external', label: 'MGMA · External' },
                ...inboundIDs.map((id) => ({
                  value: `inbound:${id}`,
                  label: `${l('inbound')} #${id}`,
                })),
              ]}
              onChange={(refs) => onChange?.({ ...members, [name]: refs })}
            />
            <Button
              danger
              onClick={() => {
                const copy = { ...members };
                delete copy[name];
                onChange?.(copy);
              }}
            >
              {l('remove')}
            </Button>
          </Space>
        </div>
      ))}
      <Space wrap>
        <Select
          aria-label={l('chooseGroup')}
          placeholder={l('chooseGroup')}
          style={{ minWidth: 250 }}
          value={next}
          options={groups
            .filter((name) => !(name in members))
            .map((value) => ({ value, label: value }))}
          onChange={setNext}
        />
        <Button
          disabled={!next}
          onClick={() => {
            if (next) {
              onChange?.({ ...members, [next]: [] });
              setNext(undefined);
            }
          }}
        >
          {l('addGroup')}
        </Button>
      </Space>
    </Space>
  );
}
