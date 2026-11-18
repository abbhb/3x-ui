# 第四阶段：外部订阅、原生连接、规则 DNS 与同域名切换

当前状态：剩余旧服务已于 2026-11-18 完成交接并解除新业务冻结。本文中的旧核保留、阶段性限制和冻结步骤是历史交接说明；现行状态见[最终交接记录](FINAL-CUTOVER.zh-CN.md)。

外部 XHTTP 订阅的整数输出与旧全局指纹兼容见 [Mihomo 兼容记录](MIHOMO-COMPATIBILITY.zh-CN.md)。

基于第三阶段 PR #3 合并提交 `b4ae56a4`，来源仍为
`abbhb/Marzban@0ebdd8052743066fbe5d845153a6adc24334ea6a`。
本文件记录迁移合同和阶段开发验证；实际部署范围与验收边界见
[2026-11-17 生产交接记录](PRODUCTION-2026-11-17.zh-CN.md)。

## 老代理保留约束

用户明确要求：**老 Xray 先不要下，至少保留一条能让本机继续联网的旧线路。**
2026-11-17 提供的客户端截图选中“美国 03 · HY2 IPv6 优先”；名称只能帮助定位，
实施前必须核对实际主机、服务进程、入站、UDP 监听、入口地址、凭据、出口控制器与分配库。
仅在订阅里保留同名节点不算保留线路。

- 新实例使用独立目录、数据库、socket、内部监听端口和服务名，优先放在另一台主机。
- 本机代理所用旧核心、监听、证书、分配库和 NAT 规则保持运行。禁止顺手执行旧栈
  `compose down`、卸载、覆盖二进制、全局 `pkill xray` 或重启其容器 / 父面板进程。
- 新旧系统不能控制同一个核心、同一个活动分配库、相同 mark / nft namespace。
  面板域名切换与代理监听接管是两件事；先切门户不会要求拆掉旧代理。
- 在本机实际切到新线路、完成重连和外网探测前，不回收最后一条旧线路。
  之后是否下线旧核心仍需单独明确指令。

## 当前连接配置范围（2026-11-17）

额外的“线路、级联与 XHTTP”页与对应管理 API 已移除。连接配置统一使用 3X-UI 原生
入站、Host、出站和路由；旧扩展不再注入级联入口、替换 XHTTP 连接或限制订阅格式。
数据库旧扩展记录仅用于历史套餐签名与迁移回读，保留以兼容既有授权，不参与订阅生成。
已导入的独立连接不会自动转换为原生配置，需要的连接应在原生功能中配置。
账号列表与账号详情均提供“查看客户端”，使用关联的 `client_id` 进入
`panel/clients?clientId=<id>` 精确筛选，清除之前的搜索和筛选，并显示可清除的客户端条件。
未关联客户端的账号显示“未关联客户端”；找不到指定 ID 时返回空列表。

## 页面与接口

管理页为 `<webBasePath>panel/commerce`，URL 的 `tab` 支持直接进入：

| 页面       | URL 参数                 | 能力                                                             |
| ---------- | ------------------------ | ---------------------------------------------------------------- |
| 外部订阅   | `tab=external&source=12` | 公共 / 用户私有来源，元信息、只写凭据、刷新、导入 YAML、节点清单 |
| 规则与 DNS | `tab=policy`             | 面板模板、规则顺序、策略组顺序、安全 DNS                         |
| 套餐       | `tab=plans`              | 本地线路 all / selected、外部来源 all / selected、策略组成员     |

列表进入页面内的详情；刷新和返回可保留定位，不用模态窗承载这些设置。
用户在 `<webBasePath>portal/` 的“我的外部订阅”管理自己的来源。
公共 / 私有列表每页 100 项，节点列表同样分页。旧的 `tab=routes` 书签会返回账号页。

管理 API 在 `/panel/api/commerce/external-subscriptions` 与 `/subscriptions/*` 下，
沿用完整管理员会话 / token 和 CSRF；monitor / 节点角色没有管理权限。
用户 API 在 `/api/portal/external-subscriptions` 下，使用门户 JWT；owner 来自已验证账号，
不接收请求体指定其他账号。跨账号读取 / 修改返回 404。
凭据 URL 和节点明文不出现在元信息 API；只返回是否已配置凭据。

