# abbhb/3x-ui：Marzban 分阶段迁移

本 fork 从 `MHSanaei/3x-ui main` 的
`5297fd3c8495d80fab97ae511553fa545c69e913` 开始二开；不是从旧版单入站面板重写。
迁移来源为 `abbhb/Marzban`，本阶段核对的版本为
`0ebdd8052743066fbe5d845153a6adc24334ea6a`。记录日期：2026-11-17。

第二阶段的实现与部署合同见[主机、线路和出口兼容](STAGE2-HOSTS-EGRESS.zh-CN.md)。
第三阶段见[门户、交易与 MGMA](STAGE3-PORTAL-MGMA.zh-CN.md)。
第四阶段见[外部订阅、级联、XHTTP、规则 DNS 与同域名切换](STAGE4-SUBSCRIPTIONS-CUTOVER.zh-CN.md)。
验收后按[原生功能优先审查](NATIVE-FIRST.zh-CN.md)收敛重复实现；该记录区分已清理内容和仍缺失的面板操作能力。
当前生产范围、旧 HY2 保留、IPv6 接入与出口区别、实际探测结果及回退方法见
[2026-11-17 生产交接记录](PRODUCTION-2026-11-17.zh-CN.md)。

## 阶段与验收边界

| 阶段                          | 交付与验收                                                                                                                                                               |
| ----------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| 1：离线数据样板（已合并）     | SQLite 副本转换成原生客户端导入包；合成样本经过真实 `ClientService.ImportClients` 后回读身份、凭据、授权、流量、状态和到期时间；保存 IPv6 分配库副本。尚未接管真实节点。 |
| 2：主机、线路与出口（已合并） | 建立物理主机 / 服务实例 / 入站 / 订阅方案的关系；迁移节点限定、Host 方案和套餐授权；适配 Xray 启动与用户固定 IPv6 控制器；主机详情页及连接 tab。                         |
| 3：门户与授权分发（已合并）   | 门户账号、密码兼容、邀请码、钱包与不可变交易账本、套餐购买 / 续费 / 待生效套餐；MGMA 仅最新 bearer、可配置有效期及永久授权、撤销、来源 CIDR 和代理信任边界。             |
| 4：订阅与切换（开发交付）     | 外部订阅的凭据只写、加密保存、失败保留最后可用版本、套餐节点投影；级联替换、XHTTP 独立上下行、DNS / 规则 / 策略组；真实双用户线路验收，冻结旧写入后对账切换。            |

四个阶段和后续迁移修复已合并；域名及服务 1、2、3、5 已交接。旧主核心与服务 4 保留，交易暂时冻结，用户本机最终切换仍待确认。
用户要求旧 Xray 暂时保留，尤其不能关闭本机正在使用的最后一条旧代理；详见第四阶段的保留与切换顺序。
阶段 1 的包始终标记 `ready_for_cutover=false`，不能用它证明业务、网络或生产迁移完成。
旧 Marzban 继续管理保留核心；业务授权冻结并通过流量交接回执对账，不能让两个面板同时修改同一份授权、核心或分配状态。

3x-ui 原生已经有全局客户端、多入站绑定、多节点、配额、到期、Hysteria2、XHTTP、
外部订阅合并等能力；优先扩展现有模型。面板管理员 `User` 不能替代门户账号，
客户端 `Group` 不能替代付费权益或交易账本；节点接口也不能直接连接 Marzban-Node。

## 第一阶段工具

`tools/marzban_export.py` 使用 Python 3.11+ 标准库。它只读输入，生成私有文件，
不会访问网络、写入目标库、调用管理 API、重启核心或部署任何服务。

输入：

- Marzban **独立、静止的 SQLite 备份**，不能直接复制运行中的数据库主文件。
  先用 SQLite backup API / `.backup` 创建副本；工具拒绝非空 `-wal` 旁文件，并检查完整性与读取前后摘要。
- 对应版本的核心 JSON，含入站的 `x-marzban-flow`。环境变量中
  `XRAY_EXCLUDE_INBOUND_TAGS` 的每项需通过 `--excluded-inbound-tag` 明确传入。
- 人工核对的“源入站 tag → 目标入站 ID 列表”JSON。同一 tag 可对应不同服务上的多个入站，
  但目标 ID 不能重复归属于不同 tag。
- 目标 3x-ui 入站清单，接受原生 API 的 `{ "success": true, "obj": [...] }` 或直接数组。
  每项必须包含 `id`、`protocol`、明确的布尔 `disableFlow`；Vision 入站还需 `streamSettings`。
  创建测试入站时逐项核对传输、TLS / REALITY、SNI、路径、监听、端口、密钥及节点归属。
  工具只验证 ID / 协议 / flow 兼容，不迁移或验证整套服务器配置。
- 可选的命名 IPv6 分配库备份，格式 `--ipv6-allocation us03=/私有路径/allocations.sqlite3`。

