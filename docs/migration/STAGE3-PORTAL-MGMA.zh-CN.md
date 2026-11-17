# 第三阶段：门户、交易与 MGMA

当前状态：剩余旧服务已于 2026-11-18 完成交接并解除新业务冻结。本文中的旧核保留、阶段性限制和冻结步骤是历史交接说明；现行状态见[最终交接记录](FINAL-CUTOVER.zh-CN.md)。

本阶段基于 PR #2 的合并提交 `a52b7dd42f57191f8c39d09466f4f992e9b30868`。
来源合同是 `abbhb/Marzban@0ebdd8052743066fbe5d845153a6adc24334ea6a` 的
`PORTAL-COMMERCE.zh-CN.md`、`MGMA.zh-CN.md` 及对应数据库、服务、路由实现。
本阶段提供隔离迁移演练与功能实现；**没有接管生产、冻结旧写入或完成真实线路验收**。

## 页面与权限

- 用户入口：`<webBasePath>portal/`。独立的登录 / 邀请注册、钱包余额、当前套餐、
  用量、可购套餐、钱包流水、密码修改及 MGMA 链接。页面不复用面板管理员登录。
- 管理入口：面板的“门户与交易”。账号详情使用页面导航，包含赠送 / 充值 / 续期、
  账本、购买记录、MGMA、待生效套餐；另有套餐、邀请码、IP 封禁、设置和下发状态页。
- 门户账号是 `portal_accounts`，绝不写入面板管理员 `users`。
  管理能力只授予 3x-ui 完整管理员会话 / admin API token；monitor、node-sync、node-admin
  均不能读写门户账目或签发 MGMA。旧 Marzban 管理员表不自动导入。
- 用户 JWT 有独立签名域、`aud=portal`、账号 ID 与认证版本，最长 24 小时。
  修改 / 重置密码、停用账号使旧会话失效。迁移后用原密码重新登录，旧门户 JWT 不延续。
- 用户 JWT 存在当前标签的 sessionStorage；MGMA 明文 URL、一次性密码和邀请码只存在
  页面内存，隐藏、切页或页面进入后台时清除，有限期 URL 到期清除。
  用户主动复制到剪贴板或客户端后的副本由用户保管。
- 开放门户前，在“设置”写入公共地址并配置服务环境中的 MGMA pepper。默认关闭。

API 同时登记在面板 API 文档 / 生成的 OpenAPI：
`/api/portal/*` 返回直接 JSON，使用门户 JWT；
`/panel/api/commerce/*` 返回原生 `Msg` 包装，使用完整面板权限和原生 CSRF。

## 身份、钱包与套餐

| 对象 / 操作  | 约定                                                                                                                    |
| ------------ | ----------------------------------------------------------------------------------------------------------------------- |
| 门户账号     | 用户名去空格、转小写，3–32 个 ASCII 字符；导入保留 bcrypt 哈希，兼容旧 72 字节截断边界                                  |
| 永久代理身份 | 核心 email 为 `<永久数字ID>.<用户名>`；账号 ID、客户端表 ID 均不是新用户的分配来源                                      |
| 分配预留     | `portal_identities` 单独分配，导入全部旧用户 ID 与分配库孤儿 ID；不回收、不重排                                         |
| 注册         | 邀请码验证与次数消费在创建账号事务内；只建账号，首次购买 / 赠送才创建代理                                               |
| 金额         | CNY 整数分；单次充值 / 套餐价格最多 `10^12` 分，钱包最多 `9×10^15` 分，余额不得为负                                     |
| 充值         | 正金额钱包流水，记录管理员、备注、变更后余额                                                                            |
| 购买         | 扣款、购买记录、当前权益、原生客户端 / 入站绑定、流量重置及下发 outbox 一起提交                                         |
| 赠送         | 不扣款，可以选择隐藏套餐；仍写不可变赠送记录并重置周期                                                                  |
| 重复购买     | 覆盖单一当前套餐，用量归零，到期从现在重算；保留 email 数字身份、已有 UUID / HY2 Auth、稳定路径；新增协议只补齐缺失凭据 |
| 续期         | 只延长 `max(现在, 原到期)`；累计当前套餐天数，不扣款、不重置流量，不复活手动停用或已耗尽的额度                          |
| 停用权益     | 停止代理能力，保留账户与历史；停用账号还会使客户登录失效                                                                |
| 历史         | 购买、钱包流水、邀请码使用记录、周期用量重置记录只追加；套餐通过隐藏下架，不提供硬删除                                  |
| 套餐编辑     | 当前入站和具体订阅方案授权随套餐变化；已购价格、额度、期限保留历史快照                                                  |

