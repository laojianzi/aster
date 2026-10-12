# Aster 实施状态

2026-10-12 接续基线：已验证 main `841745bf0b2dd0f52f994cae31a464c5dbc72a5c`，
源码树 `460306069e410276ec1dda6124f077fc60f2c7fe`，Go 1.27.2 / MyGo v0.4.5。
#38/#41/#43/#45/#47/#49/#51/#53 已完成各自 PR、主干和实际分支结项；本轮唯一新增范围为 #54 的
有界 Kubernetes map-list 字符串键身份提示。这是开发预览。当前精确提交、CI 收据和 Issue
状态以 [#54](https://github.com/laojianzi/aster/issues/54)、其单一实现 PR 及
[#13](https://github.com/laojianzi/aster/issues/13) 的实时检查点为准。

## 首版 MVP 范围

按 2026-10-12 的用户决定，企业权限体系改为 **P3，暂缓，不纳入首版 MVP**。
Gateway/Connector、租户隔离、可信策略/审批/审计与企业会话撤权不是当前实现任务或
首版 MVP 验收门槛。首版面向直接连接 Kubernetes 的原生桌面；现有凭据保护、当前
身份授权、不可变审阅变更和本地生命周期清理要求继续有效。其他优先级不随之降级。

## 已合并，不得重复实现

| 范围 | PR |
| --- | --- |
| 原生增量 TextBuffer、不可变快照、超限保留 | #36 |
| 有界 scalar enum、精确数值边界、Unicode 长度与容器大小 Schema 提示 | #43 |
| 有界精确 decoded-number multipleOf、数学对照与实际服务器容差边界 | #47 |
| 有界可移植字符串 pattern、独立有限语言对照与真实 CRD 验证 | #49 |
| 有界 date/byte/uuid 格式提示、独立公历/编码/语法对照与真实 CRD 验证 | #51 |
| 有界结构化 enum、完整对象/有序数组等值和实际服务端类型/nullable 边界 | #53 |
| 已渲染清单有序导入、逐对象审阅交付与部分结果核对 | #45 |
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
#11、#14、#18、#21、#24、#27、#29、#32、#35、#37、#42、#44、#46、#48、#50、#52 已完成。
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

#41 复用了获批的 `renovate/mygo-native-framework` 分组，合并为 `7bf17dd…`。
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
PR HEAD `c1c743e…` 是 #41 merge `7bf17dd…` 的直接第二父提交，也是现 main
`387ee43…` 的祖先，完整历史仍可达。
这与 #38 的实际删除/留档结果不同，不把不存在的收据补写为已完成。

Dashboard 审批、自动关闭和禁止自动合并策略保持有效。未来真实更新可以重开同一 #11；
关闭前正文保留的 #41 链接不是待继续实现的分支，空队列也不是漏洞清零证明。

## 已结项：#42 / #43 有界 Schema 约束辅助

已复用 Field help / Check draft、当前身份 OpenAPI v3 请求、字段指针和原始源码导航。
每次显式操作已经获取新 schema，无需增加重复刷新功能或跨身份长效缓存。

已交付 scalar enum、数值 minimum/maximum 与 OpenAPI 3.0 布尔 exclusive 边界、
Unicode 码点 min/maxLength，以及 min/maxItems、min/maxProperties。精确整数和
schema `json.Number` 不经 `float64` 比较；已有浮点 draft 值按其 JSON 序列化值比较，
不宣称恢复 manifest 解码前已丢失的 YAML 数字词法或精度。

诊断不回显 draft/enum 值、默认值或示例，不修改 draft，不生成 Prepared 计划或发起变更。
不支持、格式错误和预算耗尽均显式标为检查不完整。#42 当时不含的复杂 enum 由 #52
继续有界扩展；其他 format、完整组合、列表语义与 CEL 等仍保留。multipleOf 已由 #46
完成，pattern 已由 #48 完成，date/byte/uuid 已由 #50 完成，server dry-run
继续决定实际准入。

最终 PR HEAD `16fa04b7446a0975f4cc1e913d91452442510e6c` 合并为 `01089da…`，
源码树相同。四项最终 PR gate、四项精确 main gate 和原始证据均已核验，包括
精确数字/Unicode/预算/取消/隐私单测、三平台原生历史与选择、普通/生产迁移及
三个受支持 Kubernetes 版本上的真实 CRD 对照。#42 已关闭。
[退役 38196067007](https://github.com/laojianzi/aster/actions/runs/38196067007)
报告 **applied**，完整历史 bundle 及独立恢复已核验，准确 feature 分支已回收，
`maintenance/completed-pr-43` 指向 `01089da…`。不重做已完成的 CI、合并或清理。
详见 [Schema 合约](schema-assistance.md) 和 [#43](https://github.com/laojianzi/aster/pull/43)。

## 已结项：#44 / #45 已渲染清单审阅交付

使用 `feat/reviewed-manifest-delivery` 和 [PR #45](https://github.com/laojianzi/aster/pull/45)。粘贴最多 32 个对象 / 2 MiB 的
已渲染 YAML，以当前身份重新发现准确类型，明确解析到一个已存在的 namespace。
完整有序清单逐对象执行 GET、strict dry-run、不可变预览、精确名称确认和单次提交；
只有实际 NotFound 使用 POST/create-only，已有对象使用无 force SSA 与 UID/RV 约束。

已接受、失败、Unknown 和尚未提交的结果分别保留。失败或取消使队列停止，后续必须
显式重新核对状态和审阅；不自动批量写入、重试、回退、回滚或 prune，不声称原子事务。
POST 的 `aster-desktop` 所有权不会自动迁移给 `aster-apply`：后续意图可以管理新字段，
但修改原创建方仍拥有的字段会冲突，须回到拥有该字段的工作流审查处理。

剪贴板按当前文本加待粘贴文本的字节、换行及最长行保守上界预检；高风险内容完整
单独保留，原稿不变，显式 **Discard retained paste** 前暂停导入。Back 保留原稿、
集合、结果和待处理粘贴，但 Import/清单视图/Back 不保证先前原生 Undo/Redo 与选区
跨视图保留。原生 Type/IME 的任意巨型单段输入仍可能在下一帧检查前阻塞 MyGo；
该范围剪贴板预检不覆盖它，也不将该上游安全编辑器及发布验收边界写为已解决。

本范围不安装 CRD/Namespace、不写 Secret、不执行渲染器/命令/URL/仓库，也不实现
Helm release 或 GitOps reconcile。三平台原生生命周期及两模式迁移、三版本真实
多对象交付/并发/所有权/RBAC/丢响应/显式重审和 native E2E 均已通过。PR 和精确主干
四项门槛各自完成 14 份新 ZIP 和六个完整二进制审阅；main 为 `387ee43…`。
[退役 38202506391](https://github.com/laojianzi/aster/actions/runs/38202506391) 返回
**applied**，完整 bundle 在全新空 bare 库独立恢复并通过 strict fsck，旧 14 个 tag
指向未变，新增 `maintenance/completed-pr-45` 指向该 merge。#44 在人工主干审阅及
恢复检查后于 2026-10-12T04:17:21Z 明确关闭；远端和本地 feature 已回收。
详见 [交付合约](manifest-delivery.md) 和 [最终 PR 收据](https://github.com/laojianzi/aster/pull/45)。

## 已结项：#46 / PR #47 精确 multipleOf 提示

复用 Check draft、已规范化的十进制值、共享预算与原始源码诊断导航。正数除数的
整倍数条件按实际 decoded JSON number 精确判断；零、负值、超过 2^53 的整数和
巨大合法指数均保留其数值语义。模运算不展开巨大十次幂，数字 token 仍限 512 字节，
算术按系数字长和指数位数计入共享 20,000 工作量。非法除数、超预算和不可检查的
数字保留 incomplete；错误类型不隐藏非法元数据，诊断不显示值或除数。

Kubernetes 浮点容差可能接受某些局部 exact 提示的邻近小数；本地提示不能替代
server dry-run，也不改成 epsilon 比较。单测独立使用有理数作为数学对照；原生测试
覆盖精确输入、Unicode 选区/原有 Undo/Redo、最小窗口和不完整输出；真实 CRD 比较
二进制可精确表示的除数，并单独验证命名的容差边界。

最终 HEAD `10ad0c1eee337d3b4fd9d20abdbb7d51d120405d` 合并为 `45f897f…`。
修正后的四项 PR 门槛与四项新 main 门槛，各自审阅 14 份原始 ZIP 和六个完整二进制；
未采用旧失败候选结果。主干原始证据包含 595 次独立数学比较、三版本实际 CRD 与原生
用例，原有必跑测试全部保留。[退役 38215920851](https://github.com/laojianzi/aster/actions/runs/38215920851)
返回 **applied**；完整历史 bundle 在空 bare 库独立恢复并通过 strict fsck。
原 15 个 tag 的准确指向不变，新增 `maintenance/completed-pr-47` 指向 merge；远端和
本地 feature 已回收。#46 在主干人工审阅及恢复核验后于 2026-10-12T07:15:23Z
明确关闭。详见 [Schema 合约](schema-assistance.md) 和 [PR #47](https://github.com/laojianzi/aster/pull/47)。

## 已结项：#48 / PR #49 有界可移植 pattern 提示

使用唯一 `feat/native-schema-pattern`，复用每次显式 Check 的当前身份 OpenAPI 请求、
共享预算、原始源码诊断和原生编辑历史。以 ECMA-262 5.1 与 Kubernetes 所用 Go regexp
的保守公共子集检查字符串：BMP 字面值、正向字符类、分组/选择、贪婪或懒惰量词及无 flags
的显式首尾锚点；默认匹配子串，允许输入包含 emoji。字符类整个范围必须避开 surrogate
区间，不把 Go 成功编译等同于跨方言支持。未支持语法、非法元数据和预算耗尽显式 incomplete。

模式限 512 字节、输入限 16 KiB、分组深度 32、编译指令 256，重复展开前先核保守上界。
解析/编译/匹配共享 20,000 工作量和 8 MiB 输入预算；不暴露模式、值或解析器错误。
678 次独立有限语言对照、Unicode/锚点/空值/错误类型、原生修正与 mounted Undo/Redo、最小窗口、
三版本实际 CRD/dry-run 及两模式迁移均已通过本范围必跑门槛。

最终 HEAD `0852baced6a2ceec6ad7cc99c25f6f876d9a4f2f` 合并为 `b1bfdf8…`。
四项最终 PR 与四项新主干门槛分别完成原始证据和六个完整二进制审阅。
[退役 38224257797](https://github.com/laojianzi/aster/actions/runs/38224257797)
报告 **applied**；完整历史 bundle 在新空 bare 仓库独立恢复并通过 strict fsck，
旧 16 个 tag 指向不变，新增 `maintenance/completed-pr-49` 指向 merge。
远端及本地 feature 已回收；#48 在主干人工审阅及恢复核验后于
2026-10-12T08:41:55Z 明确关闭。
详见 [PR #49](https://github.com/laojianzi/aster/pull/49) 和 [Schema 合约](schema-assistance.md)。

## 已结项：#50 / PR #51 有界字符串 format 提示

已通过 `feat/native-schema-format` 复用显式 Check、当前身份的新鲜 Schema、
共享预算和原始源码/原生编辑历史。仅实现三个固定 Kubernetes 版本行为一致的字符串
`date` / `byte` / `uuid`。格式名只删除 ASCII 连字符；date 验证真实公历并允许年 0000，
byte 检查非空 Base64 形状且不解码内容，uuid 的四个连字符分别可选，不附加版本限制。

原始格式名限 64 字节、decoded 输入限 16 KiB，名称/输入扫描预先计入共享工作和字节
预算。空、未知、非字符串、无效 UTF-8 或超限元数据保持 incomplete；错误类型不隐藏
元数据问题，也不产生次生 format 违规。值、格式元数据和解析错误不进入诊断。

独立公历/编码/语法对照、预算/取消/隐私与输入不变、原生选择/Undo/Redo 和最小窗口、
三个 Kubernetes 版本的真实 CRD create/live dry-run 均通过新增必跑门槛，保留原有全部
pattern/multipleOf/来源/凭据库/OIDC/双模式迁移门槛。没有新增依赖或长效缓存。
最终 HEAD `0c10a56180658062352886fc63c6bf7336ec900c` 合并为 `2186a23…`。
最终 PR 和精确新主干各自完成原始证据、完整二进制和实际截图的独立助手审阅。
[退役 38231769807](https://github.com/laojianzi/aster/actions/runs/38231769807)
返回 **applied**；完整历史 bundle 在新空 bare 仓库独立恢复并通过 strict fsck，
旧 17 个 tag 指向未变，新增 `maintenance/completed-pr-51` 指向 merge。
远端和本地 feature 已回收，#50 于 2026-10-12T09:59:21Z 明确关闭。
详见 [Schema 合约](schema-assistance.md)。

## 已结项：#52 / PR #53 有界结构化 enum 提示

唯一 `feat/native-schema-structured-enum` 将精确 decoded JSON 比较递归扩展到对象和数组。
对象完整键集合与值相等、键顺序无关；数组保留顺序和重复次数。缺失、null、空对象和
空数组保持区别，字符串与键不做 Unicode 归一化，嵌套数字复用既有精确十进制合约。
纯 scalar 快路和旧预算保持；新路径每候选/实例限制深度 32（根为 0）、1,024 值节点和
每键/字符串 16 KiB，共享 20,000 工作与 8 MiB 字节预算，分配/扫描/键查找/比较预收费。

必须先完整检查所有候选元数据，早期匹配不能隐藏后续错误或超限。错误类型旁仍检查
元数据，但不遍历错误类型的 draft 容器；取消/耗尽只披露 incomplete，不编造 enum 违规。
提示只包含原始父字段指针和固定类别文字，不回显嵌套键值或改动 draft、选择、Undo/Redo、
Prepared 与服务端。原有三个 complex-enum 测试保留名称和输入，更新为现已支持的结果，
新增真正无效及晚出现的预算回归；其余旧门槛全部保留。

独立 JSON/有理数对照、三平台原生交互和双模式迁移、三版本真实结构化 CRD/dry-run 是
新增门槛。服务端只转换枚举最外层 Go 类型，不对嵌套数字归一化；nullable enum 的具名
差异与一致案例分开。裁剪、默认值、ratcheting、列表/CEL/webhook 仍由实际 dry-run 决定。
最终 HEAD `60f412b6…` 合并为接续 main `841745bf…`，tree `46030606…`。
最终 PR 和精确 main 分别审阅 14 份新 ZIP、6 个完整二进制、24 份 raw / 4,526 条通过
记录（含跨平台/模式重复）及 81 张实际查看的图像。主干 CI/迁移的准备阶段 HTTP 504
与原始重试来源均保留，旧必跑门槛完整。[退役 38247964969](https://github.com/laojianzi/aster/actions/runs/38247964969)
实际 applied；完整历史在新空 bare 仓库独立恢复，15 项 Git 检查含 strict fsck 通过。
原 18 个 tag 指向不变，新增 `maintenance/completed-pr-53` 指向 main。自动退役先于
独立原始产物审查；恢复与本地清理在审查后完成，#52 于 2026-10-12T12:32:07Z 明确关闭。

## 本轮唯一范围：#54 有界 map-list 字符串键身份提示

唯一 `feat/native-schema-list-map` 复用当前身份的新鲜 OpenAPI、Check draft、预算和原始
父字段导航。仅检查 `x-kubernetes-list-type: map`、显式 required/nonnullable string keys；
完整 tuple 相同即重复，其他 payload 不参与身份。键元数据和全部条目先检查完，晚出现
的缺键、null、类型错误、超限或取消不会被早期重复掩盖。固定通用提示不回显键名和值。

键名限制 1–8 个、各 1–256 UTF-8 字节，数组最多 256 条，每个键值最多 16 KiB；解析、
引用、分配、扫描和散列预先计入既有共享预算。默认-only/非字符串键、set/atomic、普通
uniqueItems 和完整列表合并仍属后续范围。CRD 本身禁止 `uniqueItems: true`，不能把
通用 JSON Schema 的这一关键词当作可发布 CRD 功能。默认、裁剪和旧错误对象的更新
豁免由服务端决定；本地不会修改草稿或模拟转换。

新增独立 tuple 对照、原生选择/历史/修正重查/最小窗口，以及三版本真实 CRD create/live
dry-run。原有全部门槛和四项 PR/主干资格继续保留；本节不预先声称实现已完成验收。

## 后续优先级

P1 编辑/交付：#44/#45 已交付已渲染清单的逐对象审阅；#46/#47 已完成精确 multipleOf，
#48/#49 已完成有界可移植 pattern，#50/#51 已完成 date/byte/uuid，#52/#53 已完成结构化 enum；
#54 推进有界 map-list 字符串键身份提示。
这些子集之外的 pattern/format、组合/列表/CEL、编辑辅助、
真正的 Helm 渲染/release 与 GitOps 同步/reconcile 仍为后续独立范围。
受支持的安全显式补全事务继续等待原生编辑器契约，复用 TextBuffer/Find/Schema/Prepared，
保留 Undo、IME 和不可变审阅边界。
MyGo 0.4.5 的外部 `TextBuffer.Replace` 仍重置原生历史；不绕过该约束，
也不以模拟按键、保留私有 handle 或第二套 history 实现补全。
巨型非剪贴板 Type/IME 段的同步布局阻塞也仍待上游公开契约解决；剪贴板预检和
有界短行的原生输入预算回归不代表这一输入路径已具备完整发布资格。

独立身份策略范围：单独审查刷新凭据跨启动持久化、RFC7009 令牌撤销/联邦登出及可选自动
续期，不与企业权限实现混为一项。
浏览器登出交接不保证令牌撤销、通用发行方支持或其他工作区注销。

P2：持久设置、全局预算、历史监控/拓扑、工作负载聚合与日志检索。

P3 企业权限：Gateway/Connector、租户隔离、可信策略/审批/审计与企业会话撤权；
按用户决定暂缓，排除首版 MVP，不作为首版完成条件。未来实施需要可信服务边界，
桌面提示无法约束保有直接集群凭据的其他客户端。

发布：签名安装、验证更新/恢复、完整 SBOM/漏洞审查、长稳故障注入、物理键盘/IME、
字体覆盖、辅助技术、GPU 与混合 DPI 验收。现有未签名 CI 产物不是生产 Release；
继续保留 #13 的剩余产品和发布验收目标。
