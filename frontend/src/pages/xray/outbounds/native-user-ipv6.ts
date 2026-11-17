import type { XraySettingsValue } from '@/hooks/useXraySetting';
import type { NativeIPv6Client, NativeIPv6Address } from '@/generated/types';

interface NativeOutbound extends Record<string, unknown> {
  tag: string;
  protocol: string;
  sendThrough?: string;
  proxySettings?: unknown;
  targetStrategy?: string;
  settings?: Record<string, unknown>;
  streamSettings?: { security?: string; sockopt?: Record<string, unknown> } & Record<
    string,
    unknown
  >;
}
interface NativeRule extends Record<string, unknown> {
  enabled?: boolean;
  inboundTag?: string[];
  outboundTag?: string;
}
interface NativeTemplate extends Record<string, unknown> {
  outbounds: NativeOutbound[];
  routing?: { script?: string; rules?: NativeRule[]; domainStrategy?: string } & Record<
    string,
    unknown
  >;
}

// A convenience editor for ordinary native objects, not a second policy store.
// Only the final direct branch is changed; preceding restrictions and tunnels
// keep their first-match order. Complex policies remain editable in Routing.
export function planNativeUserIPv6(
  template: XraySettingsValue,
  inbound: string,
  clients: NativeIPv6Client[],
  hasInjectedOutbounds = false,
) {
  const next = JSON.parse(JSON.stringify(template)) as NativeTemplate;
  const routing = next.routing ?? { rules: [] };
  const rules = routing.rules ?? [];
  if (!inbound || !clients.length || clients.some((c) => !c.inbounds.includes(inbound)))
    throw new Error('pages.xray.user6.selectClients');
  if (routing.script?.trim()) throw new Error('pages.xray.user6.manualRouting');
  const catchAllKeys = new Set([
    'type',
    'enabled',
    'comment',
    'ruleTag',
    'inboundTag',
    'outboundTag',
    'balancerTag',
  ]);
  let at = rules.findIndex(
    (r) =>
      r.enabled !== false &&
      Object.entries(r).every(
        ([k, value]) =>
          catchAllKeys.has(k) ||
          value == null ||
          value === '' ||
          (Array.isArray(value) && value.length === 0),
      ) &&
      (!r.inboundTag?.length || r.inboundTag.includes(inbound)),
  );
  if (at < 0 && hasInjectedOutbounds) throw new Error('pages.xray.user6.explicitDirect');
  const baseTag = at >= 0 ? rules[at].outboundTag : next.outbounds?.[0]?.tag;
  if (at < 0) at = rules.length;
  const base = next.outbounds?.find((o) => o.tag === baseTag);
  const socket = base?.streamSettings?.sockopt ?? {};
  if (
    !base ||
    typeof base.protocol !== 'string' ||
    base.protocol.toLowerCase() !== 'freedom' ||
    base.sendThrough ||
    base.proxySettings ||
    base.settings?.redirect ||
    socket.mark ||
    socket.dialerProxy ||
    socket.interface ||
    (base.streamSettings?.security && base.streamSettings.security !== 'none')
  )
    throw new Error('pages.xray.user6.directOnly');
  const tags = clients.map((c) => `user6-${c.id}-${inbound}`);
  if (
    next.outbounds.some((o) => tags.includes(o.tag)) ||
    rules.some((r) => typeof r.ruleTag === 'string' && tags.includes(r.ruleTag))
  )
    throw new Error('pages.xray.user6.alreadyExists');
  return (leases: NativeIPv6Address[]): XraySettingsValue => {
    const additions = clients.map((client, i) => {
      const lease = leases.find((l) => l.clientId === client.id && l.ready);
      if (!lease || !lease.address.includes(':')) throw new Error('pages.xray.user6.notReady');
      const outbound = JSON.parse(JSON.stringify(base)) as NativeOutbound;
      outbound.tag = tags[i];
      outbound.sendThrough = lease.address;
      delete outbound.targetStrategy;
      outbound.settings ??= {};
      delete outbound.settings.domainStrategy;
      delete outbound.settings.targetStrategy;
      outbound.streamSettings ??= {};
      outbound.streamSettings.sockopt ??= {};
      outbound.streamSettings.sockopt.domainStrategy = 'ForceIPv6';
      next.outbounds.push(outbound);
      return {
        type: 'field',
        ruleTag: tags[i],
        comment: `${client.email} → ${lease.address}`,
        inboundTag: [inbound],
        user: [client.email],
        ip: ['::/0'],
        outboundTag: tags[i],
      };
    });
    rules.splice(at, 0, ...additions);
    next.routing = { ...routing, domainStrategy: 'IPOnDemand', rules };
    return next as XraySettingsValue;
  };
}
