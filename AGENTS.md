# 3x-ui fork development

- Prefer the upstream 3x-ui models, APIs, forms and runtime paths. Inspect the pinned upstream implementation before adding a replacement.
- Keep Client, Inbound, Host and Node as the canonical proxy configuration. Portal accounts and financial snapshots must not become a second editable proxy configuration.
- Manage forwarding through native Xray outbounds and routing. Extend their service scope when needed; do not build another route-policy store or an annotation selector that appears to apply forwarding.
- Keep custom code limited to demonstrated gaps: commerce, subscription authorization, stable address allocation and observed runtime evidence. Reuse native parsing, validation and rendering where semantics match.
- Distinguish client subscription composition, server forwarding, allocated addresses and observed sockets. Similar labels do not make these interchangeable.
- Before replacing a production path, document its native replacement, persisted-data handling and an end-to-end acceptance case. Preserve current routing when an equivalent replacement has not been verified.
- Co-located nodes 3, 4 and 5 must keep distinct core API ports (62783/62784/62785), metrics listeners, state and observer paths. Validate actual socket ownership and egress; a saved configuration or running badge is insufficient.
- Keep UDP 22445 on US03 Direct as requested on 2026-11-17. Its legacy client label/tag is compatibility metadata, not authorization to restore WireGuard forwarding.
- An archived acceptance profile does not identify the user's current local account. Preserve live connectivity and inspect current evidence before attributing a limit or changing quota.
