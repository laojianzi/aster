# Aster 实施状态

## 本次接续基线与唯一实施范围 — 2026-10-20

**接续时已验收 main：`aa84d7adb3a3c4d19d58d7a39443e49b2eb51490`，MyGo v0.5.5。**
[#94/#95 完成记录](https://github.com/laojianzi/aster/issues/94#issuecomment-6289174151)
及 [#13 接续检查点](https://github.com/laojianzi/aster/issues/13#issuecomment-6289180711)
记录最终 PR 与实际 main 各自原始验收、功能分支退役及完整历史 bundle 的空仓库恢复。
原生受限卸载的准备、dry-run、独立最终确认、结果与恢复展示已完成，直接复用。
入场盘点只有 main、0 开放 PR、38 个保留标签；#11 与 #13 开放，#94 已关闭。

**本次唯一新增范围为 [#96](https://github.com/laojianzi/aster/issues/96)：只读 journal 准入说明。**
唯一 [PR #97](https://github.com/laojianzi/aster/pull/97) / `feat/helm-journal-admission` 分支。
在已有卸载审查和恢复面板中，明确点击后读取
已经初始化的私有目录与永久锁，显示首个已知阻止、canonical 记录 ID、完整容量统计
及完整 256 KiB 操作预留。缺目录或锁只说明未初始化/未检查，不创建、不修复。
使用当前 Backend 冻结绑定、namespace 与 metadata UID，直接复用实际 Begin 的同一
判定；任何合法 metadata 状态均可检查，不需要先准备另一项写操作。报告仅说明当时
本地历史，不证明 profile 有效、不访问集群、不消费或延长确认、不授予执行权限。
实际 Begin 仍在锁内复查。详见 [准入说明](helm-journal-admission.md)。

本文件描述实施范围和已验收接续基线，不预先认定本次 PR、实际 main 或分支退役通过。
最终候选和新主干的原始四组门槛、原生/真实集群证据及可恢复清理收据写入 #96 的
单一完成记录，完成 #13 检查点后才显式关闭。#13 继续承载未完成 MVP/发布目标。
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
| 独立单次卸载执行、真实条件效果、严格确认/实际缺失与保留部分/Unknown结果 | #90/#91；后端直接复用 |
| 强制私有 journal、真实进程中断离线恢复、新授权 GET 核对与未决阻止 | #92/#93；历史结果与当前事实分开 |
| 原生独立准备/dry-run/执行确认、结果/恢复展示及 worker 生命周期 | #94/#95；固定 home journal，未知历史与当前 GET 事实独立 |
| MyGo v0.4.8/v0.5.1/v0.5.5 来源与运行时、输入/布局/时钟保护 | #58、#74、#75；历史升级不重做 |

证据以各自 Issue/PR 为准，不用合并数或绿色 CI 百分比代替 MVP 完成度。
归档/维护 tags 保存恢复历史，不是待开发分支。已完成分支仅在精确 head、合并/主干
门槛、活动 PR、保护状态与可达性检查后退役。存在实际 bundle 才验证离线恢复；
机器人提前删除而返回 already-absent 时，不制造归档或恢复证明。

## 首版 MVP 范围与剩余优先级

**企业权威服务按 2026-10-12 决定延后至 P3，排除首个 MVP，不作为首版验收条件。**
现有凭据处理、当前身份授权、不可变审阅和本地会话清理契约继续有效。

**当前优先产品 P1。** 按 #13 的最新检查点推进唯一 #96 只读 journal 准入说明；直接复用
#94/#95 原生交互、#90/#91 执行器、#92/#93 journal/GET 核对和既有编码、准备、dry-run。
#11 的两个依赖候选单独审查，不混入本范围。

**P1 — Helm release / GitOps。** #68/#70/#72 的本地渲染、修订元数据和内容一致性摘要直接复用。
已完成 #94/#95 原生执行确认、恢复展示与生命周期。本次 #96 解释当前本地 journal
阻止和容量；未决记录继续保留并阻止新执行。历史保留/处置首先需要独立政策：是否允许
解除、适用记录/效果、证据要求、精确绑定、明确确认与失败行为，不能用只读报告隐式放行。
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
