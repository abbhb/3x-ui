import { describe, expect, it } from 'vitest';
import {
  disableNativeUserIPv6Auto,
  enableNativeUserIPv6Auto,
  nativeUserIPv6AutoEnabled,
  planNativeUserIPv6,
} from '@/pages/xray/outbounds/native-user-ipv6';
import { propagateOutboundTagRename } from '@/pages/xray/basics/helpers';
import type { XraySettingsValue } from '@/hooks/useXraySetting';

const clients = [
  { id: 7, email: '7.sample', inbounds: ['entry'] },
  { id: 9, email: '9.sample', inbounds: ['entry'] },
];
const leases = [
  { clientId: 7, address: '2001:db8:1::7', ready: true },
  { clientId: 9, address: '2001:db8:1::9', ready: true },
];
function fixture() {
  return {
    inbounds: [
      {
        tag: 'entry',
        listen: '2001:db8:1::a',
        port: 443,
        protocol: 'vless',
        settings: { clients: [{ id: 'credential', email: '7.sample' }] },
      },
    ],
    outbounds: [
      {
        tag: 'direct',
        protocol: 'freedom',
        settings: {
          domainStrategy: 'ForceIPv4',
          finalRules: [{ action: 'block', ip: ['geoip:private'] }],
        },
      },
      { tag: 'blocked', protocol: 'blackhole' },
    ],
    routing: {
      domainStrategy: 'AsIs',
      rules: [
        { type: 'field', domain: ['blocked.test'], outboundTag: 'blocked' },
        { type: 'field', inboundTag: ['entry'], outboundTag: 'direct' },
      ],
    },
  } as unknown as XraySettingsValue;
}

describe('native per-user IPv6 editor', () => {
  it('expresses automatic grants with visible native rules and a pending IPv6 guard', () => {
    const before = fixture();
    const enabled = planNativeUserIPv6(before, 'entry', clients, false, true)(leases);
    expect(nativeUserIPv6AutoEnabled(enabled, 'entry')).toBe(true);
    expect(enabled.routing?.rules?.map((rule) => rule.ruleTag)).toEqual([
      undefined,
      'user6-7-entry',
      'user6-9-entry',
      'user6-pending-entry',
      'user6-auto-entry',
      undefined,
    ]);
    expect(enabled.routing?.rules?.[3]).toMatchObject({
      ip: ['::/0'],
      inboundTag: ['entry'],
      outboundTag: 'blocked',
    });
    const later = { id: 11, email: '11.new', inbounds: ['entry'] };
    const added = planNativeUserIPv6(enabled, 'entry', [later])([
      { clientId: 11, address: '2001:db8:1::11', ready: true },
    ]);
    expect(added.routing?.rules?.[3].ruleTag).toBe('user6-11-entry');
    expect(added.routing?.rules?.[4].ruleTag).toBe('user6-pending-entry');
    const disabled = disableNativeUserIPv6Auto(enabled, 'entry');
    expect(nativeUserIPv6AutoEnabled(disabled, 'entry')).toBe(false);
    expect(disabled.outbounds).toEqual(enabled.outbounds);
    expect(disabled.routing?.rules?.at(-1)).toEqual(before.routing?.rules?.at(-1));
    expect(enableNativeUserIPv6Auto(disabled, 'entry').routing?.rules).toEqual(
      enabled.routing?.rules,
    );
  });
  it('requires an explicit direct branch when runtime subscriptions can change the default', () => {
    const value = fixture();
    value.routing!.rules = [];
    expect(() => planNativeUserIPv6(value, 'entry', clients, true)).toThrow('explicitDirect');
    expect(() => planNativeUserIPv6(fixture(), 'entry', clients, true)(leases)).not.toThrow();
    value.routing!.rules = [{ type: 'field', balancerTag: 'existing-balancer' }];
    expect(() => planNativeUserIPv6(value, 'entry', clients)).toThrow('directOnly');
    value.outbounds!.push({ tag: 'tunnel', protocol: 'freedom', sendThrough: '10.66.66.3' });
    value.routing!.rules = [{ type: 'field', outboundTag: 'tunnel', user: [], ip: [] }];
    expect(() => planNativeUserIPv6(value, 'entry', clients)).toThrow('directOnly');
  });
  it('keeps ingress, credentials, earlier restrictions and IPv4 routing intact', () => {
    const before = fixture();
    const original = JSON.stringify(before);
    const result = planNativeUserIPv6(before, 'entry', clients)(leases);
    expect(JSON.stringify(before)).toBe(original);
    expect(result.inbounds).toEqual(before.inbounds);
    expect(result.outbounds?.slice(0, 2)).toEqual(before.outbounds);
    expect(result.routing?.rules?.[0]).toEqual(before.routing?.rules?.[0]);
    expect(result.routing?.rules?.at(-1)).toEqual(before.routing?.rules?.[1]);
    expect(result.routing?.domainStrategy).toBe('IPOnDemand');
    for (const [i, user] of clients.entries()) {
      const outbound = result.outbounds?.[2 + i];
      expect(outbound?.sendThrough).toBe(leases[i].address);
      expect(outbound?.settings).toEqual({
        finalRules: [{ action: 'block', ip: ['geoip:private'] }],
      });
      expect(outbound?.streamSettings).toMatchObject({ sockopt: { domainStrategy: 'ForceIPv6' } });
      expect(result.routing?.rules?.[1 + i]).toMatchObject({
        user: [user.email],
        inboundTag: ['entry'],
        ip: ['::/0'],
        outboundTag: outbound?.tag,
      });
    }
  });

  it('refuses to bypass an existing tunnel, script, binding or generated outbound', () => {
    for (const sendThrough of ['10.66.66.3', '2001:db8:1::a']) {
      const value = fixture();
      value.outbounds![0].sendThrough = sendThrough;
      expect(() => planNativeUserIPv6(value, 'entry', clients)).toThrow('directOnly');
    }
    const scripted = fixture();
    scripted.routing!.script = 'existing-routing.lua';
    expect(() => planNativeUserIPv6(scripted, 'entry', clients)).toThrow('manualRouting');
    const generated = planNativeUserIPv6(fixture(), 'entry', clients)(leases);
    expect(() => planNativeUserIPv6(generated, 'entry', clients)).toThrow('alreadyExists');
    for (const outbound of generated.outbounds!.slice(2)) {
      const previous = outbound.tag!;
      outbound.tag = `renamed-${previous}`;
      propagateOutboundTagRename(generated, previous, outbound.tag);
    }
    expect(() => planNativeUserIPv6(generated, 'entry', clients)).toThrow('alreadyExists');
    expect(() => planNativeUserIPv6(fixture(), 'other-entry', clients)).toThrow('selectClients');
    expect(() =>
      planNativeUserIPv6(fixture(), 'entry', clients)(leases.map((l) => ({ ...l, ready: false }))),
    ).toThrow('notReady');
  });
});
