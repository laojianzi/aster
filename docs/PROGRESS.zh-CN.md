# Aster 实施状态

## 最近接续基线与验收入口 — 2026-10-28

**接续时已验收 main：`b8c69749c60417df877b277ee83713e5684694a2`，Go 1.27.3、MyGo v0.5.5。**
[#107/#108 完成记录](https://github.com/laojianzi/aster/issues/107#issuecomment-6433363807)
及 [#13 接续检查点](https://github.com/laojianzi/aster/issues/13#issuecomment-6433372970)
记录安装 v2 WAL 的最终 PR、实际 main、退役与完整历史恢复。
#96/#97 准入说明、#99/#100 归档核心、#101/#102 原生归档、#103/#104 安装捕获/准备、
#105/#106 独立 server dry-run 和 #107/#108 共享安装 WAL 均已完成，直接复用。

**本次只接续已有 [#109](https://github.com/laojianzi/aster/issues/109) /
[PR #110](https://github.com/laojianzi/aster/pull/110) / `feat/helm-install-execution`。**
入场全量盘点为 42 个实际 Issues（#11、#13、#109 开放）、68 个 PR
（66 merged、#2 superseded/closed、#110 draft/open）、main 与唯一 feature 两个分支、
44 个恢复 tags、0 releases。这是接续快照，不是未来远端状态承诺；实时结项状态以
上述 Issue/PR 与 [#13 当前检查点](https://github.com/laojianzi/aster/issues/13) 为准。

该增量增加后台受限安装：原始 Backend/session/candidate/preflight 绑定的独立、
可撤销、过期、单次写许可；原字节与原期限；同一 Store/锁/准入及 v2 WAL；每次可能
生效的写入前先同步精确意图。仅创建 revision-1 pending Secret、按序创建 1–32 个
ConfigMaps，再用实际观察到的 UID/resourceVersion 条件更新 deployed。严格响应、
独立 GET 与 journal 完成分别记录；冲突、失联、取消或日志故障保留 Partial/Unknown，
不重试、adopt、SSA/force、回滚、重放或清除未决。详见 [执行契约](helm-install-execution.md)。
写前纯本地容量检查拒绝可证明无法完成的限速、重复敏感响应和最终条件 patch 大小；
默认 20 QPS/burst 40 的 32 目标在原 10 秒期限内不可能完成，现返回 NotStarted 且
无执行请求/新 WAL。所有原预算保持不变；通过该检查也不是完成保证。
桌面 install 控件仍未启用，不能把后台执行等同于完整原生 Helm 生命周期。

最终候选和实际新 main 的四组门槛、每个 Kubernetes 版本的五个安装测试名、原始
证据、可恢复退役和完整离线恢复都需独立核验；合并本身不代表完成。退役登记仅绑定
实际 PR #110 和准确分支，不使用 Issue #109 或旧 PR #108。验收收据写入 #109，
更新 #13 后才显式完成；本文件不预先认定这些结果。

[#11 依赖面板](https://github.com/laojianzi/aster/issues/11) 在本次盘点有 purego v0.11.2、
MyGo v0.7.1、Node v26、JSON Patch v5 四项独立待审。候选以面板实时数据为准；不批量
批准、不混入安装执行范围，也不为清空列表关闭面板。MyGo 源码/原生库组合、purego
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
| MyGo v0.4.8/v0.5.1/v0.5.5 来源与运行时、输入/布局/时钟保护 | #58、#74、#75；历史升级不重做 |
| Go 1.27.3 固定工具链与验收 | #98；与当前安装执行范围分开 |

证据以各自 Issue/PR 为准，不用合并数或绿色 CI 百分比代替 MVP 完成度。
归档/维护 tags 保存恢复历史，不是待开发分支。已完成分支仅在精确 head、合并/主干
门槛、活动 PR、保护状态与可达性检查后退役。存在实际 bundle 才验证离线恢复；
机器人提前删除而返回 already-absent 时，不制造归档或恢复证明。

## 首版 MVP 范围与剩余优先级

**企业权威服务按 2026-10-12 决定延后至 P3，排除首个 MVP，不作为首版验收条件。**
现有凭据处理、当前身份授权、不可变审阅和本地会话清理契约继续有效。

**当前优先产品 P1。** 完成唯一 #109/#110 后台受限安装执行的验证与收尾；直接复用
#103/#104 捕获/codec/准备、#105/#106 服务器预检、#107/#108 v2 WAL 和已有原生卸载。
建议下一项先补 v2 安装历史的新授权只读核对，再准入原生 install 工作流；没有历史
UID 的同名对象只能报告存在且无法归属，不能认作成功、adopt 或解除 Unknown。
准入前先读取 #109/#110 实际状态，不重建后台实现。#11 依赖候选单独审查。

**P1 — Helm release / GitOps。** #68/#70/#72 的本地渲染、修订元数据和内容一致性摘要直接复用。
已完成原生受限卸载、恢复、准入说明以及可保留原始证据的终结记录归档。
未决记录继续保留并阻止新执行；归档不能释放 Unknown/Partial，不能用新 GET 或只读
报告隐式放行。#109/#110 之后仍有原生 install 接入、upgrade/rollback 和
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
