# 第二阶段：主机、线路、固定 IPv6 出口

日期：2026-11-16。基于第一阶段 PR #1 的合并提交
`0d67b416914c3de3476975139edff566f369268e` 开发。
本阶段提供可部署代码与隔离环境验证；生产接管需要单独的切换窗口，不能让两个控制器同时管理真实分配库或 NAT 表。

## 页面与数据关系

侧栏增加“服务主机”，原来的 Hosts 标为“订阅入口”。服务主机表示物理机器；
原生 Node 表示一个服务实例；面板本机内核作为服务 0，可与远程服务一样归组。
不按控制地址相同自动合并主机，每个服务只能归属一个主机。

主机名称进入详情页面，设置表单在页面内，不使用模态窗。节点列表的名称也能进入所属主机；
尚未归组的节点会进入预选该服务的添加页面。URL 保留 `id` 与 `tab`，支持直接访问、刷新和返回：

```text
<面板路径>/panel/service-hosts?id=3&tab=connections
```

| tab       | 内容                                                               |
| --------- | ------------------------------------------------------------------ |
| 服务      | 本机 / 远程实例、控制地址、面板与内核状态                          |
| 线路      | 现有入站与订阅入口、地址族、协议、传输和出口约定                   |
| IPv6 出口 | 已安装实例的策略、真实控制器回读、用户分配；保存策略会重启对应服务 |
| 连接      | 主机下各服务近期成功拨号，支持按服务和用户筛选                     |
| 主机设置  | 主机名、服务归属和说明                                             |

线路出口约定已退为只读迁移记录，不提供修改分类的保存接口；它不会部署路由，也不代表当前生效状态。
实际服务器转发应由对应服务的原生出站与路由管理，后续收敛见[原生功能优先审查](NATIVE-FIRST.zh-CN.md)。固定入口、CDN 入口和用户出口分开显示：
同一入站的 IPv4 / IPv6 / CDN 别名无法仅按订阅名称区分服务端策略。
只接管特定 IPv6 入口时，需要先拆出可区分的监听 / 入站；独立 XHTTP 上下行与级联参数的完整迁移留在第四阶段。
主机分组或删除记录不停止服务、不改变订阅，也不撤销已安装的内核出口规则。

## 套餐具体线路授权

第一阶段遇到非空 `local_node_selections` 会停止。现在可通过显式映射，
将源项目的具体线路 SHA256（源线路目录中的 route ID）转换为目标原生 Host 行 ID。
先在隔离目标建立并核对对应入站和订阅入口，再准备：

```json
{
  "SOURCE_INBOUND_TAG": {
    "源目录中64位小写SHA256线路ID": [1001],
    "另一个64位小写SHA256线路ID": [1002, 1003]
  }
}
```

示例键需要替换成真实的 64 位摘要；数字为目标 `/panel/api/hosts/raw` 中的 Host ID。
同一源线路可对应不同服务的多个目标入口；不同源线路不能合并到同一目标 Host，
否则套餐投影会扩大权限。未受具体方案限制的入站也必须完整列出人工核对的源线路映射。
工具不会自动证明手工映射的传输、SNI、证书、XHTTP 上下行等参数等价，不能把多方案压成一个入口。

```sh
python3 tools/marzban_export.py export \
  --database .migration-private/marzban-backup.sqlite3 \
  --core .migration-private/core.json \
  --inbound-map .migration-private/inbound-map.json \
  --target-inbounds .migration-private/target-inbounds.json \
  --route-map .migration-private/route-map.json \
  --target-hosts .migration-private/target-hosts.json \
  --traffic-policy preserve-total-as-download \
  --output .migration-private/stage2-bundle
```

该模式导出的客户端设置 `strictHostBindings=true`。这是本 fork 增加的严格入口白名单：
没有匹配某入站的 Host 时，不回退为该入站全部方案；空白名单不发布任何入口。
普通 3x-ui 客户端保持原行为。严格模式在创建时确定，普通编辑不会关闭它；
迁移账号的数字用户编号不能通过改名更换。
导入前若目标已有同名的非严格客户端，会整体拒绝严格迁移导入，避免原生“复用已有用户”绕过授权模式。
请使用干净的演练目标，不能把反复导入当作升级或对账。

## 节点组件与启动兼容

本阶段不依赖 Marzban-Node。目标节点必须运行此 fork 的 3x-ui。
出口功能适用于具有可路由公网 IPv6 /64、`iproute2`、`nftables`、`curl` 和 Python 3.11+ 的 Linux 服务。
实际修改网卡和 nftables 的任务由 root 的本机定时器完成，管理 API 不执行任意 shell，也不能指定文件或可执行路径。

