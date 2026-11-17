import { createContext, useContext, useMemo } from 'react';
import { HttpUtil } from '@/utils';

export const XrayScope = createContext(0);

export function xrayPageUrl(path: string, nodeId: number) {
  if (nodeId === 0) return path;
  const [base, hash] = path.split('#');
  return `${base}?nodeId=${nodeId}${hash ? `#${hash}` : ''}`;
}

export function xrayScopeUrl(url: string, nodeId: number) {
  if (nodeId === 0) return url;
  if (!Number.isSafeInteger(nodeId) || nodeId < 0 || !url.startsWith('/panel/api/xray/')) {
    throw new Error('Unsupported request for the selected Xray service');
  }
  return `${url}${url.includes('?') ? '&' : '?'}nodeId=${nodeId}`;
}

export function xrayHttp(nodeId: number) {
  return {
    get<T = unknown>(...args: Parameters<typeof HttpUtil.get<T>>) {
      const [url, ...rest] = args;
      return HttpUtil.get<T>(xrayScopeUrl(url, nodeId), ...rest);
    },
    post<T = unknown>(...args: Parameters<typeof HttpUtil.post<T>>) {
      const [url, ...rest] = args;
      return HttpUtil.post<T>(xrayScopeUrl(url, nodeId), ...rest);
    },
    delete<T = unknown>(...args: Parameters<typeof HttpUtil.delete<T>>) {
      const [url, ...rest] = args;
      return HttpUtil.delete<T>(xrayScopeUrl(url, nodeId), ...rest);
    },
  };
}

export function useXrayHttp() {
  const nodeId = useContext(XrayScope);
  return useMemo(() => xrayHttp(nodeId), [nodeId]);
}
