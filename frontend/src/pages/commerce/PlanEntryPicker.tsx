import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Alert, Button, Checkbox, Empty, Input, Modal, Pagination, Typography } from 'antd';
import { SearchOutlined } from '@ant-design/icons';
import type { EntryCatalog, InboundEntries } from './planEntryGrants';
import './PlanEntryPicker.css';

function coverage(group: InboundEntries, keys: Set<string>) {
  const all = keys.has(group.allKey);
  let total = 0;
  let included = 0;
  for (const entry of group.entries) {
    if (entry.disabled) continue;
    total++;
    if (all || keys.has(entry.key)) included++;
  }
  return { included, total };
}

type Props = {
  catalog: EntryCatalog;
  value?: string[];
  onChange?: (keys: string[]) => void;
  id?: string;
  'aria-describedby'?: string;
  'aria-invalid'?: boolean;
};

export function PlanEntryPicker({ catalog, value = [], onChange, ...control }: Props) {
  const { t: label } = useTranslation(undefined, { keyPrefix: 'pages.commerce' });
  const [draft, setDraft] = useState<string[] | null>(null);
  const saved = new Set(value);
  const included = catalog.groups.reduce((sum, group) => sum + coverage(group, saved).included, 0);
  const followed = catalog.groups.filter((group) => saved.has(group.allKey)).length;
  const unavailable = value.filter((key) => !catalog.byValue.has(key)).length;
  return (
    <>
      <div className="plan-entry-field">
        <div className="plan-entry-summary">
          <strong>{label('entryCount', { count: included })}</strong>
          {followed > 0 && <span>{label('followingCount', { count: followed })}</span>}
          {unavailable > 0 && (
            <Typography.Text type="warning">
              {label('entryUnavailableLabel')} ({unavailable})
            </Typography.Text>
          )}
        </div>
        <Button
          {...control}
          aria-label={label('chooseEntries')}
          aria-haspopup="dialog"
          onClick={() => setDraft([...value])}
        >
          {label('chooseEntries')}
        </Button>
      </div>
      <Modal
        open={draft !== null}
        title={label('chooseEntries')}
        width={880}
        centered
        className="plan-entry-modal"
        destroyOnHidden
        okText={label('confirm')}
        cancelText={label('cancel')}
        onCancel={() => setDraft(null)}
        onOk={() => {
          if (draft) onChange?.(draft);
          setDraft(null);
        }}
      >
        {draft && <EntryList catalog={catalog} draft={draft} onChange={setDraft} />}
      </Modal>
    </>
  );
}

const pageSize = 20;

