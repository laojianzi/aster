# Aster 实施状态与恢复记录

本文件用于中断后恢复工作，不是生产验收证书。状态必须同时核对代码、PR 和对应提交的 CI，
不能把上一轮绿色结果套用到新提交，也不能把规划中的能力算作已实现。

## PR 收敛

- PR #1：已进入主分支的初始原生工作台基础。
- PR #2：已关闭、未合并；被 PR #3 的不可变 Prepared 操作模型替代。
  旧的可变 Executor/Plan 路径不可重新引入。其 SSA/upsert 设想并未因此完成。
- PR #3：`feat/native-workbench-verified`，继续承载原生工作流与验证。
  本轮恢复基线为 `e42c9ca944fe273345e03007f1908a17d8fee42c`。
  恢复时 CI run `37756182885` 已成功；对应实际测试 merge SHA 为
  `af393780e044bc938802fbec5ea7dd74cf8aef2c`。该历史结果不证明后续提交通过。

## 已有可执行能力

原生资源表、上下文切换、Namespace/Label 范围、Discovery/CRD 浏览、有界 LIST/WATCH 与
410 重同步、资源详情、YAML 编辑和字段差异、Events、容器日志、仅回环端口转发；
创建/修改/扩缩容/重启/删除的服务端 dry-run、名称确认和 UID/resourceVersion 并发保护。
这不是多租户平台，也不是完整 IDE。

## 本轮新增

1. 内置工作负载健康判定：Deployment、StatefulSet、DaemonSet、Pod、Job、Node。
   区分 API 接受、控制器观察和实际就绪，未知 CRD 不推断健康。
2. 原生 Health 面板和就绪跟踪：固定 UID/Generation、替换/新版本停止、授权错误停止、
   超时/取消语义、暂时读失败退避、写入与就绪分开记录；无自动回滚或写请求重放。
3. 修复最小窗口的详情操作栏溢出，增加原生交互和截图回归。
4. 测试隔离：所有真实集群 fixture 显式使用专属 kind kubeconfig 和破坏性测试许可，
   不再隐式读取用户默认集群。提供可复现的 `scripts/e2e.sh`。
5. 增加真实 Deployment 扩缩容到就绪的 API/原生 UI 测试；增强三平台原生交互 CI；
   用 evidence validator 阻止空测试、跳过、失败、截断或缺少必需场景被计为通过。

本轮运行结果以 PR 中的新提交及其 CI 证据为准；请勿将上述“新增”理解为已通过所有环境。

## 尚未完成的产品目标

| 目标 | 当前缺口 |
| --- | --- |
| 完整排障工作台 | 交互式原生终端/exec、Metrics 图表、关系导航、日志高级检索 |
| 多集群体验 | 同时活跃的多集群工作区、多窗口/页签模型、持久化偏好 |
| 变更治理 | SSA 字段所有权、Helm、GitOps/PR 工作流、复杂批量操作 |
| 原生编辑器 | Schema 补全/诊断、增量文档模型、完整 IDE 级编辑和三方合并 |
| 凭据与身份 | 系统密钥库、OIDC/PKCE、exec helper 加固、短期凭据轮换 |
| 企业服务 | Gateway/Connector、租户隔离、服务端策略/审批、远程审计与撤权 |
| 发行与验收 | 签名安装包、安全升级/恢复、SBOM、长稳/故障注入、真实 IME/无障碍/DPI |

这些项目不是“只差配置”。只有功能实现和对应测试都存在，才能改变其状态。

## 中断后恢复步骤

读取所有 open PR 和评论，检查当前 head/base、未提交变更及 CI 实际 checkout SHA；
下载源码/evidence artifact 并验证 checksum；复跑本地单元/竞态；在同一工作分支持续提交。
真实集群测试由独立 kind CI 执行，轮询到成功或明确失败，读取失败日志后修复。
保持 main 和 Release 不变，除非用户另行授权合并/发布。不要以“CI 已触发”代替测试结果。