## 外部订阅与授权

- YAML 输入只接收顶层 `proxies`，不采用上游的规则、DNS、策略组或 provider。当前来源也支持分享链接列表；客户端管理和格式范围见[客户端外部订阅](NATIVE-FIRST.zh-CN.md#客户端外部订阅)。
  YAML 最大 2 MiB、每来源最多 1000 节点，限制深度与标量，拒绝重复键、别名、标签及多文档。
  节点中的 `dialer-proxy`、`detour` 等外部引用被拒绝，防止引用面板外的隐藏节点。
- URL 仅允许公共 HTTPS，无用户名密码；每次 DNS 解析和重定向重新检查地址，
  拒绝回环、内网、保留地址。使用固定解析结果拨号、验证 TLS、不使用代理环境变量，
  限制超时、重定向和解压后大小；不把 URL、token、上游响应写进错误信息。
- URL 与完整节点用来源兼容的 Fernet / HKDF 方式加密。
  目标环境变量 `XUI_EXTERNAL_SUBSCRIPTION_ENCRYPTION_KEY` 必须等于原
  `EXTERNAL_SUBSCRIPTION_ENCRYPTION_KEY`，至少 32 UTF-8 字节；单独安全备份，禁止提交 Git。
  密钥错误时拒绝导入 / 投影，不能静默清空节点或变为明文保存。
- 刷新先在事务外下载、解析，再通过 lease / URL revision 检查和全局预算锁原子发布。
  下载、解析、预算或提交失败保留最后成功快照；订阅请求只读快照，不现场抓机场。
- 公共来源停用只停止自动刷新，最后成功快照仍按套餐授权提供。
  私有来源停用则停止混入该用户订阅，快照仍保留；两种开关含义在页面注明。
- 私有来源每账号最多 10 个，合计最多 1000 节点 / 2 MiB。
  公共来源按每个套餐所选节点限制 5000 节点 / 8 MiB。
  多进程刷新不同来源也必须串行校验同一个套餐预算。
- `all` 包含来源今后新增的活动节点；`selected` 只包含显式节点 ID。
  节点身份依据名称、类型、服务器、端口和重复序号生成，凭据轮换不改 ID。
  被选节点消失后保留墓碑，再出现可恢复；不会替换成同名新服务器。
- 外部来源只增加订阅节点，不分配本地入站权限。支持仅含外部节点的套餐；
  购买仍使用原钱包与幂等交易链路。外部机场流量无法计入本机 Xray 的用量账目。
  用户自己的启用来源仅混入自己的有效代理订阅，不能成为公共套餐。

## 原生连接与 XHTTP

入站的传输参数与 Host 的原生地址、端口、TLS、SNI 和 HTTP Host 设置决定订阅连接。
原生 XHTTP 的 `extra`、独立下载连接、IPv6 authority 与 HTTP 复用仍转换为对应的
Mihomo 参数；原生 raw / JSON / Clash 输出按各自支持范围生成。
扩展中的 `cascade_mode`、入口密文、`mihomo_only` 和 `client_stream` 不再影响这些输出。
`/panel/api/commerce/subscriptions/hosts`、`/hosts/:id` 和 `/cascade-entries` 已移除；
原生 Host API、套餐 Host 授权目录及规则 DNS API 保持可用。

## 规则、组成员和 DNS

规则 / 策略组使用面板拥有的模板，来源刷新不能覆盖。模板用
`__MGMA_LOCAL_NODES__` 表示当前授权本地节点，用 `MGMA · External` 表示当前授权的外部来源。
保留规则顺序、最终 `MATCH,target`、合法目标引用、组顺序与套餐组成员；校验循环、未知目标和 provider 引用。
套餐可以把组限制到已授权入站和外部来源；显式空组使用 `REJECT`，不回退到全部节点。

安全 DNS 开启时：

- 普通解析通过主代理组的 Cloudflare / Google DoH，无系统 DNS 或明文 fallback。
- 只有可证明全部是代理的选择图才作为 DNS 出口；有 DIRECT、循环或未知引用时使用 REJECT。
- 节点 bootstrap 和面板域名单独使用阿里 IP DoH 直连，解决代理未建立前的依赖。
- 保留 LAN、时间同步、STUN、系统连通性检查、QQ 等 fake-IP 例外与 hosts。
  fake-IP 例外是返回真实地址，不代表改用系统 DNS。
- 总体 `ipv6=true` 保留 IPv6 节点连接；DNS `ipv6=false` 控制业务域名 AAAA 查询，两者不冲突。
- 输出 TUN 所需 DNS 劫持 / 路由默认值，但不设置 `tun.enable`。
  客户端是否开启 VPN / TUN、是否覆盖 DNS，需要实际设备验收。

保存规则和 Host 设置使用 revision 并发检查。套餐具体线路保存连接签名；
地址 / 端口 / 传输变化后原 `selected` 授权失效，后台分批清理并重新下发，不偷偷改授另一条线路。
`all` 授权保留为入站级自动包含今后方案，不能误转为当前 Host ID 快照。
价格、购买额度、期限、已付账本不随线路清理改写。

## 离线导出、导入与回读

使用独立静止 SQLite 副本、同一切换点的核心配置与完整分配库，输入不可来自活动 WAL。
先创建隔离目标的入站，人工核对协议、flow、传输、TLS、路径、后端端口与节点服务归属。
源 tag → 目标入站 ID 的映射沿用前三阶段；工具不猜生产主机与监听关系。

```sh
python3 tools/marzban_subscription_export.py \
  --database .migration-private/frozen-marzban.sqlite3 \
  --core .migration-private/core.json \
  --inbound-map .migration-private/inbound-map.json \
  --output .migration-private/stage4-catalog

python3 tools/marzban_portal_export.py \
  --database .migration-private/frozen-marzban.sqlite3 \
  --core .migration-private/core.json \
  --inbound-map .migration-private/inbound-map.json \
  --target-inbounds .migration-private/target-inbounds.json \
  --route-map .migration-private/stage4-catalog/route-map.json \
  --target-hosts .migration-private/stage4-catalog/target-hosts.json \
  --traffic-policy preserve-total-as-download \
  --ipv6-allocation us03=.migration-private/us03-allocations.sqlite3 \
  --stage4 --clash-template .migration-private/easyus.yml \
  --output .migration-private/stage4-bundle

# 仅新隔离实例离线，不能为运行此命令去停止旧代理。
# XUI_DB_FOLDER / PostgreSQL DSN 必须指向空的目标客户库。
./x-ui import-portal --bundle .migration-private/stage4-bundle --offline
```

环境预先受控安装旧 MGMA pepper、旧签名 key 和外部加密 key；命令参数不接收密钥明文。
JSON 策略模板只需要 Python 3.11+ 标准库；源 Jinja2 / YAML 模板需要 `Jinja2`、`PyYAML`。
离线从 REALITY 私钥推导公钥、读取证书 SAN 还需要 `cryptography`；可用显式
`--tls-name-map` 的 `tag → [DNS名称]` 替代现场证书路径。禁止把私钥写到客户端方案。

目录 `0700`，文件 `0600`，不得上传 Git / PR / CI。生成的 route ID 与源算法一致，
包含停用的命名方案；要求全部映射与 Host 参数精确一致，缺少 / 合并方案直接拒绝。
具体 Host 保存原公共接入端口，因此目标后端可以改用隔离端口，不会让订阅误连接新后端端口。

`portal.json` 包含 `subscriptions_extension`：公共 / 私有来源、原节点 ID / 密文、
具体 Host 与规则模板；旧包中的级联、客户端 XHTTP 流仅归档回读，不再生效。
manifest 校验模板与所有包成员摘要。
原子事务同时写入第三阶段账本与第四阶段扩展，验证密钥、归属、引用、预算和策略，逐记录回读；
错误整体回滚，PostgreSQL 推进显式 ID 序列。目标门户默认关闭，也不会启动核心或联系节点。
工具始终返回 `ready_for_cutover=false`；需要下列实际交付证据才能接管。

### 生产副本中的历史兼容

生产演练发现的边界已纳入导入检查：

- `--archive-unmanaged-disabled` 只允许把没有门户关联、没有待生效套餐的停用代理用户保留在私有历史包中；保留其凭据、使用历史和永久编号。活动用户、门户用户仍必须完整转换。该选项不删除源用户，也不让历史身份重新获得代理权限。
- 历史购买记录中的已下线入站保存在 `legacy_inbound_tags`，只给仍有明确映射的入站写入目标 ID。历史购买不作为当前授权的恢复依据。
- 已解绑代理用户的停用或已到期套餐可以保留；尚有效的套餐不能在缺少代理用户时导入。
- 管理员可能单独延长实际代理账号有效期。订阅授权以活动代理账号的实际到期时间为准，保留套餐历史到期日，显式停用的套餐仍撤销授权。
- SQLite 的可空布尔值按源 ORM 转换后再计算线路 ID，`0` 不能当作与 `false` 不同的线路指纹。
- Jinja 模板的相对 include 读取冻结模板目录，参与模板摘要。模板对单个代理做的修改（例如证书指纹）仍需逐线路核对，不能只核对空代理列表下的策略模板。
- 旧策略组排序中的已删除名称按源行为忽略，新增模板组按模板顺序追加；不改变组的成员和规则。

2026-11-17 的隔离生产副本演练完成 5 个活动代理用户、7 个门户账号、32 条购买、5 条钱包记录及 6 个永久身份的原子导入与 450 次逐记录回读。另 1 个无门户关联的停用用户保留在私有历史包中。此结果只证明冻结副本转换与回读，不代表域名、节点或实际出网已经完成切换。

此次交接必须保留旧主面板内核，以及 `marzban-node-us03-hy2` 整个旧服务（普通 HY2 与 IPv6 优先 HY2）。旧订阅中美国 03 HY2 IPv6 优先入口 `23.246.155.106:22444/udp` 的监听、认证和原 IPv6 分配控制器，在用户明确通知已切换本地代理之前不得停止或接管。其余节点分服务切换；不能使用全局停止命令或重新连接受保护的旧节点来获取配置。

## mp.easyus.top 同域名切换步骤

可以保留域名、DNS 记录和现有 TLS 证书，通过反代切换后台。
以下是假定目标 `webBasePath=/`、订阅前缀已设为 `/sub/` 的配置示意；
上线前需根据实际旧地址补全 `/json/`、`/clash/` 等已公开前缀，禁止遗漏造成裸订阅旁路。

```nginx
# http {} 内：不记录包含 MGMA token 的查询字符串
log_format portal_no_query '$remote_addr $request_method $uri $status';
upstream xui_panel { server unix:/run/x-ui-private/panel.sock; }
upstream xui_sub   { server unix:/run/x-ui-private/sub.sock; }

# 放入 mp.easyus.top 现有 HTTPS server {}，保留证书与已有代理传输 location。
access_log /var/log/nginx/portal-access.log portal_no_query;
location ^~ /sub/ {
    proxy_pass http://xui_sub;
    proxy_set_header Host $host;
    proxy_set_header X-Real-IP $remote_addr;
    proxy_set_header X-Forwarded-Proto $scheme;
    proxy_buffering off;
    proxy_cache off;
}
location = /dashboard { return 302 /panel/; }
location = /dashboard/ { return 302 /panel/; }
location / {
    proxy_pass http://xui_panel;
    proxy_set_header Host $host;
    proxy_set_header X-Real-IP $remote_addr;
    proxy_set_header X-Forwarded-Proto $scheme;
    proxy_cache off;
}
```

Nginx 必须覆盖来源头；fork 仅在受权限保护的 Unix listener 信任 `X-Real-IP`。
socket 权限、目录、单实例锁遵循[第三阶段说明](STAGE3-PORTAL-MGMA.zh-CN.md)。
TCP 入口忽略伪造转发头。管理登录、门户 API 与订阅要走正确的不同 listener。
XHTTP / WebSocket 等现有转发路径必须保留精确 location，不能被新 `location /` 接走。
旧 `/dashboard/#/nodes/3` 的 hash 不会传给服务器；跳到 `/panel/` 后重新按主机定位，
不能假设旧节点数字 ID 等于新服务主机 ID。用户入口为 `/portal/`。

按以下顺序实施，每一步保存证据和回退点：

1. **并行演练。** 保留全部旧代理，部署独立 fork 和已升级的 fork 子节点，
   导入脱敏演练库或受控真实副本，先不向公网发布新订阅。核对客户端凭据、钱包余额、
   每条历史流水、当前权益、私有来源归属、级联、规则、Host 数量和分配库孤儿项。
2. **新线路验证。** 用两名测试用户完成固定 IPv6 接入不变、不同用户出口不同、重连不换地址、
   IPv4 目标 / IPv4 回退、普通 HY2、WG、CDN、XHTTP 独立上下行、机场、级联入口断开、
   DNS / VPN 覆盖与 MGMA。NAT 前 socket 观察不能代替公网服务看到的实际源地址。
3. **定义最终切换点。** 冻结旧账号 / 钱包 / 购买 / 套餐 / 规则 / 分配写入，并暂缓新业务写入。
   老核心继续跑会产生流量和可能的新分配；必须禁建新用户 / 新分配，记录最后核心计数与已入库水位，
   将冻结后增量纳入最终账目，不能拿前一天快照当最终数据。当前导入器只接受空目标，
   不提供双写或持续流量增量同步；未完成最终对账前不能开放新交易。
   **不要通过停止旧父面板 / 容器来“冻结”而顺带杀掉本机代理。**
4. **切管理面。** 安装同一最终备份 / 密钥到空目标，离线原子导入并逐记录回读；设置正式公共地址。
   保存旧反代配置，检查 `nginx -t` 后只 reload Nginx；先验证门户登录与旧永久 / 新短期 MGMA。
   这是同域名后台切换，不改代理公共 IP 或停旧核心。
5. **分批接管代理。** 每个服务单独转移控制权与监听，已有固定 IPv6 地址 / mark 保持；
   同一监听不能两套核心同时绑定。保留本机正在使用的旧 HY2 服务到最后，
   先把本机切到已实测的新线路并确认可达，再讨论退役。旧残留核心的流量 / 配额仍需单一账目归属，
   未解决计数衔接则继续维持旧业务权威，新门户只用于验收，不开放购买和额度变更。
6. **验收后收尾。** 只有新核心实际下发、双用户出口、财务对账和本机连接都验收后，
   才开放新交易并记录唯一写入权威。保留旧服务与备份的回退期，之后另行决定卸载。

回退时先冻结目标写入、保存目标新增账目与计数，再回切反代和单个服务控制权；
不能只恢复旧数据库从而丢掉新充值 / 购买。若目标尚未开放写入，可直接回切保存的旧反代；
保留的旧代理无需重启。线上执行需要明确的切换窗口、主机清单和当时的计数水位。

## 原第四阶段验证证据与限制（历史记录）

下述级联验收来自移除扩展前的版本。当前回归改为检查旧扩展不参与输出、原生 XHTTP
仍可生成；`subscription_runtime_lab.py` 继续覆盖原生上下行与规则 DNS，已移除级联实验。

- Python 合成 SQLite → stage4 bundle 与 Go 实际事务导入使用同一 fixture；核对旧 bcrypt、
  本地凭据、外部 selected / 私有归属、密文刷新、XHTTP 参数、规则 DNS 与错误密钥整体回滚。
- SQLite 验证 LKG、节点 ID、墓碑、授权收敛与金融历史不变；PostgreSQL 16 验证两个来源
  各自合规但合计超额的并发候选只能发布一个，另一个保留旧快照。
- 真实 Mihomo `v1.19.34` 与锁定 Xray 观察内核在 loopback 验证两跳级联 / 无直连回退、
  导入配置可加载、fake-IP 例外，以及 IPv4 上传 + IPv6 下载到同一 XHTTP 后端。
  脚本不启用 TUN、不修改 OS 代理，只清理自己启动的测试进程。
- 前端组件验证私有来源的 JWT 范围、写后清空 URL / YAML、规则 revision 冲突、原门户购买重试。
  API 模型、生成 OpenAPI、页面登记与权限链路一并验证。

上述测试和 CI 合并是开发交付证据。真实生产导出、主机安装、同域名反代 reload、
既有连接迁移、NAT 后公网 IP、实际客户端 DNS 与双用户计费验收仍是单独的上线步骤。
