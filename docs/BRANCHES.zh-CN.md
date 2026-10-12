# 分支与 PR 接续台账

2026-10-12 接续基线为已验证 main
`7bf17ddbfaf87fa8ecb720b1e5e95e913823345d`。#38/#41 均已完成，不重建其功能或
依赖分支。本轮唯一新增范围为 #42 / `feat/native-schema-constraints`。
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
或 `maintenance/completed-pr-41`。PR HEAD 是接续 main 的直接第二父提交和祖先，
完整实现历史仍在；不制造回收收据，也不重建分支以重新删除。

Dashboard 审批/自动关闭保持启用，automerge 保持禁用。将来真实依赖更新可重开同一
#11，那是新的依赖 scope；保留正文中的已合并 #41 不代表当前队列仍未完成。

## 本轮唯一范围：#42

使用 `feat/native-schema-constraints` 和一个对应 PR，扩展当前 Check draft 的有界
scalar enum、数值边界、Unicode 字符串长度与容器大小提示。复用 #14/#15 的身份和
传输约束、#29/#30 的源码导航及 #35/#36 的增量缓冲；不新增刷新入口、补全事务、
依赖升级或 Prepared/变更路径。

本范围必须验证精确数字/不完整检查/预算/隐私、三平台原生历史与选择、普通/生产
迁移及三版本真实 CRD admission 对照。注册退役时必须采用该唯一 PR 的实际编号与
准确 branch，不能把 Issue #42 当作 PR 编号，也不能预猜下一个编号。
精确 HEAD 的结果、合并提交和实际退役结果记录在该 PR、#42 和 #13，不能将本段
实现范围或计划视为未来 CI 已通过。完成该子范围后，#13 的其他验收仍保持开放。

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
依赖范围另外要求 Renovate configuration 第五项主干门槛；#42 是独立 Schema 功能，
不会更改或放宽已有依赖范围的门槛。旧登记、历史收据和恢复归档保持有效。
