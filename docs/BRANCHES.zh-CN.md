# 分支与 PR 接续台账

2026-10-09 恢复开发时已读取全部十二个远端分支和所有 PR，并比较未合并分支差异。
本台账记录接续决策，不替代 GitHub 当前 refs。每次恢复必须重新读取 refs 和 CI，不能照抄旧 SHA。

| 分支 | PR / 接续决策 |
| --- | --- |
| main | 已合并 #1、#3、#4、#5、#6、#7、#8；#8 合并提交 ed8a5b594364be2ee372f9f87d3b561d4274dae7 |
| feat/bootstrap-production-core | #1 已合并，历史分支，不继续重复开发 |
| feat/resource-operations | #2 已关闭且未合并；旧可变 Executor 已由 #3 代替，不重新引入 |
| feat/native-workbench-verified | #3 已合并；原生工作台、日志/转发/命令等在主干继续 |
| fix/no-redirect-or-mutation-replay | #4 已合并，凭据重定向与变更重试保护不得回退 |
| feat/native-resource-relationships | #5 已合并，关系导航不重新实现 |
| feat/native-resource-metrics | #6 已合并，当前指标范围见 metrics 文档 |
| feat/native-multi-workspaces | #7 已合并，独立工作区和双集群隔离不重新实现 |
| feat/native-reviewed-apply | #8 已验证并合并，existing-resource SSA 不是批量 upsert |
| feat/native-terminal | 唯一继续实现的产品分支；沿用原 8b5956b 依赖准备提交，并纳入已合并 #8 的最终测试修正，不另建重复终端分支 |
| build/development-kit | 历史环境辅助分支，独有变化只有 development-kit 工作流；已由主干 offline-devkit/source-evidence 流程接续，不作为缺失产品功能合并 |
| chore/development-snapshot | 历史环境辅助分支，独有变化只有 development-environment 工作流；不再创建重复快照 PR |

## 合并门槛

限定功能范围完成后，核对最终 head、CI 实际 checkout、源码 tree、生成源码与产物哈希，
逐一验证必需用例；检查评审线程，使用 expected head SHA 合并。合并完成不等于发布正式产品。
主干的合并后 CI 也要检查。未满足门槛的 PR 保持 Draft，不删除或强制覆盖独有历史分支。

## 当前优先级

P1：完成原生交互式终端的安全边界与真实集群/三平台验证。
随后：凭据库与 OIDC/认证 helper；Schema 编辑器/Helm/GitOps；企业 Gateway/Connector、
策略/审批/审计/撤权；签名发行与恢复、长稳/故障注入和真实桌面资格验证。
同一功能只维护一个工作分支和一个开放 PR，后续模块在已验证主干上增量开发。
