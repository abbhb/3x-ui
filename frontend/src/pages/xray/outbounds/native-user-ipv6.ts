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

export function nativeUserIPv6AutoEnabled(template: XraySettingsValue, inbound: string) {
  return template.routing?.rules?.some(
    (rule) => rule.ruleTag === `user6-auto-${inbound}` && rule.enabled !== false,
  );
}

export function disableNativeUserIPv6Auto(template: XraySettingsValue, inbound: string) {
  const next = JSON.parse(JSON.stringify(template)) as XraySettingsValue;
  if (next.routing?.rules)
    next.routing.rules = next.routing.rules.filter(
      (rule) =>
        rule.ruleTag !== `user6-auto-${inbound}` && rule.ruleTag !== `user6-pending-${inbound}`,
    );
  return next;
}

export function enableNativeUserIPv6Auto(
  template: XraySettingsValue,
  inbound: string,
  hasInjectedOutbounds = false,
): XraySettingsValue {
  if (!inbound) throw new Error('pages.xray.user6.selectClients');
  if (nativeUserIPv6AutoEnabled(template, inbound)) return template;
  // The same direct-branch validation as the manual editor. No leases are
  // allocated here: canonical native reconcile prepares newly granted clients.
  const next = disableNativeUserIPv6Auto(template, inbound) as NativeTemplate;
  const { routing, rules, at, base } = nativeIPv6DirectBranch(next, inbound, hasInjectedOutbounds);
  let blocked = next.outbounds.find((outbound) => outbound.protocol === 'blackhole');
  if (!blocked) {
    const tag = `user6-pending-block-${inbound}`;
    if (next.outbounds.some((outbound) => outbound.tag === tag))
      throw new Error('pages.xray.user6.alreadyExists');
    blocked = { tag, protocol: 'blackhole' };
    next.outbounds.push(blocked);
  }
  rules.splice(
    at,
    0,
    {
      type: 'field',
      ruleTag: `user6-pending-${inbound}`,
      comment: 'IPv6 pending preparation: prevent shared IPv6 before the user binding is ready',
      inboundTag: [inbound],
      ip: ['::/0'],
      outboundTag: blocked.tag,
    },
    {
      type: 'field',
      ruleTag: `user6-auto-${inbound}`,
      comment: 'Automatically prepare a permanent IPv6 for every native client of this inbound',
      inboundTag: [inbound],
      outboundTag: base.tag,
    },
  );
  next.routing = { ...routing, domainStrategy: 'IPOnDemand', rules };
  return next as XraySettingsValue;
}

function nativeIPv6DirectBranch(
  next: NativeTemplate,
  inbound: string,
  hasInjectedOutbounds: boolean,
) {
  const routing = next.routing ?? { rules: [] };
  const rules = routing.rules ?? [];
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
  const guard = rules.findIndex((rule) => rule.ruleTag === `user6-pending-${inbound}`);
  if (guard >= 0 && guard < at) at = guard;
  return { routing, rules, at, base };
}

// A convenience editor for ordinary native objects, not a second policy store.
// Only the final direct branch is changed; preceding restrictions and tunnels
// keep their first-match order. Complex policies remain editable in Routing.
export function planNativeUserIPv6(
  template: XraySettingsValue,
  inbound: string,
  clients: NativeIPv6Client[],
  hasInjectedOutbounds = false,
  followNewUsers = false,
) {
  const next = JSON.parse(JSON.stringify(template)) as NativeTemplate;
  if (!inbound || !clients.length || clients.some((c) => !c.inbounds.includes(inbound)))
    throw new Error('pages.xray.user6.selectClients');
  const { routing, rules, at, base } = nativeIPv6DirectBranch(next, inbound, hasInjectedOutbounds);
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
    return followNewUsers
      ? enableNativeUserIPv6Auto(next as XraySettingsValue, inbound, hasInjectedOutbounds)
      : (next as XraySettingsValue);
  };
}
