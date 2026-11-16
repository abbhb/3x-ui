import { z } from 'zod';
import {
  SubscriptionSourceSchema as GeneratedSource,
  SubscriptionNodeSchema,
  SubscriptionHostViewSchema,
  SubscriptionPolicySchema as GeneratedPolicy,
} from '@/generated/zod';
export const SourceSchema = GeneratedSource;
export const NodeSchema = SubscriptionNodeSchema;
export const RouteSchema = SubscriptionHostViewSchema;
export const PolicySchema = GeneratedPolicy.extend({
  group_order: z
    .array(z.string())
    .nullish()
    .transform((v) => v ?? []),
});
export const SelectionSchema = z.object({
  source_id: z.number().int().positive(),
  mode: z.enum(['all', 'selected']),
  node_ids: z
    .array(z.number().int())
    .nullish()
    .transform((v) => v ?? []),
});
export const PolicyNamesSchema = z.object({
  'proxy-groups': z.array(z.object({ name: z.string() })),
});
export type Source = z.infer<typeof SourceSchema>;
export type Node = z.infer<typeof NodeSchema>;
export type Route = z.infer<typeof RouteSchema>;
export type Policy = z.infer<typeof PolicySchema>;
export type Selection = z.infer<typeof SelectionSchema>;
