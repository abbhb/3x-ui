import { useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import { Alert, Button, Collapse, Drawer, Flex, Table, Typography } from 'antd';
import { ClientSourcesSchema } from '@/generated/zod';
import ExternalSources, { useExternalRequest, useSubscriptionLabel } from './ExternalSources';

export default function ClientSubscriptionSources(
  scope: { clientId: number; token?: never } | { token: string; clientId?: never },
) {
  const { token, clientId } = scope;
  const l = useSubscriptionLabel();
  const request = useExternalRequest(token, clientId);
  const [editing, setEditing] = useState<number | null>(null);
  const inventory = useQuery({
    queryKey: ['clientExternalInventory', clientId ?? token],
    queryFn: () => request('external-inventory', ClientSourcesSchema),
  });
  const data = inventory.data;
  if (!data && !inventory.isError)
    return <Typography.Text role="status">{l('loadingAssignments')}</Typography.Text>;
  if (data && !data.linked && clientId) return null;
  const groups = (data?.sources ?? []).map((source) => ({
    key: source.id,
    label: `${source.name} (${source.nodes.length})`,
    extra: source.private ? l('privateSource') : data?.plan,
    children: (
      <>
        {clientId && !source.private && (
          <Button
            href={`${window.X_UI_BASE_PATH || '/'}admin/commerce?tab=external&source=${source.id}`}
            target="_blank"
            rel="noopener noreferrer"
          >
            {l('openSource')}
          </Button>
        )}
        {source.private && (
          <Button onClick={() => setEditing(source.id)}>{l('editPrivateSource')}</Button>
        )}
        <Table
          size="small"
          rowKey="id"
          pagination={{ pageSize: 10, hideOnSinglePage: true }}
          dataSource={source.nodes}
          columns={[
            { title: l('name'), dataIndex: 'name' },
            { title: l('protocol'), dataIndex: 'type' },
            {
              title: l('formats'),
              render: (_, node) => (node.share_link ? 'Mihomo · URI · Xray JSON' : 'Mihomo'),
            },
          ]}
        />
      </>
    ),
  }));
  return (
    <Flex vertical gap="small">
      <Typography.Text strong>{l('assignedSources')}</Typography.Text>
      {inventory.isError && (
        <Alert
          type="error"
          showIcon
          title={l('failed')}
          action={<Button onClick={() => void inventory.refetch()}>{l('reload')}</Button>}
        />
      )}
      {groups.length > 0 && <Collapse size="small" items={groups} />}
      {token && data && data.direct.length > 0 && (
        <div>
          <Typography.Text strong>{l('assignedDirect')}</Typography.Text>
          <ul>
            {data.direct.map((item, i) => (
              <li key={i}>
                {item.remark || l(item.kind === 'link' ? 'directNode' : 'remoteSubscription')}
              </li>
            ))}
          </ul>
        </div>
      )}
      {data && !groups.length && (!token || !data.direct.length) && (
        <Typography.Text type="secondary">{l('noCatalogGrants')}</Typography.Text>
      )}
      <Button style={{ alignSelf: 'flex-start' }} onClick={() => setEditing(0)}>
        {l('managePrivateSources')}
      </Button>
      <Drawer
        open={editing !== null}
        title={l('managePrivateSources')}
        destroyOnHidden
        size="large"
        onClose={() => {
          setEditing(null);
          void inventory.refetch();
        }}
      >
        {editing !== null && (
          <div onSubmit={(event) => event.stopPropagation()}>
            <ExternalSources
              token={token}
              clientId={clientId}
              selected={editing}
              select={setEditing}
            />
          </div>
        )}
      </Drawer>
    </Flex>
  );
}
