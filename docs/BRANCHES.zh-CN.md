# 分支与 PR 接续台账

更新：2026-10-10。本文件是工作快照；恢复时必须重新读取远端 refs、Issues、PR、CI。
已验证主干基线：`2ab77a49cad0a7b0143dce5b147bacb7a8309fc5`。

## 唯一活动范围

`feat/native-oidc-login` / Issue #24 / PR #25：显式系统浏览器 OIDC + PKCE 登录和
二次确认后的 Kubernetes 连接。沿用中断前实现，不另开 OIDC 分支或 PR。
最终审查移除临时启动探针，保留固定 digest 的独立 Dex/Kubernetes 资格测试，
补齐 JWK key_ops 和成功 Token 响应结构的拒绝回归。最终验证以 #25 的精确提交为准。

恢复时远端仅有 main 和上述分支；开放工作项是 #11、#13、#24 和 PR #25。
#11 为持续依赖看板，#13 为未完成产品总目标，不应因为单项功能完成而关闭。

## 已完成，不得重复实现

#1、#3–#10、#12、#15、#16、#17、#19、#20、#22、#23 已合并。
#2 已关闭并被 Prepared/SSA 路径替代，不能恢复其旧可变 Executor。
#14 Schema、#18 OS Token 凭据库、#21 MyGo 迁移审查均已完成并关闭。
MyGo 0.3.7 已包含在主干，不能重开 0.3.6/0.3.7 升级分支。

## 已执行的历史清理

用户授权的工作流 `38031414643` 已删除十七条历史分支。
四个 `archive/2026-10-10/...` 标签保存未整合/被替代历史，完整 Git bundle 已验证。
收据 `maintenance/branch-cleanup-2026-10-10` 指向 main `2ab77a49`。
不要恢复旧开发快照工作流，亦不要把归档代码当作遗漏产品功能合并。

## 当前分支完成后的清理

#25 必须先通过精确 HEAD 的 CI、Source evidence、Native migration verification、
OIDC qualification，再按 expected head 合并并核验 main 四类工作流。
已有 `retire-completed-oidc` 工作流只删除 PR25 的精确、已合并可达、未保护、
无活动 PR 的分支；原子 push + exact lease 防止误删移动后的引用，保存 Git bundle 和
`maintenance/completed-pr-25` 收据。未知或新建分支不在清理范围内。

清理结果以实际工作流报告和远端 refs 为准，未执行前不能将计划描述为已完成。
后续 P1 的身份续期/轮换、编辑交付与企业服务统一由 #13 分配实际范围，
只在开始实现且确认无重复时创建子任务，不能建立空的未来功能分支。