原生 3x-ui 启动方式是 `xray -c FILE`。新增 `XUI_XRAY_LAUNCHER` 适配：
面板将真实内核路径与原生参数传给启动器，启动器验证并通过私有 stdin 启动同一内核。
版本查询、内核文件路径及升级文件定位不变。配置了出口 bootstrap 却漏掉启动器时，拒绝启动；不会退回共享出口。

已安装启动器时，面板配置变化通过受保护的核心重启生效，避免后台热加载直接覆盖动态出口规则。
启动器为本机控制器添加私有 Unix socket，并保存基线路由；控制器只在明确选择的直连默认路径前插入规则，
保留既有阻断、用户定向、隧道规则和 freedom 的 `finalRules` 等设置。
不支持自动接管路由脚本、均衡器、redirect、既有 socket mark 或被选入站的默认隧道出口；遇到这些配置会停止。

所有新启动先安装未分配用户的阻断规则，**不信任上次 ready 缓存或仍存在的内核 NAT 表**。
控制器按永久数字用户编号读取 / 创建分配，依次完成 /128 配置、地址冲突检查、源地址返回流量验证、
IPv6 SNAT、出站和路由回读后放行。TCP 使用 IPv6 优先的 Happy Eyeballs；UDP 使用 IPv6 优先策略。
新增用户出站副本会移除旧 `freedom.domainStrategy` / `targetStrategy` 解析设置，统一由本策略的 socket 设置控制；
锁定版本的内核会把这些旧设置迁移到 socket 并覆盖新值。原始出站和未接管的线路保留原配置。
IPv4 字面目标保持 IPv4，IPv4 回退可使用共享 IPv4。NAT 只作用于本实例 `ip6 mz6_<namespace>` 中的 mark 映射。

用户身份继续使用 `<永久数字ID>.<用户名>`，与第一阶段导出的核心 email 一致。
改用户名或轮换 UUID 不改变数字编号和 /128。普通原生 3x-ui 名称没有该编号时会保持阻断，并在页面列出，
不会自动拿用户名、订阅 token 或凭据猜测身份。第三阶段门户将负责统一创建永久身份。

## 安装和一致性恢复

先在隔离测试服务演练。以下命令是部署步骤说明，本次开发不执行真实节点接管。
保留旧库的 `users`、`state`、`owned` 三张表、mark 前缀及所有孤立保留项；绝不能重新随机分配现有用户。
在同一物理主机部署多个策略实例时，namespace、mark 前缀和分配状态目录必须独立。

```sh
sudo install -d -m 0700 /usr/local/lib/3x-ui-egress /etc/3x-ui-egress/us03 /var/lib/3x-ui-egress/us03
sudo install -m 0755 tools/egress/launch.py tools/egress/manage.py /usr/local/lib/3x-ui-egress/
sudo install -m 0644 tools/egress/policy.py /usr/local/lib/3x-ui-egress/
sudo install -m 0644 tools/egress/xui-egress@.service tools/egress/xui-egress@.timer /etc/systemd/system/
```

将一致性备份恢复为 `/etc/3x-ui-egress/us03/allocations.sqlite3`，目录 `0700`、文件 `0600`。
创建同目录 `node.json`，以下占位值必须按目标实例替换：

```json
{
  "enabled": true,
  "namespace": "us03",
  "interface": "REPLACE_WITH_INTERFACE",
  "prefix": "REPLACE_WITH_ROUTED_PUBLIC_IPV6_64",
  "mark_prefix": 25903,
  "state": "/var/lib/3x-ui-egress/us03",
  "allocations": "/etc/3x-ui-egress/us03/allocations.sqlite3",
  "binary": "/usr/local/x-ui/bin/xray-linux-amd64",
  "inbounds": ["REPLACE_WITH_TARGET_SERVICE_TAG"]
}
```

`mark_prefix` 必须与恢复库一致，示例 `25903` 不能给其他并行实例复用；`binary` 必须等于该面板实际内核路径。
目标入站 tag 以目标服务自己的报告为准，不能直接使用主面板可能改名后的别名。
配置 `enabled=true`，让首启即受阻断保护。重启 / 恢复之前停止旧控制器与旧服务的分配写入；
旧系统与新系统不能共用活动数据库，或同时接管同名 nft 表。最终冻结与切换仍需对账号、流量和权益统一对账。

在该 3x-ui 服务的 systemd drop-in（容器需对应环境变量和挂载）设置：

```ini
[Service]
Environment=XUI_XRAY_LAUNCHER=/usr/local/lib/3x-ui-egress/launch.py
Environment=XUI_EGRESS_CONFIG=/etc/3x-ui-egress/us03/node.json
Environment=XUI_CONNECTION_OBSERVER=1
```

