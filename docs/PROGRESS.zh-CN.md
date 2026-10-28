# Aster 实施状态

## 最近接续基线与验收入口 — 2026-10-28

**接续时已验收 main：`c178e3fbadd5f8e0688d0a4350baebd416d3dc81`，Go 1.27.3、MyGo v0.5.5。**
[#109/#110 完成记录](https://github.com/laojianzi/aster/issues/109#issuecomment-6460505239)
及 [#13 接续检查点](https://github.com/laojianzi/aster/issues/13#issuecomment-6460512541)
记录独立后台受限安装的最终 PR、实际 main、退役与完整历史恢复。
#96/#97 准入说明、#99/#100 归档核心、#101/#102 原生归档、#103/#104 安装捕获/准备、
#105/#106 独立 server dry-run、#107/#108 共享安装 WAL 和 #109/#110 后台执行均已完成，直接复用。

入场全量盘点为 42 个实际 Issues（40 closed、仅 #11/#13 open）、68 个 PR
（67 merged、#2 superseded/closed、0 open）、仅 main、45 个恢复 tags、0 releases。
**本次唯一新增范围是 [#111](https://github.com/laojianzi/aster/issues/111) /
[PR #114](https://github.com/laojianzi/aster/pull/114) / `feat/helm-install-recovery`：
v2 安装历史的新授权只读核对及原生展示。**
创建该 Issue/branch 时有 43 个实际 Issues、两个远端分支；唯一实际 PR #114 已登记。
这是入场/准入快照，实时结项以实际 Issue/PR 和 [#13 当前检查点](https://github.com/laojianzi/aster/issues/13) 为准。

已复现共享历史 loader 将合法 v2 交给 v1 Recover，导致四条混合 active/archive 历史
全部丢失的问题；现使用一次检查加载和明确版本分发，保留既有 v1 API 的类型拒绝。
安装历史只包含防御性元数据快照，不携带旧执行许可；完整性错误同步反映在只读投影，
包括被错误移入归档的未决记录。新 GET 同意独立绑定当前 Backend、连接、session、
namespace 和精确历史实例，可撤销、过期、单次消费，不要求当前选中 Helm revision。

观察只读取 namespace、完整有界 release 元数据历史、精确保留 Secret 名和原目标
ConfigMap 的元数据；前后核对 storage/history 与 namespace UID。历史缺少已接受 UID
时，同名对象只能显示 unattributed-present；最终 PATCH 的条件 RV 不能充当响应 RV。
当前事实不改变历史 outcome、WAL、未决准入或归档资格。现有显式 Reload 可初始化
私有目录与 lock，但不创建/修复 WAL、不自动联网；当前 GET 不重开 Store。
原生界面复用既有 worker/epoch/session 取消路径，当前观察与历史分开显示。
详见 [安装恢复契约](helm-install-recovery.md)。桌面 install 执行控件仍未启用。

本次冻结已有 256 个 Go 测试文件及全部旧 mandatory 名单；271 个源路径的内容/模式
独立保护，旧预算、依赖和工作流不变。最终候选与实际新 main 的四组门槛、三版本
真实 Kubernetes、普通/生产原生资格及各自原件都需核验；准确实际 PR 登记、真实
退役 bundle、独立完整离线恢复和 #13 更新完成后，才显式关闭 #111。
本文件不预先认定这些结果，也不把旧 #109/#110 原件当成本次验证。

[#11 依赖面板](https://github.com/laojianzi/aster/issues/11) 仍有 purego v0.11.2、
MyGo v0.7.1、Node v26、JSON Patch v5 四项独立待审。候选以面板实时数据为准；不批量
批准、不混入安装恢复范围，也不为清空列表关闭面板。MyGo 源码/原生库组合、purego
非 CGO 信号路径、CI Node 和 JSON Patch 模块路径迁移分别审查。

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
| existing-only 本地 journal 准入说明与完整操作容量预留 | #96/#97；只读报告不授予执行权限 |
| 终结且无未决记录的同目录有界归档及原生独立审阅/确认/浏览 | #99/#100、#101/#102；保留原字节，不 purge/export/清除 Unknown |
| 不可变受限安装捕获、纯 revision-1 codec、GET-only 目标准备 | #103/#104；不创建 release 或目标 |
| 独立一次性、冻结字节与原期限绑定的安装 server dry-run | #105/#106；服务端预检不授予持久写入权限 |
| 安装 v2 WAL、跨操作共享锁/准入/预算、惰性离线恢复 | #107/#108；日志和恢复不重建执行许可 |
| 后台独立受限安装、严格响应/独立 GET、写前容量拒绝与 Partial/Unknown | #109/#110；新 revision-1、1–32 ConfigMaps，原期限/预算与 WAL 直接复用 |
| MyGo v0.4.8/v0.5.1/v0.5.5 来源与运行时、输入/布局/时钟保护 | #58、#74、#75；历史升级不重做 |
| Go 1.27.3 固定工具链与验收 | #98；与当前安装恢复范围分开 |

证据以各自 Issue/PR 为准，不用合并数或绿色 CI 百分比代替 MVP 完成度。
归档/维护 tags 保存恢复历史，不是待开发分支。已完成分支仅在精确 head、合并/主干
门槛、活动 PR、保护状态与可达性检查后退役。存在实际 bundle 才验证离线恢复；
机器人提前删除而返回 already-absent 时，不制造归档或恢复证明。

## 首版 MVP 范围与剩余优先级

**企业权威服务按 2026-10-12 决定延后至 P3，排除首个 MVP，不作为首版验收条件。**
现有凭据处理、当前身份授权、不可变审阅和本地会话清理契约继续有效。

**当前优先产品 P1。** 完成唯一 #111 安装恢复的验证与收尾；直接复用已验收
#103/#104 捕获/codec/准备、#105/#106 服务器预检、#107/#108 v2 WAL、#109/#110
后台执行和已有原生卸载。工作期间新增的 [#112](https://github.com/laojianzi/aster/issues/112)
报告仓库内 TMPDIR 使终端加固补丁静默跳过并阻断构建，应在 #111 收尾后作为独立
P1 修复，验证真实补丁产物与内外临时目录构建一致，不混入本恢复分支。
随后新增的 [#113](https://github.com/laojianzi/aster/issues/113) 固定字号/文字缩放
是独立 P2 可读性问题，保留实际渲染与文本大小验证，不以现有控件边界测试视作完成。
之后再准入原生 install 工作流，明确新的持久写入确认、
取消/Unknown 与固定 journal 行为；不能从恢复 GET 隐式授予执行。先读取本次实际
Issue/PR 状态，不重建恢复分支或后台实现。#11 依赖候选单独审查。

**P1 — Helm release / GitOps。** #68/#70/#72 的本地渲染、修订元数据和内容一致性摘要直接复用。
已完成原生受限卸载、恢复、准入说明以及可保留原始证据的终结记录归档。
未决记录继续保留并阻止新执行；归档不能释放 Unknown/Partial，不能用新 GET 或只读
报告隐式放行。#111 之后仍有原生 install 接入、upgrade/rollback 和
GitOps repository/sync/reconcile 的可信执行、并发、失败/Unknown 与确认边界。
purge/export、未决记录处置和断电/文件系统持久性资格分别需要独立政策与验证。
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
