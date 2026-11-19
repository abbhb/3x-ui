import { useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Alert, Button, Input, Popconfirm, Space, Spin, Typography } from 'antd';
import { HttpUtil } from '@/utils';

interface Dialer {
  revision: string;
  config?: string;
  name?: string;
  type?: string;
  server?: string;
  port?: number;
}

export default function SubscriptionDialer({ id, landing }: { id: number; landing: string }) {
  const { t } = useTranslation();
  const label = (key: string) => t(`pages.inbounds.dialer.${key}`);
  const url = `/panel/api/inbounds/${id}/subscriptionDialer`;
  const [view, setView] = useState<Dialer>();
  const [edit, setEdit] = useState<string>();
  const stored = view?.config ?? '';
  const draft = edit ?? stored;
  const [busy, setBusy] = useState(false);
  const [feedback, setFeedback] = useState<'invalid' | 'conflict' | 'failed' | 'saved'>();
  const [attempt, setAttempt] = useState(0);

  useEffect(() => {
    let active = true;
    void HttpUtil.get<Dialer>(url, { includeConfig: true })
      .then((msg) => {
        if (!active) return;
        if (msg?.success && msg.obj) {
          setView(msg.obj);
          setEdit(undefined);
        } else setFeedback('failed');
      })
      .catch(() => {
        if (active) setFeedback('failed');
      });
    return () => {
      active = false;
    };
  }, [url, attempt]);

  async function save(remove: boolean) {
    if (!view || busy) return;
    setBusy(true);
    setFeedback(undefined);
    try {
      const change = remove ? { remove: true } : { config: draft };
      const msg = await HttpUtil.post<Dialer>(
        url,
        { revision: view.revision, ...change },
        {
          headers: { 'Content-Type': 'application/json' },
        },
      );
      if (msg?.success && msg.obj) {
        setView({ ...msg.obj, config: remove ? undefined : draft });
        setEdit(undefined);
        setFeedback('saved');
      } else if (msg?.msg?.includes('conflict')) setFeedback('conflict');
      else if (msg?.msg?.includes('invalid')) setFeedback('invalid');
      else setFeedback('failed');
    } catch {
      setFeedback('failed');
    } finally {
      setBusy(false);
    }
  }

  return (
    <Space orientation="vertical" size="middle" style={{ width: '100%' }}>
      {view && (
        <>
          <Typography.Text strong>
            {label('client')} → {view.name ? `${view.name} → ` : ''}
            {landing}
          </Typography.Text>
          <Typography.Text>
            {view.server
              ? `${view.type?.toUpperCase()} · ${view.server} · ${view.port}`
              : label('direct')}
          </Typography.Text>
          <div>
            <label htmlFor="inbound-dialer-config">{label('input')}</label>
            <Input.TextArea
              id="inbound-dialer-config"
              value={draft}
              onChange={(e) => {
                setEdit(e.target.value);
                if (feedback === 'saved' || feedback === 'invalid') setFeedback(undefined);
              }}
              autoComplete="off"
              spellCheck={false}
              autoSize={{ minRows: 6, maxRows: 14 }}
              disabled={busy}
              placeholder={
                view.name
                  ? undefined
                  : '- name: Hong Kong\n  type: socks5\n  server: hop.example.org\n  port: 1080'
              }
            />
          </div>
          <Typography.Text type="secondary">{label('hint')}</Typography.Text>
          <Space>
            <Button
              type="primary"
              disabled={!draft.trim() || draft === stored || busy || feedback === 'conflict'}
              loading={busy}
              onClick={() => void save(false)}
            >
              {label('save')}
            </Button>
            {view.name && (
              <Popconfirm title={label('confirm')} onConfirm={() => save(true)}>
                <Button danger disabled={busy || feedback === 'conflict'}>
                  {label('remove')}
                </Button>
              </Popconfirm>
            )}
          </Space>
        </>
      )}
      {!view && !feedback && <Spin />}
      {feedback && (
        <Alert
          type={feedback === 'saved' ? 'success' : 'error'}
          title={label(feedback)}
          action={
            (feedback === 'failed' || feedback === 'conflict') && (
              <Button
                onClick={() => {
                  setView(undefined);
                  setFeedback(undefined);
                  setAttempt((v) => v + 1);
                }}
              >
                {label('reload')}
              </Button>
            )
          }
        />
      )}
    </Space>
  );
}