权限映射文件示例，以下名称和 ID **均为测试数据**：

```json
{
  "TEST_VISION": [101],
  "TEST_XHTTP": [102],
  "TEST_HY2": [103]
}
```

先在不对公网开放订阅和代理端口的隔离实例建立对应测试入站。`SubID` 保留旧稳定 token，
但 3x-ui 原生长期订阅不具有 MGMA 语义；在 MGMA 网关完成之前不能发布或代理它的公网订阅。
本地演练命令：

```sh
python3 tools/marzban_export.py export \
  --database .migration-private/marzban-backup.sqlite3 \
  --core .migration-private/core.json \
  --inbound-map .migration-private/inbound-map.json \
  --target-inbounds .migration-private/target-inbounds.json \
  --traffic-policy preserve-total-as-download \
  --ipv6-allocation us03=.migration-private/us03-allocations.sqlite3 \
  --user-id 7 --user-id 9 \
  --output .migration-private/stage1-bundle

python3 tools/marzban_export.py verify .migration-private/stage1-bundle
```

示例 ID 仅表示选择方式；实际选取需另行核对。省略 `--user-id` 时读取全部用户。
默认要求所选用户的所有可用源入站均有映射；仅做局部样板时可显式加
`--allow-unmapped-inbounds`，遗漏项会记录在 `identities.json`，且 manifest 标记部分投影。
这个选项不能绕过套餐、生命周期或 flow 限制，也不代表完成迁移。

输出目录必须不存在；目录权限 `0700`，文件权限 `0600`：

| 文件                         | 用途                                                                                                               |
| ---------------------------- | ------------------------------------------------------------------------------------------------------------------ |
| `clients.json`               | 原生 `ClientCreatePayload[]`，可作为客户端导入数据                                                                 |
| `import-request.json`        | 原生管理接口 `POST <面板路径>/panel/api/clients/import` 的请求体：`{"data":"JSON 数组字符串"}`；不是直接 POST 数组 |
| `identities.json`            | 旧数字 ID、核心 email、分配器 key、源状态、使用量、禁用原因、遗漏入站                                              |
| `allocations/<名称>.sqlite3` | 原样保存分配器的完整独立副本，包括 `users`、`state`、`owned` 及已删除用户的保留项                                  |
| `manifest.json`              | 格式版本、输入与输出摘要、范围和未完成项；最后写入，作为包完整写出的标记                                           |

`verify` 核对成员摘要、目录成员路径、原生请求与客户端数组的一致性；它不是签名认证，
不能防止有写入权限的人同时替换数据与 manifest。它也不证明目标实例导入成功。
导入后必须检查返回的每一项 `skipped`，逐条回读目标库；原生导入可能部分成功。
已有同名用户的处理遵循原生接口，不能把反复导入当作覆盖更新或权限对账。
测试额外验证重导入不会清掉随后产生的流量计数。

输出包含真实凭据、稳定 token 和用户地址，必须保留在受控的私有目录，禁止提交 Git、
作为 PR 附件或传给 CI。仓库只提交合成样本，`.migration-private/` 已加入忽略规则。
工具成功日志只输出客户端数量，错误也不回显 UUID、Auth、token 或原始 JSON。

## 数据转换约定与阻断项

| 来源                     | 目标 / 处理                                                                                                                                 |
| ------------------------ | ------------------------------------------------------------------------------------------------------------------------------------------- |
| `users.id` + `username`  | 客户端 `email=<旧ID>.<username>`；目标自增 ID 可以不同，不能用它替换旧分配身份                                                              |
| VLESS `settings.id`      | 原样保留 `client.id`，不重新生成 UUID                                                                                                       |
| Hysteria `settings.auth` | 原样保留 `client.auth`，与 VLESS UUID 分开处理                                                                                              |
| `subscription_token`     | 阶段 1 保留为 `subId`；阶段 3 的专用包接入旧 URL、MGMA 授权及来源规则                                                                       |
| `used_traffic`           | 来源只保存本周期上传+下载总和。非零时必须显式选择 `preserve-total-as-download`：`up=0, down=原总量`；这是配额基线，不能显示成真实历史下载量 |
| `data_limit`             | 以字节写入原生 `totalGB`；字段名称包含 GB，但此导入字段实际存储字节                                                                         |
| `expire`                 | Unix 秒转换为毫秒；空 / 0 保留无限期；拒绝负值及溢出                                                                                        |
| 状态                     | `disabled/limited/expired` 保持禁用；即使源状态 active，导出时已到期或已耗尽配额也禁用                                                      |
| 入站授权                 | 按代理协议列出有效核心入站，再减去该代理的排除项，仅绑定显式映射后的 ID                                                                     |
| flow                     | 按源入站 `x-marzban-flow` 和传输能力解析，忽略旧代理设置的 flow；混合 Vision / 无 flow 入站时，无 flow 的目标必须 `disableFlow=true`        |
| 分配器                   | 保留旧数字 key、mark、IPv6 地址，以及完整状态库。报告不在源用户表中的保留 ID；不自动回收、不重排、不重新随机分配                            |

