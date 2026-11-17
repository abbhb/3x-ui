import { Link } from 'react-router';
import { useTranslation } from 'react-i18next';
import { Alert, Card, Empty, Space, Table, Tag } from 'antd';
import { xrayPageUrl } from '@/hooks/xrayScope';
import type { fetchXrayConfig } from '@/hooks/useXraySetting';
import { nativeIPv6Bindings } from './native-bindings';

export default function NativeEgressPanel({
  nodeId,
  name,
  config,
  error,
  loading,
}: {
  nodeId: number;
  name: string;
  config?: Awaited<ReturnType<typeof fetchXrayConfig>>;
  error?: Error | null;
  loading: boolean;
}) {
  const { t } = useTranslation();
  const l = (key: string) => t(`pages.serviceHosts.${key}`);
  const rows = config ? nativeIPv6Bindings(config.xraySetting) : [];
  return (
    <Card title={name} size="small" loading={loading}>
      <Space orientation="vertical" size="middle" className="service-host-full">
        <Space>
          <Link to={xrayPageUrl('/outbound', nodeId)}>{t('menu.outbounds')}</Link>
          <Link to={xrayPageUrl('/routing', nodeId)}>{t('menu.routing')}</Link>
        </Space>
        {error ? (
          <Alert type="error" showIcon title={l('loadFailed')} description={error.message} />
        ) : null}
        {config?.runtime?.heldBack ? (
          <Alert type="warning" showIcon title={config.runtime.heldBack} />
        ) : null}
        {config && (
          <>
            <Alert type="info" showIcon title={l('nativeBindingsHelp')} />
            <Table
              rowKey="key"
              size="small"
              dataSource={rows}
              scroll={{ x: 850 }}
              pagination={{ pageSize: 10, showSizeChanger: false }}
              locale={{ emptyText: <Empty description={l('noNativeBindings')} /> }}
              columns={[
                {
                  title: l('user'),
                  dataIndex: 'users',
                  render: (v: string[]) => v.join(', ') || l('allUsers'),
                },
                {
                  title: l('line'),
                  dataIndex: 'inbounds',
                  render: (v: string[]) => v.join(', ') || '—',
                },
                { title: l('assignedIPv6'), dataIndex: 'address' },
                { title: l('outbound'), dataIndex: 'outbound' },
                {
                  title: l('target'),
                  dataIndex: 'destinations',
                  render: (v: string[]) => v.join(', ') || '—',
                },
                {
                  title: l('status'),
                  dataIndex: 'enabled',
                  render: (v: boolean) => <Tag>{l(v ? 'ruleEnabled' : 'ruleNotSelected')}</Tag>,
                },
              ]}
            />
          </>
        )}
      </Space>
    </Card>
  );
}
