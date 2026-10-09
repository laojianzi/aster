# 分支与 PR 接续台账

2026-10-09 恢复开发时已读取全部十三个远端分支和所有 PR，并比较未合并分支差异。
本台账记录接续决策，不替代 GitHub 当前 refs。每次恢复必须重新读取 refs 和 CI，不能照抄旧 SHA。

| 分支 | PR / 接续决策 |
| --- | --- |
| main | 已合并 #1、#3–#10；#2 不合并。#10 cf5c8ff；#9 8d958719 |
| feat/bootstrap-production-core | #1 已合并，历史分支，不继续重复开发 |
| feat/resource-operations | #2 已关闭且未合并；旧可变 Executor 已由 #3 代替，不重新引入 |
| feat/native-workbench-verified | #3 已合并；原生工作台、日志/转发/命令等在主干继续 |
| fix/no-redirect-or-mutation-replay | #4 已合并，凭据重定向与变更重试保护不得回退 |
| feat/native-resource-relationships | #5 已合并，关系导航不重新实现 |
| feat/native-resource-metrics | #6 已合并，当前指标范围见 metrics 文档 |
| feat/native-multi-workspaces | #7 已合并，独立工作区和双集群隔离不重新实现 |
| feat/native-reviewed-apply | #8 已验证并合并，existing-resource SSA 不是批量 upsert |
| feat/native-terminal | #10 已按最终 CI #74 / main #75 验证合并；不再重复实现终端 |
| build/development-kit | 历史环境辅助分支，独有变化只有 development-kit 工作流；已由主干 offline-devkit/source-evidence 流程接续，不作为缺失产品功能合并 |
| chore/development-snapshot | 历史环境辅助分支，独有变化只有 development-environment 工作流；不再创建重复快照 PR |
| renovate/configure | #9 已完成受控依赖更新配置并合并；禁用自动合并，MyGo 耦合补丁手工升级 |
| feat/bounded-exec-authentication | 本轮在已合并 #9/#10 主干上唯一新建的产品分支：受控连接时认证；其他分支没有此实现 |

本轮没有删除历史分支、强制重写历史或恢复 #2 的旧执行路径。台账中的合并状态是本轮恢复快照。

## 合并门槛

限定功能范围完成后，核对最终 head、CI 实际 checkout、源码 tree、生成源码与产物哈希，
逐一验证必需用例；检查评审线程，使用 expected head SHA 合并。合并完成不等于发布正式产品。
主干的合并后 CI 也要检查。未满足门槛的 PR 保持 Draft，不删除或强制覆盖独有历史分支。

## 当前优先级

P1：受控 exec 认证接入原生连接，完成真实集群和三平台进程回收验证。
随后：凭据库与 OIDC/短期凭据轮换；Schema 编辑器/Helm/GitOps；企业 Gateway/Connector、
策略/审批/审计/撤权；签名发行与恢复、长稳/故障注入和真实桌面资格验证。
同一功能只维护一个工作分支和一个开放 PR，后续模块在已验证主干上增量开发。
