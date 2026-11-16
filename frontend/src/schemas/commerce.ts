import { z } from 'zod';
const int = z.number().int();
const ids = z
  .array(int)
  .nullish()
  .transform((v) => v ?? []);
export const PlanSchema = z.object({
  id: int,
  name: z.string(),
  description: z.string(),
  price_minor: int.nonnegative().safe(),
  currency: z.literal('CNY'),
  duration_days: int.positive(),
  data_limit: int.nonnegative(),
  inbound_ids: ids,
  host_ids: ids,
  is_visible: z.boolean(),
  compatibility_pending: z.boolean(),
  created_at: int,
  updated_at: int,
});
export const SubscriptionSchema = z.object({
  plan_id: int,
  plan_name: z.string(),
  price_paid_minor: int.nonnegative(),
  currency: z.string(),
  duration_days: int,
  data_limit: int,
  inbound_ids: ids,
  host_ids: ids,
  starts_at: int,
  expires_at: int,
  purchased_at: int,
  disabled_at: int,
});
export const AccountSchema = z.object({
  id: int,
  username: z.string(),
  wallet_balance_minor: int.nonnegative().safe(),
  is_active: z.boolean(),
  client_id: int.nullable(),
  user_id: int.nullable(),
  created_at: int,
  subscription: SubscriptionSchema.nullable(),
  usage: z.object({
    status: z.string(),
    used_traffic: int,
    lifetime_used_traffic: int.default(0),
    data_limit: int,
    expire: int,
  }),
  runtime_pending: z.boolean(),
});
export const WalletSchema = z.object({
  id: int,
  kind: z.string(),
  amount_minor: int.safe(),
  balance_after_minor: int.safe(),
  note: z.string(),
  created_at: int,
  actor_admin: z.string(),
});
export const PurchaseSchema = z.object({
  id: int,
  kind: z.string(),
  plan_name: z.string(),
  amount_minor: int.safe(),
  balance_after_minor: int.safe(),
  effective_expires_at: int,
  created_at: int,
  actor_admin: z.string(),
});
export const InvitationSchema = z.object({
  id: int,
  prefix: z.string(),
  note: z.string(),
  starts_at: int,
  expires_at: int,
  max_uses: int,
  use_count: int,
  disabled_at: int,
});
export const BlockSchema = z.object({
  id: int,
  network: z.string(),
  reason: z.string(),
  source: z.string(),
  actor_admin: z.string(),
  expires_at: int,
  revoked_at: int,
  created_at: int,
});
export const RuntimeSchema = z.object({
  id: int,
  account_id: int,
  client_id: int,
  finished_at: int,
  last_error: z.string(),
  created_at: int,
});
export const SettingsSchema = z.object({
  enabled: z.boolean(),
  public_url: z.string(),
  mode: z.enum(['legacy', 'dual', 'ephemeral']),
  ttl_seconds: int,
  single_use: z.boolean(),
  source_mode: z.enum(['any', 'china', 'custom', 'china_or_custom']),
  custom_cidrs: z
    .array(z.string())
    .nullish()
    .transform((v) => v ?? []),
  auto_block_enabled: z.boolean(),
  login_failure_limit: int,
  registration_failure_limit: int,
  login_window_seconds: int,
  registration_window_seconds: int,
  auto_block_seconds: int,
});
export const SettingsViewSchema = z.object({
  settings: SettingsSchema,
  pepper_configured: z.boolean(),
  legacy_key_configured: z.boolean(),
});
export const IssuedSchema = z.object({
  url: z.string().url(),
  issued_at: z.string(),
  expires_at: z.string().nullable(),
  ttl_seconds: int,
});
export const NextPlanSchema = z.object({
  client_id: int,
  data_limit: int,
  expire: int,
  add_remaining_traffic: z.boolean(),
  fire_on_either: z.boolean(),
  paused: z.boolean(),
  last_error: z.string(),
});
export type Plan = z.infer<typeof PlanSchema>;
export type Account = z.infer<typeof AccountSchema>;
export type Issued = z.infer<typeof IssuedSchema>;
export type Settings = z.infer<typeof SettingsSchema>;
export type Wallet = z.infer<typeof WalletSchema>;
