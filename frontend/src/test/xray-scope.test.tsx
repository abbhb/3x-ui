import type { ReactNode } from 'react';
import { act, renderHook, waitFor } from '@testing-library/react';
import { QueryClientProvider } from '@tanstack/react-query';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { useXraySetting } from '@/hooks/useXraySetting';
import { XrayScope, xrayPageUrl, xrayScopeUrl } from '@/hooks/xrayScope';
import { makeTestQueryClient } from './test-utils';
import { HttpUtil, Msg } from '@/utils';

afterEach(() => vi.restoreAllMocks());

describe('native Xray service scope', () => {
  it('keeps fetched templates and all mutation/test requests on the selected service', async () => {
    let selected = 1;
    const urls: string[] = [];
    vi.spyOn(HttpUtil, 'post').mockImplementation(async (url) => {
      urls.push(url);
      if (url.startsWith('/panel/api/xray/?'))
        return new Msg(
          true,
          '',
          JSON.stringify({
            xraySetting: {
              outbounds: [
                {
                  tag: `node-${new URL(url, 'https://example.test').searchParams.get('nodeId')}`,
                  protocol: 'freedom',
                  settings: {},
                },
              ],
            },
            inboundTags: [],
            clientReverseTags: [],
          }),
        );
      if (url.includes('/testOutbounds')) return new Msg(true, '', [{ success: true }]);
      return new Msg(true, '');
    });
    vi.spyOn(HttpUtil, 'get').mockResolvedValue(new Msg(true, '', []));
    const queryClient = makeTestQueryClient();
    const wrapper = ({ children }: { children: ReactNode }) => (
      <QueryClientProvider client={queryClient}>
        <XrayScope value={selected}>{children}</XrayScope>
      </QueryClientProvider>
    );
    const { result, rerender } = renderHook(() => useXraySetting(), { wrapper });
    await waitFor(() => expect(result.current.templateSettings?.outbounds?.[0].tag).toBe('node-1'));
    selected = 2;
    rerender();
    await waitFor(() => expect(result.current.templateSettings?.outbounds?.[0].tag).toBe('node-2'));
    act(() =>
      result.current.setXraySetting(
        '{"outbounds":[{"tag":"node-2-edited","protocol":"freedom","settings":{}}]}',
      ),
    );
    await act(async () => result.current.saveAll());
    await act(async () =>
      result.current.testOutbound(0, { tag: 'node-2-edited', protocol: 'freedom' }),
    );
    await act(async () => result.current.resetOutboundsTraffic('node-2-edited'));
    expect(urls).toContain('/panel/api/xray/update?nodeId=2');
    expect(urls).toContain('/panel/api/xray/testOutbounds?nodeId=2');
    expect(urls).toContain('/panel/api/xray/resetOutboundsTraffic?nodeId=2');
    expect(urls.filter((url) => !url.includes('/xray/?'))).not.toContain(
      '/panel/api/xray/update?nodeId=1',
    );
  });

  it('retains scope across native pages and rejects unrelated APIs', () => {
    expect(xrayPageUrl('/xray#advanced', 3)).toBe('/xray?nodeId=3#advanced');
    expect(xrayPageUrl('/routing', 0)).toBe('/routing');
    expect(() => xrayScopeUrl('/panel/api/setting/all', 3)).toThrow('Unsupported request');
  });
});
