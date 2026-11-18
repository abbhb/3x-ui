import type { HostChoice } from '@/generated/zod';
import type { InboundOption } from '@/schemas/client';
import type { Plan } from '@/schemas/commerce';

function hostKey(id: number, signature: string) {
  return `host:${id}:${signature}`;
}

type Grant = { inboundID: number; host?: HostChoice };
export type Entry = HostChoice & { key: string };
export type InboundEntries = {
  id: number;
  name: string;
  allKey: string;
  bindingKey?: string;
  entries: Entry[];
};

export function planEntryCatalog(
  plan: Plan | null,
  hosts: HostChoice[],
  inbounds: InboundOption[],
) {
  const groups = new Map<number, InboundEntries>();
  const byValue = new Map<string, Grant>();
  for (const ib of inbounds) {
    if (!ib.enable || !['vless', 'hysteria'].includes(ib.protocol || '')) continue;
    const allKey = `all:${ib.id}`;
    groups.set(ib.id, { id: ib.id, name: ib.remark || ib.tag || `#${ib.id}`, allKey, entries: [] });
    byValue.set(allKey, { inboundID: ib.id });
  }
  const savedHosts = new Set(plan?.host_ids);
  const hostByID = new Map(hosts.map((h) => [h.host_id, h]));
  for (const host of hosts) {
    const group = groups.get(host.inbound_id);
    if (!group) continue;
    const key = hostKey(host.host_id, host.signature);
    group.entries.push({ ...host, key });
    if (!host.disabled || savedHosts.has(host.host_id))
      byValue.set(key, { inboundID: group.id, host });
  }

  // Preserve old bindings that attached the client without publishing an entry.
  const covered = new Set(plan?.auto_host_inbound_ids);
  const initial = Array.from(savedHosts, (id) => {
    const h = hostByID.get(id);
    if (h) covered.add(h.inbound_id);
    const inSavedScope = h && plan?.inbound_ids.includes(h.inbound_id);
    return hostKey(id, inSavedScope ? plan?.host_signatures?.[String(id)] || h.signature : '');
  });
  initial.push(...(plan?.auto_host_inbound_ids || []).map((id) => `all:${id}`));
  for (const id of plan?.inbound_ids || []) {
    if (covered.has(id)) continue;
    const key = `binding:${id}`;
    initial.push(key);
    const group = groups.get(id);
    if (group) {
      group.bindingKey = key;
      byValue.set(key, { inboundID: id });
    }
  }
  return { initial, groups: [...groups.values()], byValue };
}
export type EntryCatalog = ReturnType<typeof planEntryCatalog>;

export function resolvePlanEntries(
  keys: string[],
  byValue: Map<string, Grant>,
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
