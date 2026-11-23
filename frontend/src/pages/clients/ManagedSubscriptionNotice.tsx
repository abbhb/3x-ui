import { Alert, Button, Space } from 'antd';
import { useTranslation } from 'react-i18next';

export default function ManagedSubscriptionNotice() {
  const { t } = useTranslation();
  return (
    <Alert
      type="info"
      showIcon
      title={t('pages.clients.managedSubscriptionTitle')}
      description={
        <Space orientation="vertical">
          <span>{t('pages.clients.managedSubscriptionHint')}</span>
          <Button
            size="small"
            href={`${window.X_UI_BASE_PATH || '/'}portal/`}
            target="_blank"
            rel="noopener noreferrer"
          >
            {t('pages.commerce.openPortal')}
          </Button>
        </Space>
      }
    />
  );
}
