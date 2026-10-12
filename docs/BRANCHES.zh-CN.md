# 分支与 PR 接续台账

2026-10-12 接续基线为已验证 main
`387ee43bc4d39d657044785529203e60104e2842`，源码树
`1a99815218dc662eccc2486a771576e0bde96612`。#38/#41/#43/#45 均已完成，不重建其功能或
依赖分支。本轮唯一新增范围为 Issue #46 / PR #47 / `feat/native-schema-multipleof`。
恢复工作时先读取远端 refs、Issues、PR 与精确提交的 CI；本文件描述范围和已核验
历史，实时资格与结项收据以各 scope 的单一 PR 和 #13 为准。

## 已结项：#37 / #38

#38 复用了 `feat/native-oidc-signout`，审阅 HEAD
`0231bd9654b39d377009e580ffd2f7430304ffb9` 合并为
`1dfc816fff2653844fe74f587ee645cce823dcd4`。整合保留 #39/#40 的依赖来源、终端
补丁及看板策略，并补足未开启/遗忘续期后仍独立显示原始发行方的路径。
PR 与主干各四项验证及原始证据均通过，#37 已关闭。

[退役 38184766687](https://github.com/laojianzi/aster/actions/runs/38184766687)
报告 **applied**，完整历史 `before.bundle` 已验证；远端和本地 feature 分支均已回收，
`maintenance/completed-pr-38` 指向 `1dfc816…`。早期 waiting-for-verification
只是保护等待，不能代替这份实际删除和恢复记录。

## 已结项：#11 / #41

#41 复用了获批的 `renovate/mygo-native-framework`，没有与 #33/#39 并行升级。
最终 PR HEAD `c1c743ef61c58136cf32e1f5d905ac2ae0c6df24` 合并为接续基线
`7bf17dd…`，源码树一致。完整来源审查、五项 PR gate、五项主干 gate 和原始证据
均已完成；[0.4.5 审查](dependency-review-mygo-0.4.5.md) 的验收前描述由
#41/#13 的最终收据补全，不再把该更新当作活动 scope。

#11 在 2026-10-11T23:49:15Z 由 `renovate[bot]` 自动关闭；机器人在 23:49:17Z
删除已合并分组分支。全部五项主干门槛后的
[退役 38186078349](https://github.com/laojianzi/aster/actions/runs/38186078349)
报告 **already-absent**。该流程没有再次删除分支，也没有产生新的 `before.bundle`
或 `maintenance/completed-pr-41`。PR HEAD 是 #41 merge `7bf17dd…` 的直接第二父提交，
也是接续 main `387ee43…` 的祖先，完整实现历史仍在；不制造回收收据，也不重建分支以重新删除。

Dashboard 审批/自动关闭保持启用，automerge 保持禁用。将来真实依赖更新可重开同一
#11，那是新的依赖 scope；保留正文中的已合并 #41 不代表当前队列仍未完成。

## 已结项：#42 / #43

`feat/native-schema-constraints` 的最终审阅 HEAD
`16fa04b7446a0975f4cc1e913d91452442510e6c` 合并为 `01089da…`，源码树与已审 PR 一致。
四项最终 PR gate、四项精确 main gate 及其原始证据全部核验通过。#42 已关闭。
[退役 38196067007](https://github.com/laojianzi/aster/actions/runs/38196067007)
报告 **applied**：完整历史 bundle 及独立恢复均已验证，准确的远端/本地 feature
分支已回收，`maintenance/completed-pr-43` 指向该 merge。它与 #38 同为实际回收，
不同于 #41 的 bot 删除后 already-absent；不补造不存在的 #41 收据。

新增 #44 前全量盘点为 31 个 PR（30 merged、#2 superseded/closed、0 open），
12 个实际 Issues（仅 #13 open）、只余 main 分支、14 个恢复 tags、0 releases。
这些是已完成基线的盘点，不是新 scope 的未来状态承诺。

## 已结项：#44 / #45

`feat/reviewed-manifest-delivery` 的已审 HEAD `043819766f26def751df74f531235f65c6168afe`
合并为 `387ee43…`。四项 PR 与四项精确 main gate 各自完成 14 份新 ZIP 和六个完整
普通/生产二进制审阅；有界清单交付复用 Prepared 与既有只读 Health 入口。
[退役 38202506391](https://github.com/laojianzi/aster/actions/runs/38202506391) 报告
**applied**；完整历史 bundle 在新空 bare 仓库恢复并通过 strict fsck，原 14 个 tag
指向不变，新增 `maintenance/completed-pr-45` 指向 merge。准确 feature 在远端和
本地均已回收。自动退役发生在四项主干 workflow 成功后、人工最终审阅之前；
#44 在人工审阅及恢复核验完成后的 2026-10-12T04:17:21Z 明确关闭，不把先后顺序倒置。

新增 #46 前全量盘点为 32 个 PR（31 merged、#2 superseded/closed、0 open），
13 个实际 Issues（仅 #13 open）、只余 main 分支、15 个恢复 tags、0 releases。
#11 正文中的旧 #41 条目属于已关闭的历史快照，不代表新的待批准更新。

## 本轮唯一范围：Issue #46 / PR #47

使用 `feat/native-schema-multipleof` 实现精确 decoded-number multipleOf 提示。
复用已有 Schema transport、规范化数值、只读检查、诊断导航和原生历史合约；
不创建并行 validator、补全引擎或 mutation 路径。Pattern、实际 Helm/GitOps
生命周期及原生安全编辑器边界保留在 #13。

要求数学对照、共享算术预算/取消/隐私测试、三平台原生交互与普通/生产迁移，
以及三个 Kubernetes 版本上的实际 CRD/dry-run 与原生 E2E。PR #47 已实际建立，
既有退役登记新增准确 #47 / `feat/native-schema-multipleof` 对应关系，专属测试复用
完整 inactive-completed-merge guards 并区分旧 PR #45；Issue #46 不属于退役编号。
保留全部原有 scope guards、依赖范围第五门槛、精确 lease、完整恢复归档和历史 tags。
合并前正文各处保持中性 Issue 引用；精确 main 和实际清理核验后再完成 Issue 结项。
本文件不预先宣称新 scope 的 CI、合并或退役成功。

企业 Gateway/Connector、tenant/policy/approval/audit/session-revocation 已按用户决定
列 P3 暂缓，排除首版 MVP；不为它创建活动实现分支。#13 保留其他产品与发布验收。

## 已完成，不得重复

#40 空看板生命周期、#39 的 0.4.2 与 0.3→0.4 审计、#35/#36 增量缓冲、
#32/#34 Find、#29/#30 诊断导航、#31/#33、#27/#28、#24/#25、#18/#19 及旧工作台
能力均不重新实现。旧 #2 被不可变 Prepared/SSA 替代，保持 superseded/closed，
禁止重新合并旧 mutable Executor。历史 archive/maintenance 标签用于恢复，
不是隐藏的未完成实现分支。

## 退役条件

复用既有 `retire_completed_oidc.py`，只接受已登记的准确 PR/branch 对应关系。
退役须核对 PR 已合并至当前 main、准确 merge commit 与 branch tip、未保护未移动、
无活动 head/base PR、完整历史在 main 可达，以及该 main 精确提交最新成功的
CI、Source evidence、Native migration verification、OIDC qualification。

分支仍存在且所有检查成立时，先保存恢复 Bundle，再以精确 lease 原子删除分支并建立
`maintenance/completed-pr-<实际PR编号>`。分支已不存在则记录 already-absent，
不假称该次工作流执行过删除或建立了 bundle/tag；已有收据则保持原记录。
依赖范围另外要求 Renovate configuration 第五项主干门槛；#46 是独立 MVP Schema 功能，
不会更改或放宽已有依赖范围的门槛。旧登记、历史收据和恢复归档保持有效。
