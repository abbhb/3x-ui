import { z } from 'zod';

const list = <T extends z.ZodType>(item: T) =>
  z
    .array(item)
    .nullish()
    .transform((v) => v ?? []);
const ServiceInstance = z.object({
  id: z.number(),
  name: z.string(),
  status: z.string(),
  xrayState: z.string().optional(),
  address: z.string().optional(),
});
export const ServiceHostSchema = z.object({
  id: z.number(),
  name: z.string(),
  note: z.string(),
  services: list(ServiceInstance),
});
export const ServiceHostsSchema = z.array(ServiceHostSchema);
const Endpoint = z.object({
  id: z.number(),
  name: z.string(),
  address: z.string(),
  port: z.number(),
  family: z.string(),
  disabled: z.boolean(),
});
export const HostLineSchema = z.object({
  id: z.number(),
  nodeId: z.number(),
  tag: z.string(),
  name: z.string(),
  protocol: z.string(),
  transport: z.string(),
  listen: z.string(),
  port: z.number(),
  enable: z.boolean(),
  egressMode: z.string(),
  note: z.string(),
  endpoints: list(Endpoint),
});
export const ServiceHostDetailSchema = ServiceHostSchema.extend({ lines: list(HostLineSchema) });
export const EgressPolicySchema = z.object({
  enabled: z.boolean(),
  namespace: z.string(),
  interface: z.string(),
  prefix: z.string(),
  mark_prefix: z.number(),
  inbounds: list(z.string()),
});
const Observation = z.object({
  id: z.string(),
  timestamp: z.string(),
  user: z.string(),
  source: z.string(),
  target: z.string(),
  network: z.string(),
  inbound: z.string(),
  outbound: z.string(),
  local: z.string(),
  remote: z.string(),
  clientFamily: z.string(),
  egressFamily: z.string(),
});
export const HostRuntimeSchema = z.object({
  nodeId: z.number(),
  name: z.string(),
  installed: z.boolean(),
  error: z.string().optional(),
  policy: EgressPolicySchema.optional(),
  status: z.object({
    enabled: z.boolean(),
    verified: z.boolean(),
    updatedAt: z.number(),
    reason: z.string(),
    users: list(z.object({ identity: z.string(), address: z.string() })),
    blockedUsers: list(z.string()),
  }),
  observer: z.object({ ready: z.boolean(), dropped: z.number(), records: list(Observation) }),
  inbounds: list(z.object({ id: z.number(), tag: z.string(), protocol: z.string() })),
});
export const HostRuntimesSchema = z.array(HostRuntimeSchema);
export type ServiceHost = z.infer<typeof ServiceHostSchema>;
export type HostLine = z.infer<typeof HostLineSchema>;
export type HostRuntime = z.infer<typeof HostRuntimeSchema>;
export type EgressPolicy = z.infer<typeof EgressPolicySchema>;
export type Connection = z.infer<typeof Observation> & {
  nodeId: number;
  service: string;
  assignedIPv6?: string;
  allocationVerified: boolean;
};
