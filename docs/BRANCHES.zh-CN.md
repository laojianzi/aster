# 分支与 PR 接续台账

2026-10-09 依赖更新恢复基线：main `4a627bf9f89d6c8025cd809e87c843d223add053`，
开放 PR 为空。重新核查了全部十四个已有远端分支；下表是接续决策，不替代当前 GitHub refs。
本轮用户明确授权处理依赖看板并升级 MyGo；唯一新增工作分支为 `deps/mygo-latest-verified`。

| 分支 | PR / 接续决策 |
| --- | --- |
| main | #1、#3–#10、#12、#15 已合并；主干 CI #86 已验证 |
| feat/bootstrap-production-core | #1 已合并，历史分支 |
| feat/resource-operations | #2 已关闭未合并；旧可变 Executor 已被 Prepared/SSA 替代，不重新引入 |
| feat/native-workbench-verified | #3 已合并，原生资源、日志、转发、命令和安全操作已在主干 |
| fix/no-redirect-or-mutation-replay | #4 已合并，不回退重定向/变更重放保护 |
| feat/native-resource-relationships | #5 已合并，不重复实现关系导航 |
| feat/native-resource-metrics | #6 已合并，不重复实现当前 Pod/Node 指标 |
| feat/native-multi-workspaces | #7 已合并，不重复实现独立工作区和双集群隔离 |
| feat/native-reviewed-apply | #8 已合并，范围是已有对象无 force SSA，不是批量 upsert |
| feat/native-terminal | #10 已合并；依赖更新只迁移上游 API/加固补丁，不重写功能 |
| feat/bounded-exec-authentication | #12 已合并，受控连接时认证/到期清理已完成 |
| feat/native-schema-assistance | #14/#15 已完成并合并，不重复创建 Schema 辅助任务 |
| build/development-kit | 历史环境辅助分支，独有内容只有旧 development-kit 工作流；不当作产品缺失合并 |
| chore/development-snapshot | 历史环境辅助分支，独有内容只有旧快照工作流；不创建重复 PR |
| deps/mygo-latest-verified | 当前唯一依赖升级分支，对应 #11；MyGo、终端补丁、Go、Kubernetes 和 Actions 一起验证 |

Renovate 配置 #9 已合并，自动合并关闭，依赖看板批准机制保持。
本轮不同时勾选批量批准，避免为已经手工处理的版本生成重复 PR；#11 是持续看板，不关闭。
未删除历史分支、强制重写引用或合并已替代的 #2。

## 合并门槛

限定范围完成后，核对最终 head、CI 实际 checkout、源码 tree、生成源码和原生库哈希，
验证必需用例和评审线程。用 expected head SHA 合并，继续检查 main CI。
同一范围只有一个工作分支/PR；不能使用上一提交的绿色结果为新提交背书。

## 接续优先级

依赖任务完成后按照 #13：凭据库/OIDC/身份续期与撤权；原生完整编辑和 Helm/GitOps；
企业 Gateway/Connector/可信策略与审计；持久化和应用总预算；签名更新与生产资格验收。
完成的历史测试数字和源码身份保存在对应 PR/Issue，不用旧报告代替新代码验证。

## 2026-10-09 凭据库恢复

#16（MyGo/latest 依赖批次）与 #17（原 Renovate x/sys 更新）已经合并；#11 无待处理更新。
唯一接续分支 `feat/native-credential-vault` 对应 #18，基于 main `e1cbd416`，
原来只包含 offline-devkit 刷新；本轮补充产品实现。没有重复分支、恢复旧 Executor 或删除历史。
完成范围按最终 expected HEAD 和真实 OS/集群/源码证据合并；其后复核 main CI。