当前支持同一用户的 VLESS + Hysteria2，或其中一种。遇到未知代理协议 / 设置、重复凭据、
待生效 `next_plans`、`on_hold`、周期配额重置、未提供具体线路映射的非空套餐 `local_node_selections` 会停止。
需要从 fallback 主入站继承 flow 安全参数的监听也先阻断，等待显式迁移其父子关系。
尤其不能把套餐选中的单个主机方案直接扩大为整个入站：需阶段 2 的 Host 规则迁移。
重置次数暂记为合成 `0`，旧重置日志和全生命周期账单不包含在本包中。

## 入口、出口和连接数据必须保留的语义

固定 IPv6 **接入**是客户端连接服务的公共地址，应保持稳定；按用户 IPv6 **出口**是
代理认证后连接业务目标使用的 /128。同一用户、同一出口实例重连复用分配；不同主机或
不同策略实例可以各自分配。IPv4 目标与 IPv4 回退仍可共享主机 IPv4。

来源文档 2026-11-16 的已知范围，仅作为迁移基线，部署前要重新核验：

- `US03_HY2_IPV6` 是已接管的按用户 IPv6 出口入站；普通 HY2、WireGuard → US01 不属于该策略。
- US02 的固定 IPv6 P1 及 XHTTP IPv6 上下行方案仍是共享出口，**尚未完成**用户要求的按用户出口。
- US02 DIRECT 的 IPv4 / IPv6 方案共用入站，XHTTP 多方案也共用入站；仅按 tag 下发策略会一起生效。
  如需只接管 IPv6 接入方案，先设计服务端可辨识的监听 / 入站，不能只改订阅名称。
- 一个物理主机可以承载多个服务实例；主机详情的连接 tab 要展示明确的服务范围。

旧 `scripts/node_egress/launch.py` 只匹配 `run -config stdin:`，
此版本 3x-ui 使用 `xray -c <配置文件>`。直接复制旧启动器不会注入出口路由。
阶段 2 需适配启动前配置生成、持久用户身份、mark / nftables、重启后回读与新用户阻断，
保留已有分配库，禁止临时落到共享 IPv6。

连接展示要分别记录认证用户、客户端地址、目标、实际拨号远端、出站和本地 socket 地址，
由地址判定 `IPv4 → IPv4 / IPv4 → IPv6 / IPv6 → IPv4 / IPv6 → IPv6`。
原生在线统计不足以提供每条连接的实际出口，需要移植主机侧观察器。
当前来源观察器是成功拨号的近期记录，没有关闭事件，不能称为实时活跃连接清单；
socket 本地源地址是 NAT 前地址，也不能单独证明 NAT 后公网源 IP。
CDN / 反向代理前的真实客户端地址依赖可信转发配置；不知道的字段保持未知。

## 验证与最终切换

```sh
python3 -m unittest discover -s tools/tests -v
go test ./internal/web/service -run 'TestMarzbanStage1NativeImport|TestExportImportPreserves|TestImportClients' -count=1
```

Python 从合成 SQLite 数据构造包，并与 Go 测试读取的同一个 JSON 样本比对；
Go 使用真实数据库模型及原生导入服务回读。均不连接生产节点，也不证明真实协议连通。

上线前另需在隔离环境核对真实副本与账户权益，完成每阶段开发；测试两名用户的固定
IPv6 入口不变、出口不同、重连保持、IPv4 目标 / 回退、普通直连、WG 及 CDN 路线。
最终切换需有用户授权、统一冻结旧系统的业务写入，保存各库一致性备份；保留旧核心产生的流量与分配必须按第四阶段说明单独收敛，
导入后逐记录核对，避免新旧系统混写。失败时停止目标写入，按保存的切换点恢复旧服务。

源项目详细合同：
[网络拓扑](https://github.com/abbhb/Marzban/blob/0ebdd8052743066fbe5d845153a6adc24334ea6a/docs/NODE-NETWORK-TOPOLOGY.zh-CN.md)、
[IPv6 出口](https://github.com/abbhb/Marzban/blob/0ebdd8052743066fbe5d845153a6adc24334ea6a/docs/NODE-IPV6-EGRESS.zh-CN.md)、
[连接观察](https://github.com/abbhb/Marzban/blob/0ebdd8052743066fbe5d845153a6adc24334ea6a/docs/HOST-CONNECTIONS.zh-CN.md)、
[MGMA](https://github.com/abbhb/Marzban/blob/0ebdd8052743066fbe5d845153a6adc24334ea6a/docs/MGMA.zh-CN.md)、
[门户交易](https://github.com/abbhb/Marzban/blob/0ebdd8052743066fbe5d845153a6adc24334ea6a/docs/PORTAL-COMMERCE.zh-CN.md)。
