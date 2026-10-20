# 分支与 PR 接续台账

## 当前接续入口 — 2026-10-19

本次基线为已验收 `main eba0ef29c21abd0b021746c764d65c9e152109eb`。
入场盘点为 main 唯一远端分支、58 个 PR（57 merged、仅 #2 closed/unmerged、0 open），
开放 Issues 为 #11 与 #13。#90/#91 已完成且分支已退役；不重复合并、重建或删除。
唯一新增范围为 [#92](https://github.com/laojianzi/aster/issues/92) 和
唯一 [PR #93](https://github.com/laojianzi/aster/pull/93) / `feat/helm-uninstall-recovery`，
沿现有执行器增加强制持久记录、进程崩溃离线恢复和
重新明确授权的 GET 核对。精确 PR 编号、最终验证/合并及退役结果以该 Issue 和
[#13](https://github.com/laojianzi/aster/issues/13) 的最新完成记录为准。
归档/维护标签保存完整历史，保留全部 36 个现有 tag；#11 两项依赖候选继续独立待审。

以下是当时的原始检查点，未勾选项和“本轮”均不代表当前活动工作。

## 历史接续基线 — 2026-10-12

2026-10-12 接续基线为已验证 main
`841745bf0b2dd0f52f994cae31a464c5dbc72a5c`，源码树
`460306069e410276ec1dda6124f077fc60f2c7fe`。#38/#41/#43/#45/#47/#49/#51/#53 均已完成，不重建其功能或
依赖分支。本轮唯一新增范围为 Issue #54 / PR #55 / `feat/native-schema-list-map`。
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

## 已结项：#46 / #47

`feat/native-schema-multipleof` 的最终 HEAD
`10ad0c1eee337d3b4fd9d20abdbb7d51d120405d` 合并为 `45f897f…`。
四项最终 PR 和四项精确主干门槛各自审阅 14 份原始 ZIP、六个完整二进制以及必跑
JSONL；旧失败候选没有替代最终结果。[退役 38215920851](https://github.com/laojianzi/aster/actions/runs/38215920851)
实际返回 **applied**，完整历史 bundle 在空 bare 库独立恢复，strict fsck 通过。
旧 15 个 tag 的 ref→SHA 未变，新增 `maintenance/completed-pr-47` 指向 merge，准确
feature 分支在远端及本地均已回收。#46 于主干人工审阅及恢复核验完成后的
2026-10-12T07:15:23Z 明确关闭，不将自动退役发生时间与人工审阅顺序倒置。

新增 #48 前全量盘点为 33 个 PR（32 merged、#2 superseded/closed、0 open），
14 个实际 Issues（仅 #13 open）、只余 main 分支、16 个恢复 tags、0 releases。

## 已结项：Issue #48 / PR #49

`feat/native-schema-pattern` 的最终 HEAD 合并为 `b1bfdf8…`。四项最终 PR 与四项新主干
gate 分别完成原始证据与六个完整二进制审阅。独立有限语言对照、方言和 Unicode、
共享预算/取消/隐私、三平台原生交互与普通/生产迁移，以及三个 Kubernetes 版本的
实际 CRD/dry-run 与原生 E2E 均已核验。

[退役 38224257797](https://github.com/laojianzi/aster/actions/runs/38224257797)
实际返回 **applied**，完整历史 bundle 在空 bare 仓库独立恢复，strict fsck 通过。
旧 16 个 tag 的 ref→SHA 未变，新增 `maintenance/completed-pr-49` 指向 merge，准确
feature 分支在远端及本地均已回收。#48 于主干人工审阅及恢复核验后的
2026-10-12T08:41:55Z 明确关闭。

新增 #50 前盘点为 34 个 PR（33 merged、#2 superseded/closed、0 open），15 个实际
Issues（仅 #13 open）、只余 main 分支、17 个恢复 tags、0 releases。

## 已结项：#50 / #51

通过 `feat/native-schema-format` 实现有界 `date` / `byte` / `uuid` 提示，保留既有传输、
只读诊断、原始源码导航和原生历史合约。独立公历/编码/语法对照、错误类型和 nullable
适用性、共享预算、三平台原生编辑/最小窗口/双模式迁移及三版本真实 dry-run 为新增门槛。
最终 HEAD `0c10a56180658062352886fc63c6bf7336ec900c` 合并为当前接续 main `2186a23…`。
四项最终 PR、四项精确 main 门槛及原始证据均已独立核验。
[退役 38231769807](https://github.com/laojianzi/aster/actions/runs/38231769807) 返回 **applied**，
完整历史 bundle 在新空 bare 仓库独立恢复并通过 strict fsck；旧 17 个 tag 不变，新增
`maintenance/completed-pr-51` 指向 merge，准确 feature 分支在远端及本地均已回收。
#50 于 2026-10-12T09:59:21Z 在独立主干与恢复核验后明确关闭。

新增 #52 前全页盘点为 35 个 PR（34 merged、#2 superseded/closed、0 open），16 个实际
Issues（仅 #13 open）、只余 main 分支、18 个恢复 tags。

## 已结项：Issue #52 / PR #53

使用 `feat/native-schema-structured-enum` 实现有界 decoded JSON 对象/数组枚举相等，
保留纯 scalar 路径、精确数值、共享预算、完整元数据检查与所有旧门槛。新增独立 JSON
对照、原生父字段导航/选择/history/最小窗口、三平台双模式迁移及三版本真实 CRD。
正确区分服务端嵌套数字类型、nullable enum、裁剪/默认值与本地只读辅助的语义边界。
唯一 [PR #53](https://github.com/laojianzi/aster/pull/53) 已实际创建并登记其准确编号；#52 是
Issue，不能当作退役 PR 编号。专属回归复用完整 inactive-completed-merge guards，并区分
旧 PR #51 与 Issue #52，不创建重复功能分支。
最终 HEAD `60f412b6…` 合并为 main `841745bf…`。四项最终 PR 和四项精确主干验证、
原始证据、完整二进制和图像均独立核验。原失败/重试来源保留，没有复用 PR 产物验收主干。
[退役 38247964969](https://github.com/laojianzi/aster/actions/runs/38247964969) 实际 applied；
完整 bundle 在新空 bare 仓库恢复，15 项 Git 检查含 strict fsck 通过；原 18 个 tag
映射未变，新增 `maintenance/completed-pr-53`。自动退役先于独立产物审查，恢复、本地
清理及 2026-10-12T12:32:07Z 的 #52 completed 关闭在其后，时间顺序不倒置。

新增 #54 前全页盘点为 36 个 PR（35 merged、#2 superseded/closed、0 open），17 个实际
Issues（仅 #13 open）、仅 main 分支、19 个恢复 tags、0 releases。没有并发待续接范围。

## 已结项：Issue #54 / PR #55

`feat/native-schema-list-map` 已交付有界 map-list 字符串键 tuple 重复提示；
[PR #55](https://github.com/laojianzi/aster/pull/55) 合并至
`9f7010fdfe23513985c22fc1a8feb8e9ed24f6f7`。PR 与实际 main 各自完成独立原始产物审查。
[退役 38259896063](https://github.com/laojianzi/aster/actions/runs/38259896063) 实际 applied，
完整 bundle 在新空 bare 仓库恢复、15 项 Git 检查通过，分支已删除；
`maintenance/completed-pr-55` 指向该 main。自动退役早于独立审查，恢复核验与本地清理在
审查后完成，#54 于 2026-10-12T14:26:47Z 明确 completed。旧 tags 和范围保护保持有效。

## 本轮唯一范围：Issue #56 / PR #57

2026-10-13 新增 #56 前，完整盘点为 37 个 PR（36 merged、#2 superseded/closed、0 open）、
18 个实际 Issues（仅 #13 open）、仅 main、20 个 archive/maintenance tags、0 releases。
没有可复用的开放实现分支。唯一 `feat/native-schema-list-set` 从上述已验收 main 起步，
只推进显式、非 nullable string set 的有界重复提示与真实 dry-run 边界。

当前依赖仍锁定 MyGo 0.4.5；官方 0.4.8 已增加原生 editor 事务与补全入口，可独立评估，
但直接迁移尚未满足程序加载的原始字节/混合换行契约，补全/IME 交叉行为也待实测。
详见 [0.4.8 上游源码审查](editor-upstream-mygo-0.4.8-review.md)。安全补全仍未完成；
实际 Helm/GitOps、其他列表语义和发布资格仍在 #13。

保留全部原 scope guards、依赖范围第五门槛、精确 lease、完整恢复归档与历史 tags。
新退役登记已绑定实际 [PR #57](https://github.com/laojianzi/aster/pull/57) 与准确 branch，
回归完整核对 PR 57 / Issue 56 / 旧 PR 55，不把 Issue 当成 PR。合并前保持中性 Issue 引用；独立精确 main、实际退役和恢复、本地清理核验后再完成 #56。
本节不预先声称本轮实现、CI、合并或退役成功。

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
依赖范围另外要求 Renovate configuration 第五项主干门槛；#56 是独立 MVP Schema 功能，
不会更改或放宽已有依赖范围的门槛。旧登记、历史收据和恢复归档保持有效。
