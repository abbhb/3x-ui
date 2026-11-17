import { useEffect } from 'react';
import { Link } from 'react-router';
import { Alert, Button, Input, Space } from 'antd';
import { useLabel } from './CommerceForms';
export function AccountClientLink({ clientID }: { clientID: number | null }) {
  const l = useLabel();
  return clientID ? (
    <Link to={`/clients?clientId=${clientID}`}>{l('viewClient')}</Link>
  ) : (
    <span>{l('noClient')}</span>
  );
}
export function SecretNotice({ secret, clear }: { secret: string; clear: () => void }) {
  const l = useLabel();
  useEffect(() => {
    const hide = () => {
      if (document.hidden) clear();
    };
    document.addEventListener('visibilitychange', hide);
    return () => document.removeEventListener('visibilitychange', hide);
  }, [clear]);
  return secret ? (
    <Alert
      type="success"
      showIcon
      title={l('oneTimeSecret')}
      description={
        <Space orientation="vertical" className="commerce-full">
          <Input.TextArea readOnly aria-label={l('oneTimeSecret')} value={secret} autoSize />
          <Space>
            <Button
              onClick={async () => {
                try {
                  await navigator.clipboard.writeText(secret);
                } catch {
                  /* Text remains selectable. */
                }
              }}
            >
              {l('copy')}
            </Button>
            <Button onClick={clear}>{l('hide')}</Button>
          </Space>
        </Space>
      }
    />
  ) : null;
}
export function PageButtons({
  page,
  count,
  change,
}: {
  page: number;
  count: number;
  change: (v: number) => void;
}) {
  const l = useLabel();
  return (
    <Space>
      <Button disabled={page <= 1} onClick={() => change(page - 1)}>
        {l('previous')}
      </Button>
      <span>{page}</span>
      <Button disabled={count < 100} onClick={() => change(page + 1)}>
        {l('next')}
      </Button>
    </Space>
  );
}