充值的幂等命名空间与购买 / 赠送 / 续期 / 停用分开。每次请求必须提供
`Idempotency-Key`（1–128 字符）；同一 key 重试返回首次结果，不再次扣款，
变更操作类型或关键内容返回 409。服务在读余额前锁定账户，数据库事务同时保护
SQLite 与 PostgreSQL。账本写入、约束检查或 COMMIT 失败会回滚整个操作。

页面把尚未确认的购买 key 和套餐 ID 保存在当前标签 sessionStorage，重试沿用同一个 key；
确认完成或明确的业务拒绝才解除阻断。管理员的财务请求也保留待确认请求，避免重试生成新扣款。
创建托管账号与赠送同事务完成，一次性密码不持久化；如创建已成功但响应丢失，使用密码重置恢复。

导入的旧账本从零余额连续核对至当前余额，不凭空补“期初余额”。旧系统没有保存完整首次
响应体，因此导入旧幂等 key 时保留其扣款和购买结果，使用导入时可验证的账号视图构造重放响应；
旧请求的原始时刻用量 / 页面状态无法重建。不同内容复用旧 key 仍会拒绝。

## 核心下发和计费周期

业务事务不执行网络调用或重启核心；它提交原生客户端、绑定、节点 dirty 标记、待重置记录和
`portal_runtime_changes`。每 10 秒重试配置下发；离线节点不会导致再次扣款。
列表按最近尝试时间轮转，避免离线任务长期挡住后续任务。

本机重置等待新的核心代际，期间过滤旧代际统计；远程重置走已有 `NodePendingReset` 流程。
历史重置用量单独保留，当前用量和累计用量分开显示。来源只保留上传+下载总量，导入沿用
`up=0, down=原总量` 作为配额基线，不能把它解释为真实历史下载量。

“已下发”只表示期望配置与重置工作已被服务侧确认；不能证明真实客户端连接、目标出口、
NAT 后公网源地址或全部既有会话已关闭。核心手动停止时不会由本 worker 擅自启动。

`next_plans` 保留源字段：绝对 Unix 秒到期、额度、`fire_on_either`、`add_remaining_traffic`。
达到额度或到期条件时一次应用；关闭 `fire_on_either` 时必须两者均满足。
**源实现的 `add_remaining_traffic=false` 才结转剩余流量**，不是按字段字面含义反转；
页面用“不结转原剩余流量”表达 true。负结转或溢出会保留排队记录并显示失败；
一个失败记录不会阻止其他用户与下发 outbox。源手动 disabled 用户的排队记录保持等待。

## MGMA 鉴权

- 稳定路径保存为原生 SubID，bearer 是独立的 32 字节随机值（43 字符 Base64URL）。
  数据库仅保存 `HMAC-SHA256(pepper, bearer)`，明文只返回一次。
- 每次签发替换上一 digest；TTL 为 0 时永久，否则为 30–315360000 秒，默认 180 秒。
  修改全局 TTL 不改写旧授权的实际到期；撤销清除 digest。
- single-use 通过条件 UPDATE 原子消费；并发下载只允许一位获胜。HEAD 不消费，HTML
  请求拒绝且不消费；关闭 single-use 不会复活已消费的旧授权。
- 错误、过期、空、重复、损坏、来源拒绝、未知 token 均返回无细节 404；显式 token
  绝不会回退为旧签名链接。稳定 SubID 裸路径在所有模式下都要求 bearer。
- `legacy/dual` 仅允许已导入用户的旧源签名 URL（旧 HS256 JWT 与自定义短签名）；
  `ephemeral` 禁止旧签名。源签名 key 只从环境读取，并保留用户创建 / 撤销时间。
