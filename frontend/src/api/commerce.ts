import { z } from 'zod';
import { httpRequest } from '@/api/http-init';

export class PortalRequestError extends Error {
  constructor(public status: number) {
    super(`Request failed (${status})`);
  }
}
export async function portalRequest<T>(
  path: string,
  schema: z.ZodType<T>,
  token = '',
  body?: unknown,
  method?: string,
  key?: string,
): Promise<T> {
  const res = await fetch(`${window.X_UI_BASE_PATH || '/'}api/portal/${path}`, {
    method: method || (body === undefined ? 'GET' : 'POST'),
    credentials: 'omit',
    cache: 'no-store',
    referrerPolicy: 'no-referrer',
    headers: {
      'Content-Type': 'application/json',
      ...(token ? { Authorization: `Bearer ${token}` } : {}),
      ...(key ? { 'Idempotency-Key': key } : {}),
    },
    body: body === undefined ? undefined : JSON.stringify(body),
  });
  if (!res.ok) throw new PortalRequestError(res.status);
  return schema.parse(res.status === 204 ? null : await res.json());
}
export async function commerceRequest<T>(
  path: string,
  schema: z.ZodType<T>,
  body?: unknown,
  method?: string,
  key?: string,
): Promise<T> {
  const response = await httpRequest(
    method || (body === undefined ? 'GET' : 'POST'),
    `/panel/api/commerce/${path}`,
    body,
    { headers: { 'Content-Type': 'application/json', ...(key ? { 'Idempotency-Key': key } : {}) } },
  );
  const envelope = z
    .object({ success: z.boolean(), obj: z.unknown().optional() })
    .parse(response.data);
  if (!envelope.success) throw new Error('Operation unavailable');
  return schema.parse(envelope.obj);
}

// Keep an unresolved financial request across reloads. The stored intent has no
// password, session or MGMA bearer; repeating it always uses the same key.
export type PurchaseIntent = { key: string; plan_id: number };
export function readPurchaseIntent(accountId: number): PurchaseIntent | null {
  try {
    const value = JSON.parse(sessionStorage.getItem(`xui.purchase.${accountId}`) || 'null');
    return z
      .object({ key: z.string().uuid(), plan_id: z.number().int().positive() })
      .nullable()
      .parse(value);
  } catch {
    return null;
  }
}
export function savePurchaseIntent(accountId: number, intent: PurchaseIntent | null) {
  if (intent) sessionStorage.setItem(`xui.purchase.${accountId}`, JSON.stringify(intent));
  else sessionStorage.removeItem(`xui.purchase.${accountId}`);
  window.dispatchEvent(new Event('xui-purchase-intent'));
}
export const emptyResponse = z.unknown();
