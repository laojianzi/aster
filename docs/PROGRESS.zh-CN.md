# Aster 实施状态

2026-10-12 接续基线：已验证 main `7bf17ddbfaf87fa8ecb720b1e5e95e913823345d`，
源码树 `fb7705ea48eeb885c89362d69697346450086ba2`，Go 1.27.2 / MyGo v0.4.5。
#38/#41 已完成各自 PR、主干和实际分支结项；本轮唯一新增范围是 #42 的有界 Schema
约束辅助。这是开发预览。当前精确提交、CI 收据和 Issue 状态以 #42、其单一 PR
及 [#13](https://github.com/laojianzi/aster/issues/13) 的实时检查点为准。

## 已合并，不得重复实现

| 范围 | PR |
| --- | --- |
| 原生增量 TextBuffer、不可变快照、超限保留 | #36 |
| 原生有界 Unicode 字面 Find、选择/Undo/组合保护 | #34 |
| 原生资源/CRD、日志、转发、命令、安全变更与故障恢复 | #1、#3、#4 |
| 资源关系、Metrics、独立工作区与双集群隔离 | #5、#6、#7 |
| 无 force SSA、原生交互 Terminal/TTY | #8、#10 |
| 有界 exec 认证、到期清理、OS Token 凭据库 | #12、#19 |
| OpenAPI 字段帮助与结构提示、原始源码诊断导航 | #15、#30 |
| 浏览器 OIDC/PKCE、JOSE 更新、显式内存轮换与确认后连接替换 | #25、#26、#28 |
| 审阅原始发行方、精确确认后先本地清理再浏览器登出交接 | #38 |
| MyGo 0.2→0.3 行为迁移及普通/生产双模式门槛 | #22、#23 |
| 依赖与原生终端来源更新、MyGo 0.3→0.4 审计、0.4.2 合约 | #9、#16、#17、#20、#31、#33、#39 |
| 保留审批与禁止自动合并的空 Dashboard 自动关闭策略 | #40 |
| MyGo 0.4.5 全量来源审查、物理快捷键和跨行选择/历史合约 | #41 |

#2 已被不可变 Prepared/SSA 替代，保持关闭而不合并其旧实现。
#14、#18、#21、#24、#27、#29、#32、#35、#37 已完成。
历史分支清理保留恢复归档，不恢复旧分支或重新提交已完成范围。

## 已结项：#37 / #38 显式发行方登出

#38 复用了 `feat/native-oidc-signout`，合并为
`1dfc816fff2653844fe74f587ee645cce823dcd4`。单次登出能力绑定最初审阅的发现结果、
工作区与连接期限；原始发行方独立显示，不依赖续期是否启用或已遗忘。
精确上下文确认后先关闭本地客户端、观察、日志、终端和内存续期，再请求系统浏览器。
URL 不携带令牌；浏览器交接不声称发行方已完成登出或 Kubernetes JWT 已撤销。

四项 PR 验证、四项主干验证及原始证据均已核验，#37 已关闭。
[退役 38184766687](https://github.com/laojianzi/aster/actions/runs/38184766687)
返回 **applied**，完整历史 `before.bundle` 已验证；
远端和本地 feature 分支已删除，`maintenance/completed-pr-38` 指向 `1dfc816…`。
实现仍在接续基线可达。详见 [登出范围](oidc-signout.md) 与
[分支台账](BRANCHES.zh-CN.md)。

## 已结项：#11 / #41 MyGo 0.4.5

#41 复用了获批的 `renovate/mygo-native-framework` 分组，合并为上述 `7bf17dd…`。
0.4.2→0.4.5 审阅覆盖完整 20 个提交、325 个变更路径和实际 Aster API 使用；
982 个 module ZIP 文件与官方 tag 逐字节一致。28 个终端源文件、30 个生成文件、
加固补丁及六平台 Ghostty 库 pin 未变化；原生渲染和生成 GPU 着色器的变化单独审查。
Manifest/Find 的非拉丁物理快捷键与 Unicode 跨行选择/Undo 已进入三平台普通/生产
必跑清单。完整来源和验证边界见 [0.4.5 审查](dependency-review-mygo-0.4.5.md)。

最终五项 PR gate 与五项主干 gate、原始证据和实际分支结果均已核验。
Renovate 于 **2026-10-11T23:49:15Z** 自动关闭本轮空队列的 #11，并于 23:49:17Z
删除已合并的分组分支。全部主干门槛通过后的
[退役 38186078349](https://github.com/laojianzi/aster/actions/runs/38186078349)
因此返回 **already-absent**；没有创建新的 bundle 或 `maintenance/completed-pr-41`。
PR HEAD `c1c743e…` 是接续 main 的直接第二父提交，完整历史仍可达。
这与 #38 的实际删除/留档结果不同，不把不存在的收据补写为已完成。

Dashboard 审批、自动关闭和禁止自动合并策略保持有效。未来真实更新可以重开同一 #11；
关闭前正文保留的 #41 链接不是待继续实现的分支，空队列也不是漏洞清零证明。

## 本轮独立范围：#42 有界 Schema 约束辅助

唯一实现分支为 `feat/native-schema-constraints`，复用已有 Field help / Check draft、
当前身份 OpenAPI v3 请求、字段指针和原始源码导航。每次显式操作已经获取新 schema；
本轮不增加重复刷新功能或跨身份长效缓存。

新增范围是 scalar enum、数值 minimum/maximum 与 OpenAPI 3.0 布尔 exclusive 边界、
Unicode 码点 min/maxLength，以及 min/maxItems、min/maxProperties。精确整数和
schema `json.Number` 不经 `float64` 比较；已有浮点 draft 值按其 JSON 序列化值比较，
不宣称恢复 manifest 解码前已丢失的 YAML 数字词法或精度。

诊断不回显 draft/enum 值、默认值或示例，不修改 draft，不生成 Prepared 计划或发起变更。
不支持、格式错误、复杂 enum 和预算耗尽均显式标为检查不完整。pattern、format、
multipleOf、完整组合、列表语义与 CEL 等仍保留；server dry-run 继续决定实际准入。

本范围的必需证据包括精确数字/Unicode/预算/取消/隐私单测，保留 buffer、Undo/Redo
与选择的三平台原生回归、普通/生产双模式迁移，以及三个受支持 Kubernetes 版本上的
真实 CRD discovery/dry-run 对照。本文件记录契约，不把计划或局部预演当作最终资格；
最终提交、原始结果、合并及分支回收记录由 #42、其 PR 和 #13 跟踪。
详见 [Schema 合约](schema-assistance.md)。

## 后续优先级

P1 编辑/交付：受支持的安全显式补全事务、剩余高级 Schema/编辑辅助、Helm/GitOps
审阅交付。复用 TextBuffer/Find/Schema/Prepared，保留 Undo、IME 和不可变审阅边界。
MyGo 0.4.5 的外部 `TextBuffer.Replace` 仍重置原生历史；本次只读检查不绕过该约束，
也不以模拟按键、保留私有 handle 或第二套 history 实现补全。

P1 身份/企业：单独审查刷新凭据跨启动持久化、RFC7009 令牌撤销/联邦登出及可选自动
续期；企业 Gateway/Connector、租户隔离、可信策略/审批/审计与会话撤权。
浏览器登出交接不保证令牌撤销、通用发行方支持或其他工作区注销。

P2：持久设置、全局预算、历史监控/拓扑、工作负载聚合与日志检索。
发布：签名安装、验证更新/恢复、完整 SBOM/漏洞审查、长稳故障注入、物理键盘/IME、
字体覆盖、辅助技术、GPU 与混合 DPI 验收。现有未签名 CI 产物不是生产 Release；
继续保留 #13 的剩余产品和发布验收目标。