- 完整重置旋转稳定路径、UUID、HY2 Auth，并撤销 MGMA 和旧签名；永久数字出口身份不变。
- 原生 raw / Xray JSON / Clash / Mihomo 与显式格式别名共用鉴权；`info/usage` 返回配额信息。
  原生 Clash Legacy 不支持的 VLESS 仍返回 422，不能误报成鉴权成功的可用配置。
- 先缓冲生成结果，发送前复核授权代际、稳定路径、用户状态和来源。并发重置时旧授权
  不能读到新凭据。共享 SubID 涉及受管身份时拒绝，避免一个原生聚合 URL 绕过鉴权。
- HTML 订阅页拒绝；响应 `no-store`、`no-referrer`、`noindex`，移除永久 profile 页面链接。
  反代访问日志也必须避免记录查询字符串，缓存必须关闭。

`requireMGMA` 是独立于严格 Host 授权的客户端标记，随原生节点客户端快照传播，只能加强。
子节点没有对应本地授权状态时拒绝该客户端的所有订阅格式，不能把同步来的 UUID / SubID
转成永久公开订阅。**正式接管前，所有相关主节点、子节点和中间级节点都必须升级到包含
本阶段实现的 fork；旧版 / 上游版子节点不支持该标记，混合版本不属于安全接管范围。**
MGMA 签发继续由保存门户账目的主面板承担，不向节点复制 bearer 摘要或钱包数据。

## 来源规则、反代与失败封禁

TCP 请求只认真实 peer IP，忽略 `X-Real-IP`、`X-Forwarded-For`；来源有 any、china、custom、
china_or_custom。内置 APNIC 中国 CIDR 快照与来源项目完全一致：7500 条，数据截至 2026-07-10，
序号 20260712，SHA256 `7b8bb77937e04c3f56b5825a10e4fe2715ba52c8dd9f4a8c3609faa7b6f72d7f`。
它是网段分配快照，不是实时地理定位。

反代需要正确识别访客来源时，显式使用私有 Unix listener：

```ini
# systemd 环境配置示例，路径和服务用户需按部署核对
XUI_WEB_UNIX_SOCKET=/run/x-ui-private/panel.sock
XUI_SUB_UNIX_SOCKET=/run/x-ui-private/sub.sock
XUI_MGMA_TOKEN_PEPPER=<从受控环境安装的原 pepper，至少 32 UTF-8 字节>
XUI_MARZBAN_SUBSCRIPTION_KEY=<原 Marzban jwt.secret_key；兼容旧签名时需要>
```

运行目录由服务用户拥有，与反代共用专用组，建议 `02750`，socket 为 `0660`；不向其他用户
开放目录。监听器用 `.lock` 的持有期锁阻止并发启动，只清理已失效的 socket，拒绝替换普通文件。
Nginx 同时代理 panel 与订阅服务；以服务器配置的原订阅前缀映射到 sub socket，余下门户 / API
映射到 panel socket，并用 **`proxy_set_header X-Real-IP $remote_addr`** 覆盖来源，不传客户端自带值。
不要把公共 TCP 入口当作 Unix 入口，也不要把任意转发链当作可信来源。
旧 URL 前缀需要与来源一致，或在反代提供对应重写；仅保留 SubID 不会自动迁移 DNS / 路径。

登录限流为每 IP 60 次 / 5 分钟、每 IP+用户名 5 次 / 5 分钟，注册为每 IP 5 次 / 分钟。
持久失败封禁默认登录 8 次 / 15 分钟、注册 5 次 / 10 分钟，封禁一天，可配置永久。
失败计数键最多 10000 个。封禁先于请求体解析；完整管理员恢复 API 保持可达。
HTTP 层规则覆盖门户、登录和 MGMA；来源系统可选的宿主机 nftables 面板端口防护服务
本阶段未移植，可在第四阶段宿主机交付时处理；不能把 HTTP 封禁称为宿主机网络层丢弃。

## 冻结副本导出与原子导入

先准备阶段 2 的目标主机、入站和具体 Host 方案映射。在隔离实例上操作；目标不能承载现有
客户端、门户账户或账本。**导入器拒绝非空客户端 / 门户目标**，不要在已运行的演练库上反复覆盖。
重新演练应创建新的独立目标副本。

