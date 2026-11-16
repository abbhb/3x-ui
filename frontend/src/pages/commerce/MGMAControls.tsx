import { useEffect, useState } from 'react';
import { Alert, Button, Card, Input, Popconfirm, QRCode, Space, Typography } from 'antd';
import { useTranslation } from 'react-i18next';
import type { Issued } from '@/schemas/commerce';

export default function MGMAControls({
  issue,
  revoke,
  regenerate,
  disabled = false,
}: {
  issue: () => Promise<Issued>;
  revoke: () => Promise<unknown>;
  regenerate: () => Promise<unknown>;
  disabled?: boolean;
}) {
  const { t } = useTranslation();
  const l = (key: string) => t(`pages.commerce.${key}`);
  const [grant, setGrant] = useState<Issued | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState(false);
  const [now, setNow] = useState(Date.now);
  const [copied, setCopied] = useState(false);
  useEffect(() => {
    const timer = setInterval(() => {
      const time = Date.now();
      setNow(time);
      setGrant((current) =>
        current?.expires_at && Date.parse(current.expires_at) <= time ? null : current,
      );
    }, 1000);
    const hide = () => {
      if (document.hidden) setGrant(null);
    };
    document.addEventListener('visibilitychange', hide);
    return () => {
      clearInterval(timer);
      document.removeEventListener('visibilitychange', hide);
    };
  }, []);
  async function run(action: () => Promise<unknown>, issuance = false) {
    setGrant(null);
    setCopied(false);
    setBusy(true);
    setError(false);
    try {
      const value = await action();
      if (issuance) setGrant(value as Issued);
    } catch {
      setError(true);
    } finally {
      setBusy(false);
    }
  }
  return (
    <Card title={l('subscriptionLink')}>
      <Space orientation="vertical" size="middle" className="commerce-full">
        <Typography.Paragraph>{l('linkHelp')}</Typography.Paragraph>
        {error && <Alert type="error" showIcon title={l('failed')} />}
        <Space wrap>
          <Button
            aria-label={l('issueLink')}
            type="primary"
            disabled={disabled}
            loading={busy}
            onClick={() => void run(issue, true)}
          >
            {l('issueLink')}
          </Button>
          <Button disabled={disabled || busy} onClick={() => void run(revoke)}>
            {l('revokeLink')}
          </Button>
          <Popconfirm
            title={l('regenerateConfirm')}
            onConfirm={() => run(regenerate)}
            okText={l('confirm')}
            cancelText={l('cancel')}
          >
            <Button danger disabled={disabled || busy}>
              {l('regenerate')}
            </Button>
          </Popconfirm>
        </Space>
        {grant && (
          <section aria-label={l('subscriptionLink')} className="commerce-secret">
            <Typography.Paragraph>
              {grant.expires_at
                ? t('pages.commerce.linkCountdown', {
                    seconds: Math.max(0, Math.ceil((Date.parse(grant.expires_at) - now) / 1000)),
                  })
                : l('permanentLink')}
            </Typography.Paragraph>
            <Input.TextArea
              aria-label={l('subscriptionLink')}
              value={grant.url}
              readOnly
              autoSize={{ minRows: 2, maxRows: 5 }}
            />
            <QRCode type="svg" aria-hidden value={grant.url} size={180} />
            <Space wrap>
              <Button
                onClick={async () => {
                  try {
                    await navigator.clipboard.writeText(grant.url);
                    setCopied(true);
                  } catch {
                    setError(true);
                  }
                }}
              >
                {copied ? l('copied') : l('copy')}
              </Button>
              <Button onClick={() => setGrant(null)}>{l('hide')}</Button>
            </Space>
          </section>
        )}
      </Space>
    </Card>
  );
}