function EntryList({
  catalog,
  draft,
  onChange,
}: {
  catalog: EntryCatalog;
  draft: string[];
  onChange: (keys: string[]) => void;
}) {
  const { t: label } = useTranslation(undefined, { keyPrefix: 'pages.commerce' });
  const [query, setQuery] = useState('');
  const [onlyIncluded, setOnlyIncluded] = useState(false);
  const [activeID, setActiveID] = useState(
    () => catalog.byValue.get(draft.find((key) => catalog.byValue.has(key)) || '')?.inboundID,
  );
  const [page, setPage] = useState(1);
  const keys = new Set(draft);
  const term = query.trim().toLowerCase();
  const matches = catalog.groups
    .map((group) => {
      const all = keys.has(group.allKey);
      const bound = !!group.bindingKey && keys.has(group.bindingKey);
      const groupMatches = `${group.name} #${group.id}`.toLowerCase().includes(term);
      const entries = group.entries.filter(
        (entry) =>
          (!onlyIncluded || all || keys.has(entry.key)) &&
          (groupMatches ||
            `${entry.name} ${entry.address} #${entry.host_id}`.toLowerCase().includes(term)),
      );
      return { group, entries, all, bound, groupMatches, ...coverage(group, keys) };
    })
    .filter((m) => m.entries.length || (m.groupMatches && (!onlyIncluded || m.all || m.bound)));
  const active = matches.find((m) => m.group.id === activeID) || matches[0];
  const currentPage = Math.min(
    page,
    Math.max(1, Math.ceil((active?.entries.length || 0) / pageSize)),
  );
  const missing = draft.filter((key) => !catalog.byValue.has(key));
  function toggle(key: string) {
    onChange(keys.has(key) ? draft.filter((value) => value !== key) : [...draft, key]);
  }
  return (
    <div className="plan-entry-picker">
      <div className="plan-entry-tools">
        <Input
          allowClear
          prefix={<SearchOutlined aria-hidden />}
          placeholder={label('entrySearch')}
          aria-label={label('entrySearch')}
          value={query}
          onChange={(event) => {
            setQuery(event.target.value);
            setPage(1);
          }}
        />
        <Checkbox
          checked={onlyIncluded}
          onChange={(event) => {
            setOnlyIncluded(event.target.checked);
            setPage(1);
          }}
        >
          {label('onlyIncluded')}
        </Checkbox>
      </div>
      {missing.length > 0 && (
        <Alert
          type="warning"
          showIcon
          title={label('entryUnavailable')}
          description={
            <div className="plan-entry-stale">
              {missing.map((key) => (
                <Button key={key} size="small" onClick={() => toggle(key)}>
                  {label('remove')} · {label('entryUnavailableLabel')} #{key.split(':')[1]}
                </Button>
              ))}
            </div>
          }
        />
      )}
      {active ? (
        <div className="plan-entry-layout">
          <nav className="plan-entry-inbounds" aria-label={label('inbound')}>
            {matches.map(({ group, included, total }) => (
              <button
                type="button"
                key={group.id}
                aria-pressed={group.id === active.group.id}
                onClick={() => {
                  setActiveID(group.id);
                  setPage(1);
                }}
              >
                <span>
                  {group.name}
                  <small>#{group.id}</small>
                </span>
                <span
                  className="plan-entry-ratio"
                  aria-label={label('coverageCount', { included, total })}
                >
                  {included}/{total}
                </span>
              </button>
            ))}
          </nav>
          <section
            className="plan-entry-detail"
            aria-label={active.group.name}
            key={active.group.id}
          >
            <div className="plan-entry-heading">
              <strong>{active.group.name}</strong>
              <Typography.Text type="secondary">
                {label('coverageCount', { included: active.included, total: active.total })}
              </Typography.Text>
            </div>
            <div className="plan-entry-follow">
              <Checkbox checked={active.all} onChange={() => toggle(active.group.allKey)}>
                {label('autoHostsHelp')}
              </Checkbox>
              <small>{label('followHint')}</small>
            </div>
            {active.group.bindingKey && (
              <Checkbox checked={active.bound} onChange={() => toggle(active.group.bindingKey!)}>
                {label('bindingOnly')}
              </Checkbox>
            )}
            <div className="plan-entry-rows">
              {active.entries
                .slice((currentPage - 1) * pageSize, currentPage * pageSize)
                .map((entry) => {
                  const direct = keys.has(entry.key);
                  const covered = active.all || direct;
                  const status = active.all
                    ? direct
                      ? 'entryBoth'
                      : 'entryInherited'
                    : direct
                      ? 'entryDirect'
                      : 'entryUnselected';
                  return (
                    <div className="plan-entry-row" key={entry.key}>
                      <Checkbox
                        checked={covered}
                        disabled={active.all || !catalog.byValue.has(entry.key)}
                        onChange={() => toggle(entry.key)}
                      >
                        <span className="plan-entry-title">
                          {entry.name || entry.address || `#${entry.host_id}`}
                        </span>
                        <small>
                          {[entry.address, `#${entry.host_id}`, entry.disabled && label('disabled')]
                            .filter(Boolean)
                            .join(' · ')}
                        </small>
                      </Checkbox>
                      <span className="plan-entry-status" data-covered={covered}>
                        {label(status)}
                      </span>
                    </div>
                  );
                })}
              {!active.entries.length && (
                <Empty
                  image={Empty.PRESENTED_IMAGE_SIMPLE}
                  description={label('noMatchingEntries')}
                />
              )}
            </div>
            {active.entries.length > pageSize && (
              <Pagination
                size="small"
                current={currentPage}
                pageSize={pageSize}
                total={active.entries.length}
                showSizeChanger={false}
                onChange={setPage}
              />
            )}
          </section>
        </div>
      ) : (
        <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description={label('noMatchingEntries')} />
      )}
    </div>
  );
}