```sh
# 源 pepper 只通过进程环境提供，导出包仅保存指纹和旧 digest
python3 tools/marzban_portal_export.py \
  --database .migration-private/frozen-marzban.sqlite3 \
  --core .migration-private/core.json \
  --inbound-map .migration-private/inbound-map.json \
  --target-inbounds .migration-private/target-inbounds.json \
  --route-map .migration-private/route-map.json \
  --target-hosts .migration-private/target-hosts.json \
  --traffic-policy preserve-total-as-download \
  --ipv6-allocation us03=.migration-private/us03-allocations.sqlite3 \
  --output .migration-private/stage3-bundle

# 必须先停止隔离目标的面板与核心；--offline 是操作方的明确确认
# XUI_DB_FOLDER / PostgreSQL DSN 指向新隔离库，不是生产库
./x-ui import-portal --bundle .migration-private/stage3-bundle --offline
```

导出读取独立静止 SQLite，拒绝非空 WAL，校验读取前后摘要。目录 `0700`、文件 `0600`，
manifest 最后写入；包含账号 bcrypt、全部购买与流水、当前套餐、邀请码摘要与使用历史、
IP 规则、失败计数、MGMA 设置 / digest、旧签名身份时间、待生效套餐、历史周期用量及分配预留。
`allocations/` 原样保存完整分配库，含孤儿记录，不自动回收。
`stage4-private.json` 额外保留原始交易相关表（含无法归属现存用户的旧周期用量），
与套餐私有元数据一起保存外部订阅密文 / 引用和规则，等待第四阶段处理；
对应套餐标记待兼容并阻止购买 / 赠送。外部密文解密所需的源环境密钥应另行受控保存。

Go 导入器验证 manifest 成员 / 摘要 / 路径、旧密钥指纹、余额连续性、凭据、引用、额度与
具体授权，在一个数据库事务里写入并逐条回读。任何校验 / 插入 / 回读失败整体回滚；
PostgreSQL 显式 ID 导入后推进序列。报告列出核对行数、客户端 / 账号 / 账本数量与包摘要，
始终 `ready_for_cutover=false`。导入不会启动核心、联系节点或发布任何 URL，门户保持关闭。

如有意不延续旧授权，操作方必须显式指定 `--revoke-legacy`（切为 ephemeral）及 / 或
`--revoke-grants`（清空旧 bearer digest）；缺失或不匹配密钥不能默默通过。
旧签名密钥和 pepper 的明文不写入包，客户端 UUID / HY2 Auth 等仍属于私有凭据，整个包禁止入 Git / CI。

第四阶段的外部订阅、级联、XHTTP 上下行与规则 / DNS 实现及切换说明见
[第四阶段文档](STAGE4-SUBSCRIPTIONS-CUTOVER.zh-CN.md)。切换前仍需真实双用户网络验收；
统一冻结旧系统账号、交易、流量、分配写入，保存一致切换点，导入后再逐账户 / 每条账目对账。
回退时先停止目标写入，再恢复原切换点的旧库、分配库、旧服务与反代。不能混合两边继续写。

## 验证记录

仓库仅使用合成数据：

- Python 生成的交易样本与 Go 真实导入器使用同一 JSON；测试余额断链、缺失 pepper、
  外部权益阻断、私有权限与完整分配库 / 孤儿 ID 保留。
- SQLite 购买、同 key 并发重放、流水失败回滚、套餐路由变更与金融快照分离、邀请竞争、
  密码版本失效、待生效套餐和历史用量回读。
- PostgreSQL 16 上 12 个独立 SQL 事务重放同一购买只扣一次、余额不足拒绝，以及延迟外键
  触发的真实 COMMIT 失败回滚；该测试加入 CI，禁止静默 skip 当成功。
- 原生订阅渲染回读导入 VLESS / HY2 凭据；旧 bcrypt 登录、旧签名地址、旧永久 MGMA digest、
  最新替换、来源 / 伪造头、单次消费并发、过期、共享 SubID、节点无授权状态及渲染期间重置。
- 门户组件验证购买请求 key 恢复 / 重试及 URL 隐藏 / 到期清除；Storybook 检查订阅控件可访问性。

这些证据不替代生产副本对账、所有子节点升级或真实客户端出口验收。
