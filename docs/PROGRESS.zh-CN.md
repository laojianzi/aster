# Aster 实施状态

## 当前基线与唯一实施范围

2026-10-15 接续基线：已验收 main `a8422d80af4e434558639d1893d1406f60f8c922`，
源码树 `05f0e63e1a330d4afff2845907582ad472345971`，Go 1.27.2 / MyGo v0.4.8。
#67/#68 本地渲染、#69/#70 修订元数据、#71/#72 单次同意后的选定内容一致性摘要均已完成。
#71 在新 main 独立验收和完整历史恢复后，于 2026-10-15T03:51:41Z 显式 completed；
[原始结项记录](https://github.com/laojianzi/aster/issues/71#issuecomment-6183982696)保留。
不得重建这些已完成分支或重复其 PR。

**当前唯一实施 PR：[安全依赖更新 #73](https://github.com/laojianzi/aster/pull/73)，
`renovate/github.com-go-jose-go-jose-v4-4.x`：go-jose/v4 v4.1.6。**
[#11](https://github.com/laojianzi/aster/issues/11) 是真实重新开放的依赖队列，不是旧升级快照。
本次仅批准既有 JOSE 条目，由 Renovate 创建 #73；不新开竞争分支，不批准全部更新。
MyGo v0.5.1 仍单独待审，当前 pin 保持 v0.4.8，不将其混入安全修复。

机器人初始提交仅改 go.mod，OIDC 在缺失 go.sum 阶段失败。接续提交补已核验校验和，
明确 RS256 的 RSA 范围收窄为 2048–4096 位、前置编码预算及登录/续期边界回归，
不覆盖上游进程级安全上限。既有私钥字段拒绝、公钥投影、JWT 头白名单和私有错误保留。
[依赖审查](dependency-review-go-jose-4.1.6.md)记录来源、兼容性与真实验收边界。

本文件不预先宣称 #73 最终 CI、合并、新主干或退役已通过；这些结果以 #73、#11 和
[#13](https://github.com/laojianzi/aster/issues/13) 为准。旧 #72 的绿色 CI 不验证新依赖图。
#11 在仍有待审更新时保持开放；每个实际范围仅一条分支及一个 PR。

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
门槛、活动 PR、保护状态与可达性检查后退役，并验证完整历史恢复。

## 首版 MVP 范围与剩余优先级

**企业权威服务按 2026-10-12 决定延后至 P3，排除首个 MVP，不作为首版验收条件。**
现有凭据处理、当前身份授权、不可变审阅和本地会话清理契约继续有效。

**优先安全依赖。** #73 补 go-jose v4.1.6，保留来源、五项 PR/main 门槛与精确分支保护。
MyGo v0.5.1 是之后独立的 module/native 来源与运行时兼容性审查，不预先声称解决编辑器契约。

**P1 — Helm release / GitOps。** #68/#70/#72 的本地渲染、修订元数据和内容一致性摘要直接复用。
后续仍需分别定义 release 内容与生命周期检查、install/upgrade/rollback/uninstall、
GitOps repository/sync/reconcile 的可信执行、并发、失败/Unknown 与确认边界。
不得把元数据标签、渲染或普通逐对象 SSA 重命名成完整 Helm 生命周期；不得直接运行
带集群凭据的 CLI 绕过现有 Prepared、strict dry-run、精确名称确认和 no-force 保护。

**P1 — 安全编辑器事务与大输入。** 现用 MyGo v0.4.8；升级完成不等于满足所有编辑契约。
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