受控窗口内重启对应 3x-ui 服务，再启用 `xui-egress@us03.timer`。
本机核心及控制状态目录必须由该服务与 root 控制任务按上述私有权限访问；不能开放控制 socket。
父面板读取报告可用 node-sync token；策略保存及服务重启需 node-admin / admin 权限。
远程版本过旧、离线或权限不足时只显示该服务的错误，其余服务仍可查看。

```sh
sudo python3 /usr/local/lib/3x-ui-egress/manage.py --config /etc/3x-ui-egress/us03/node.json sync
sudo python3 /usr/local/lib/3x-ui-egress/manage.py --config /etc/3x-ui-egress/us03/node.json status
```

`status` CLI 只输出摘要；完整地址映射保留在私有状态文件和登录后的管理页面。
如需明确重置单个用户，CLI 要求永久编号与**当前地址**一起校验；旧地址继续保留为已占用，不会分给别人：

```sh
sudo python3 /usr/local/lib/3x-ui-egress/manage.py --config /etc/3x-ui-egress/us03/node.json \
  reset PERMANENT_USER_ID --expected-address CURRENT_USER_IPV6
```

重置失败时保持未核验，修复后重新 `sync`，不要重复随机重置。
关闭出口策略先恢复并回读基线路由，再移除本实例 NAT；分配库和入口不删除，重新启用仍复用原分配。

## 连接采集和证据范围

普通 Xray 的在线人数 / 来源 IP 不足以提供实际拨号远端。本 fork 提供显式启用的观察内核构建工具：

```sh
python3 tools/connection_observer/build.py --output .migration-private/xray-observer
```

它读取本仓库锁定的 Xray 模块版本，复制到临时目录，仅包装实际 `DialSystem` 返回后的获胜 socket；
不修改 Go 模块缓存、不安装、不重启服务。产物旁记录版本、模块校验和、补丁摘要、二进制摘要、平台和 Go 工具链。
通过受控内核替换流程安装到目标实际内核路径，保留原内核用于恢复。
常规内核升级可能覆盖观察内核；页面必须看到新的 ready 标记才能认定采集就绪。

内核只在 `XUI_CONNECTION_OBSERVER=1` 时输出 MZCONN v1。
面板逐行解析 stdout，最多保留每服务最近 500 条，重启清空；原始事件不写入普通面板日志。
事件包含认证身份、内核看到的客户端地址、目标、获胜拨号远端、本地 socket 地址、入站和出站，
不包含 UUID、Auth、密码和完整配置。损坏或超长记录丢弃，旧节点缺少能力时不伪造数据。

**近期成功拨号不是活跃连接清单**，没有关闭事件。socket 本地源地址位于 NAT 前；
页面“固定分配 IPv6”来自控制器配置与回读，不是该连接逐包测得的公网源 IP。
CDN / 反代前的客户端地址依赖可信原地址传递。地址族方向仅描述 core 看到的客户端与实际拨号远端。
控制状态超过一分钟、配置摘要不一致或核心代际不同会显示未核验。

## 验证与剩余阶段

```sh
python3 -W error::ResourceWarning -m unittest discover -s tools/tests -v
XRAY_OBSERVER_BINARY=/path/to/built-core python3 -m unittest discover -s tools/tests -p test_observer_runtime.py -v
sudo python3 tools/egress/runtime_lab.py lab /path/to/built-core
```

最后一项只在明确选择的 Linux root 隔离实验室运行，创建随机 netns 与临时状态，使用文档保留地址；
不读取真实配置，也不接触生产接口 / nft 表，结束清理自建资源。
它验证旧分配库恢复、双用户 TCP / UDP SNAT、固定 IPv6 接入、旧 IPv4 解析配置下真实双栈域名的 IPv6 优先、
IPv4 目标与回退、受保护重启、关闭后恢复共享出口。
这不代替运营商公网 /64、真实 REALITY / HY2 / CDN / WG 路线及全部客户端的生产验收。

第三阶段继续门户、交易与 MGMA；第四阶段继续高级 XHTTP / 级联、外部订阅、规则 / DNS / 策略组及最终切换。
现有 Marzban 的已启用范围和未对齐项仍以[源网络拓扑记录](https://github.com/abbhb/Marzban/blob/0ebdd8052743066fbe5d845153a6adc24334ea6a/docs/NODE-NETWORK-TOPOLOGY.zh-CN.md)为基线，
不能把普通 IPv6 拨号或本阶段的实验室结果写成所有生产线路已经完成按用户出口。
