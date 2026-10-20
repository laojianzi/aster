# Aster 实施状态

## 本次接续基线与唯一实施范围 — 2026-10-19

**接续时已验收 main：`eba0ef29c21abd0b021746c764d65c9e152109eb`，MyGo v0.5.5。**
[#90/#91 完成记录](https://github.com/laojianzi/aster/issues/90#issuecomment-6264952696)
及 [#13 接续检查点](https://github.com/laojianzi/aster/issues/13#issuecomment-6265021247)
记录实际主干验收和完整分支恢复。#90 已完成受限、独立单次授权的保留历史卸载执行器，
其分支已退役；不重新实施。接续盘点只有 main、0 开放 PR；#11 与 #13 开放。

**本次唯一新增范围为 [#92](https://github.com/laojianzi/aster/issues/92)：持久执行记录、进程中断离线恢复与重新授权的只读核对。**
在现有执行器内强制先保存和同步每项意图，再派发条件请求；响应、独立完成观察和
终结记录分别落盘，失败立即停止后继效果。恢复记录不含旧 session、凭据或授权。
新的 GET-only 单次确认使用当前连接、当前选择和新 session，只返回当前对象事实，
不改写历史 Unknown、不恢复写入、不清除未决记录的阻止条件。三平台原生文件/锁/进程
测试与三版本 Kubernetes 的实际恢复观察加入原门槛。详见 [恢复契约](helm-uninstall-recovery.md)。
桌面执行与恢复展示仍需原生确认/生命周期的独立验收。

本文件记录范围与接续基线；最终唯一 PR、新合并主干的原始证据及实际清理结果以 #92
的单一完成记录为准，验收后才显式关闭该 Issue。#13 继续承载未完成 MVP/发布目标。
[#11 依赖面板](https://github.com/laojianzi/aster/issues/11) 当前有 purego v0.11.2 和
JSON Patch v5.9.11 两项待审，保留审批队列；不批准全部、不重复已有升级分支，也不为
清空队列关闭面板。purego 的非 CGO 信号处理路径及 JSON Patch 模块路径迁移分别审查。

## 已完成、必须复用的能力

| 能力 | 已完成工作 |
| --- | --- |
| 原生资源/CRD、日志、转发、命令、TTY 与不可变审阅变更 | #1、#3、#4、#8、#10；#2 已被替代，不恢复可变 Executor |
| 资源关系、Metrics、独立工作区和双集群隔离 | #5、#6、#7 |
| exec 身份认证、OS 凭据库、OIDC/PKCE 与显式内存续期/发行方登出 | #12、#18/#19、#24/#25、#26、#27/#28、#37/#38 |
| TextBuffer、原始字节/快照、Unicode Find、位置导航与粘贴保护 | #29/#30、#32/#34、#35/#36、#63/#64 |
| 有界 Schema 结构/标量、multipleOf、可移植 pattern、date/byte/uuid 与结构化 enum | #42/#43、#46/#47、#48/#49、#50/#51、#52/#53 |
| 有界字符串/int64/bool map-list 与 set-list 身份提示 | #54/#55、#56/#57、#59/#60、#61/#62、#65/#66 |
| 已渲染清单的有序导入、逐对象不可变审阅、确认和部分结果核对 | #44/#45 |
| 可信本地 chart/values 快照、固定官方 Helm 4.3.0、渲染捕获/丢弃/接受 | #67/#68；不是 Helm release 写入或 GitOps |
| Helm Secret-driver 元数据协商、单命名空间分页与显式刷新/取消 | #69/#70；不是 release 内容或生命周期写入 |
| 单次内容同意、精确 Secret GET、UID/resourceVersion 与内容一致性只读摘要 | #71/#72；不认证真实性、不提供生命周期写入 |
| 不可变卸载范围、最新修订与实时对象观察、受限卸载/精确缺失契约 | #76/#77、#78/#79、#80/#81/#82；不重复只读清单 |
| 保留原始载荷的条件状态编码、完整受限目标准备、独立服务器 dry-run | #84/#85、#86/#87、#88/#89；读/预检同意不授予持久执行权限 |
| 独立单次卸载执行、真实条件效果、严格确认/实际缺失与保留部分/Unknown结果 | #90/#91；桌面执行仍禁用 |
| MyGo v0.4.8/v0.5.1/v0.5.5 来源与运行时、输入/布局/时钟保护 | #58、#74、#75；历史升级不重做 |

证据以各自 Issue/PR 为准，不用合并数或绿色 CI 百分比代替 MVP 完成度。
归档/维护 tags 保存恢复历史，不是待开发分支。已完成分支仅在精确 head、合并/主干
门槛、活动 PR、保护状态与可达性检查后退役。存在实际 bundle 才验证离线恢复；
机器人提前删除而返回 already-absent 时，不制造归档或恢复证明。

## 首版 MVP 范围与剩余优先级

**企业权威服务按 2026-10-12 决定延后至 P3，排除首个 MVP，不作为首版验收条件。**
现有凭据处理、当前身份授权、不可变审阅和本地会话清理契约继续有效。

**当前优先产品 P1。** 按 #13 的最新检查点推进唯一 #92 恢复与新授权只读观察；直接复用
#90/#91 执行器和既有清理、编码、准备、dry-run。#11 的两个依赖候选单独审查，不混入本范围。

**P1 — Helm release / GitOps。** #68/#70/#72 的本地渲染、修订元数据和内容一致性摘要直接复用。
当前 #92 补齐进程崩溃历史及独立 GET 观察；之后仍需原生执行确认、恢复展示、
生命周期和明确解决未决状态政策的实际证据；
其他后续范围为 install/upgrade/rollback 和
GitOps repository/sync/reconcile 的可信执行、并发、失败/Unknown 与确认边界。
不得把元数据标签、渲染或普通逐对象 SSA 重命名成完整 Helm 生命周期；不得直接运行
带集群凭据的 CLI 绕过现有 Prepared、strict dry-run、精确名称确认和 no-force 保护。

**P1 — 安全编辑器事务与大输入。** 已验收 MyGo v0.5.5 不代表全部组合契约已满足；
版本升级不等于完成原始换行、单一历史、IME 和任意大输入的验收。
安全补全仍需公开且实测的 raw 混合 CRLF/LF、单一 Undo/Redo、selection/composition、
文档身份和过期结果组合行为。任意巨型非剪贴板 Type/IME 的同步布局预算、跨卸载历史
仍开放；不使用私有 handle、模拟生产按键或第二套 Undo 栈绕过。
见 [上游编辑器审查](editor-upstream-mygo-0.4.8-review.md)、[buffer](manifest-buffer.md)
及 [clipboard](manifest-clipboard.md)。

**P1 — 剩余 Schema。** 复用已完成的标量/枚举/集合有界子集。更广泛的 format/pattern、
组合、ordinary uniqueItems、nullable/default-dependent 身份、完整列表/SSA、CEL 与准入
仍按实际 CRD/服务器语义、预算和隐私单独验收；不把本地提示替代真实 server dry-run。
见 [Schema 契约](schema-assistance.md)。

**独立身份策略。** 跨启动刷新凭据持久化、RFC7009 撤销/联合登出及可选自动续期需要
独立政策和失败行为审查。已有 memory-only renewal / browser handoff 不代表这些完成。

**P2 — 产品深度。** 持久设置、统一超时/读取预算、历史/监控/拓扑、工作负载聚合与日志
检索。复用现有 relationships、Metrics、workspace cancellation 和 bounded logs。

**发布验收。** 签名安装、可信升级/恢复、完整 SBOM/漏洞审查、长稳和故障注入、物理
键盘/IME、字体、无障碍、GPU 与混合 DPI 仍开放。已有未签名 CI 应用不是生产 Release。
只读 HTTP/native 测试和 runner 截图不建立这些发布资格。

**P3 延后。** Gateway/Connector、租户隔离、可信策略/审批/审计和企业会话撤权，需要
将来的可信服务边界；持有直接集群凭据的其他客户端不受桌面提示强制约束。

## 历史检查点

旧进度正文原样保存为 [截至 PR #68 的历史检查点](progress-checkpoints-through-pr68.md)。
其中“本轮”“当前 pin”“后续候选”和未勾选项描述当时状态，不能据此重建已完成任务。
从 Git 历史和对应 Issue/PR 追溯原始验收、失败重试与退役时序；不要倒置自动退役和
后来独立审查的顺序。新的实施/验收结果继续写入当前实际 Issue，而非重复堆叠旧快照。
