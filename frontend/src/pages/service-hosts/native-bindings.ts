import type { XraySettingsValue } from '@/hooks/useXraySetting';

export interface NativeBinding {
  key: string;
  outbound: string;
  address: string;
  users: string[];
  inbounds: string[];
  destinations: string[];
  enabled: boolean;
}

// Read the same native objects as Outbounds/Routing. This is a summary, never
// a second policy store or an assertion that a particular socket used a rule.
export function nativeIPv6Bindings(template: XraySettingsValue): NativeBinding[] {
  const result: NativeBinding[] = [];
  for (const outbound of template.outbounds ?? []) {
    const address = outbound.sendThrough;
    if (typeof address !== 'string' || !address.includes(':')) continue;
    const rules = (template.routing?.rules ?? []).filter(
      (rule) => rule.outboundTag === outbound.tag,
    );
    if (!rules.length)
      result.push({
        key: String(outbound.tag),
        outbound: String(outbound.tag),
        address,
        users: [],
        inbounds: [],
        destinations: [],
        enabled: false,
      });
    for (const [index, rule] of rules.entries())
      result.push({
        key: `${outbound.tag}:${index}`,
        outbound: String(outbound.tag),
        address,
        users: Array.isArray(rule.user) ? rule.user.map(String) : [],
        inbounds: Array.isArray(rule.inboundTag) ? rule.inboundTag.map(String) : [],
        destinations: Array.isArray(rule.ip) ? rule.ip.map(String) : [],
        enabled: rule.enabled !== false,
      });
  }
  return result;
}
