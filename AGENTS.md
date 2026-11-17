# 3x-ui fork development

- Prefer the upstream 3x-ui models, APIs, forms and runtime paths. Inspect the pinned upstream implementation before adding a replacement.
- Keep Client, Inbound, Host and Node as the canonical proxy configuration. Portal accounts and financial snapshots must not become a second editable proxy configuration.
- Manage forwarding through native Xray outbounds and routing. Extend their service scope when needed; do not build another route-policy store or an annotation selector that appears to apply forwarding.
- Keep custom code limited to demonstrated gaps: commerce, subscription authorization, stable address allocation and observed runtime evidence. Reuse native parsing, validation and rendering where semantics match.
- Distinguish client subscription composition, server forwarding, allocated addresses and observed sockets. Similar labels do not make these interchangeable.
- Before replacing a production path, document its native replacement, persisted-data handling and an end-to-end acceptance case. Preserve current routing when an equivalent replacement has not been verified.
- Preserve the legacy main Xray, US03 HY2 and their address allocator until the user explicitly confirms their local client has switched and authorizes the remaining handoff.
- Follow [the native-first audit](docs/migration/NATIVE-FIRST.zh-CN.md). It separates implemented changes from remaining work.
