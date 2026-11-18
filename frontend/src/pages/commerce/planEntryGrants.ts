import type { HostChoice } from '@/generated/zod';
import type { InboundOption } from '@/schemas/client';
import type { Plan } from '@/schemas/commerce';

function hostKey(id: number, signature: string) {
  return `host:${id}:${signature}`;
}

type EntryOption = { value: string; label: string; inboundID: number; host?: HostChoice };

export function planEntryCatalog(
  plan: Plan | null,
  hosts: HostChoice[],
  inbounds: InboundOption[],
  label: (key: string) => string,
) {
  const supported = new Map(
    inbounds
      .filter((ib) => ib.enable && ['vless', 'hysteria'].includes(ib.protocol || ''))
      .map((ib) => [ib.id, ib]),
  );
  const inboundName = (id: number) => {
    const ib = supported.get(id);
    return `${ib?.remark || ib?.tag || label('inbound')} (#${id})`;
  };
  const hostByID = new Map(hosts.map((h) => [h.host_id, h]));
  const savedHosts = new Set(plan?.host_ids);
  const hostsToOffer = hosts.filter(
    (h) => supported.has(h.inbound_id) && (!h.disabled || savedHosts.has(h.host_id)),
  );
  const explicit: EntryOption[] = hostsToOffer.map((h) => ({
    value: hostKey(h.host_id, h.signature),
    label: [
      h.name,
      h.address !== h.name && h.address,
      `#${h.host_id}`,
      inboundName(h.inbound_id),
      h.disabled && label('disabled'),
    ]
      .filter(Boolean)
      .join(' · '),
    inboundID: h.inbound_id,
    host: h,
  }));
  const follow = Array.from(supported.keys(), (id) => ({
    value: `all:${id}`,
    label: `${label('autoHosts')} · ${inboundName(id)}`,
    inboundID: id,
  }));

  // Older plans can bind a client to an inbound without publishing any of its hosts.
  const covered = new Set(plan?.auto_host_inbound_ids);
  const initial = Array.from(savedHosts, (id) => {
    const h = hostByID.get(id);
    if (h) covered.add(h.inbound_id);
    const inSavedScope = h && plan?.inbound_ids.includes(h.inbound_id);
    return hostKey(id, inSavedScope ? plan?.host_signatures?.[String(id)] || h.signature : '');
  });
  initial.push(...(plan?.auto_host_inbound_ids || []).map((id) => `all:${id}`));
  const bindings = (plan?.inbound_ids || [])
    .filter((id) => !covered.has(id))
    .map((id) => ({
      value: `binding:${id}`,
      label: `${label('bindingOnly')} · ${inboundName(id)}`,
      inboundID: id,
    }));
  initial.push(...bindings.map((o) => o.value));
  const options = [
    { label: label('explicitEntries'), options: explicit },
    { label: label('autoHostsHelp'), options: follow },
    { label: label('bindingOnly'), options: bindings.filter((o) => supported.has(o.inboundID)) },
  ].filter((group) => group.options.length);
  const byValue = new Map(
    options.flatMap((group) => group.options).map((o: EntryOption) => [o.value, o]),
  );
  return { initial, options, byValue };
}

export function resolvePlanEntries(
  keys: string[],
  byValue: Map<string, EntryOption>,
  originalIDs: number[] = [],
) {
  const inboundIDs = new Set<number>();
  const hostIDs: number[] = [];
  const signatures: Record<string, string> = {};
  const autoIDs: number[] = [];
  for (const key of keys) {
    const entry = byValue.get(key);
    if (!entry) continue;
    inboundIDs.add(entry.inboundID);
    if (entry.host) {
      hostIDs.push(entry.host.host_id);
      signatures[entry.host.host_id] = entry.host.signature;
    } else if (key.startsWith('all:')) {
      autoIDs.push(entry.inboundID);
    }
  }
  // SavePlan compares ordered slices when deciding whether to reprovision subscribers.
  const orderedIDs = originalIDs.filter((id) => inboundIDs.has(id));
  return {
    inbound_ids: [...new Set([...orderedIDs, ...inboundIDs])],
    host_ids: hostIDs,
    host_signatures: signatures,
    auto_host_inbound_ids: autoIDs,
  };
}

export function planGroupMembers(members: Plan['group_members'], inboundIDs: number[]) {
  const allowed = new Set(inboundIDs.map((id) => `inbound:${id}`));
  return Object.fromEntries(
    Object.entries(members || {}).map(([name, refs]) => [
      name,
      refs.filter((ref) => !ref.startsWith('inbound:') || allowed.has(ref)),
    ]),
  );
}
