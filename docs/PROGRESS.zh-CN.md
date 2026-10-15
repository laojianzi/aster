# Aster 实施状态

## 当前基线与唯一实施范围 — 2026-10-15

**已验收 main：`57e55aac00aa9b196a2828502dd386c83dbe4a84`。**
#73 已完成 JOSE v4.1.6 安全更新及独立新主干验收，不再是待开发任务。
主干完成记录见 [#73](https://github.com/laojianzi/aster/pull/73#issuecomment-6186214495)。
#67/#68、#69/#70、#71/#72 的 Helm 渲染、元数据和内容一致性范围继续复用。

**唯一活动 PR 是 [#74](https://github.com/laojianzi/aster/pull/74)**，沿用
`renovate/mygo-native-framework`。候选已固定 MyGo v0.5.1 与必需传递依赖，
恢复 31 个终端来源文件和加固补丁、当前模型输入/确认防护以及 LTR/缩放布局。
`a7281a0069c68c102a6532feb4a918a386a2d9f3` 的五项 PR 工作流成功，
但后续补充测试和文档必须重新取得自己的精确提交证据，不能继承这一通过状态。

本次审查覆盖从 v0.3.0 到 v0.5.1 的 **179 个提交、72 类采用决策**，
其中 61 个提交位于 v0.4.8 之后。六个模块快照逐文件匹配固定上游 Git 标签，
模块/go.mod 校验和重算一致。详见 [兼容性契约](dependency-review-mygo-0.5.1.md)、
[逐项采用清单](mygo-0.3-0.5-feature-ledger.md) 和 [完整提交映射](mygo-0.3-0.5-commits.txt)。
这些数量是覆盖范围，不是 MVP 完成百分比，也不等于全部新组件已经采用。

新增选择框、八个敏感字段、OIDC 续期同意回归，要求等待中的输入不得改变
目标、信任、epoch 或已消费同意，解除忙碌后不能重放。它们加入 checked/production
双模式必跑清单，旧必跑项不删除。局部负向实验移除提交防护时，选择框和八个
敏感字段用例失败；实验后已恢复产品防护，不将故意失败算作产品通过结果。

**#74 尚未在本文中宣告合并。** 最终 PR 原始运行时/构建证据、合并后新 main
验收、实际分支结果及 #11 队列收尾仍须以发生后的仓库记录为准。
#11 人工编辑阻塞标识不是重置许可；不勾选丢弃提交的重建选项，不创建重复 PR。
#13 保持开放。正文的旧版本基线已移至 Git 历史，不用旧快照重复启动已完成工作。

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
| MyGo v0.4.8 源码/运行时审查和已有迁移保护 | #11/#58；不按旧快照重复升级 |

证据以各自 Issue/PR 为准，不用合并数或绿色 CI 百分比代替 MVP 完成度。
归档/维护 tags 保存恢复历史，不是待开发分支。已完成分支仅在精确 head、合并/主干
门槛、活动 PR、保护状态与可达性检查后退役。存在实际 bundle 才验证离线恢复；
机器人提前删除而返回 already-absent 时，不制造归档或恢复证明。

## 首版 MVP 范围与剩余优先级

**企业权威服务按 2026-10-12 决定延后至 P3，排除首个 MVP，不作为首版验收条件。**
现有凭据处理、当前身份授权、不可变审阅和本地会话清理契约继续有效。

**当前优先依赖迁移。** #73 的 JOSE v4.1.6 安全更新已完成；继续现有 #74，
完成 MyGo v0.5.1 的逐项决策、回归及精确提交验收，不另开竞争分支。

**P1 — Helm release / GitOps。** #68/#70/#72 的本地渲染、修订元数据和内容一致性摘要直接复用。
后续仍需分别定义 release 内容与生命周期检查、install/upgrade/rollback/uninstall、
GitOps repository/sync/reconcile 的可信执行、并发、失败/Unknown 与确认边界。
不得把元数据标签、渲染或普通逐对象 SSA 重命名成完整 Helm 生命周期；不得直接运行
带集群凭据的 CLI 绕过现有 Prepared、strict dry-run、精确名称确认和 no-force 保护。

**P1 — 安全编辑器事务与大输入。** MyGo v0.5.1 候选仍不满足全部组合契约；
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
