# Aster 实施状态与恢复记录

更新：2026-10-09。本文件不是生产验收证书。恢复时先读取 GitHub refs、开放 PR、Issues、
评审与 CI；分支去重见 [BRANCHES](BRANCHES.zh-CN.md)。产品剩余范围统一由 #13 跟踪。

## 当前任务：显式系统凭据库 #18

依赖 PR #16 已完成 MyGo v0.3.6、Go 1.27.2、Kubernetes 0.37.1 和精选依赖升级；
#17 的 x/sys 0.49.0 也已合并。依赖看板 #11 当前没有待更新分支，不重复创建升级 PR。
本轮从 main `e1cbd416ecbbb3f03a5785e6f659637eebfd241d` 接续既有
`feat/native-credential-vault`；其中仅有开发环境更新提交，没有已推送的凭据库实现。
不再新建同范围分支，剩余目标由 #13、当前凭据范围由 #18 唯一跟踪。

新增 Native Review / Store / Connect with stored token / Forget，使用当前 OS 凭据库，
绑定 HTTPS/TLS/CA/context/user，拒绝混合凭据；同二进制有界 helper、输入清理、显式
确认与结果隔离，复用现有连接期限和资源/流清理。无明文回退、静默查找或续期，不改写
kubeconfig。详细限制和测试契约见 [凭据库](credential-vault.md)。
本地回归通过不等于实际 OS 验证，最终 PR/主干 CI 和产物核验以 #18 的接续记录为准。

## 已合并里程碑

| PR | 已完成范围 | 参考验证 |
| --- | --- | --- |
| #1 | Go/MyGo 原生基础工程 | 后续修正以主干为准 |
| #3 | 资源、日志/转发、命令、安全变更和同步恢复 | PR CI #51、main #52 |
| #4 | 阻断凭据重定向和不确定变更重放 | PR CI #54 |
| #5 | 有界一跳资源关系和 UID 导航 | PR CI #58、main #59 |
| #6 | Pod/Node 当前指标及本地有界趋势 | PR CI #61、main #62 |
| #7 | 独立工作区、两个真实集群隔离 | PR CI #63、main #64 |
| #8 | 已有对象的 SSA、字段所有权和并发保护 | PR CI #68、main #69 |
| #10 | 加固原生交互终端/TTY | PR CI #74、main #75 |
| #9 | 受控 Renovate 配置 | PR CI #78、官方 strict 校验 |
| #12 | 非交互式有界 exec 认证和凭据到期清理 | PR CI #81、main #82 |
| #16 | MyGo v0.3.6 及用户批准依赖升级 | 已合并；终端补丁重新核验 |
| #17 | x/sys v0.49.0 | 已合并；沿用原 Renovate PR |
| #15 | OpenAPI v3 字段帮助和有界结构提示 | PR CI #85、main #86；#14 已完成 |

#2 已关闭未合并，其旧可变 Executor 已被不可变 Prepared 模型替代，不重新引入。
所有历史证据绑定具体提交；详细源码 tree、生成补丁、实际库和测试身份见对应 PR 的验证记录。

## 已有能力与边界

资源/CRD 浏览、Namespace/Label 范围、Discovery、有界 LIST/WATCH 和 410 重同步；
YAML/字段 Diff、Events、日志、回环端口转发；创建/修改/扩缩容/重启/删除的服务端 dry-run、
名称确认、不可变单次 Prepared、UID/resourceVersion 前置条件；已有对象的无 force SSA；
Health/就绪跟踪、一跳 Related、近期 Pod/Node Metrics、最多四个独立工作区。

Command 与 Terminal 有副作用，无法 dry-run；断开不保证远端进程终止。TTY 的升级前
UID 检查不是原子身份锁。终端通过固定哈希的原生库自绘，输入/scrollback/网格有界，
拒绝远端剪贴板写入和链接打开，运行时不下载原生代码。见 [Terminal](terminal.md)。

受控 exec 认证仅在明确 Connect 时运行，限制时间、输出和环境；Unix 进程组/Windows
Job Object 提供生命周期控制，不构成恶意程序沙箱。令牌/证书快照到期关闭连接并清理
私有数据。没有静默续期；见 [认证契约](authentication.md)。

Schema 辅助是当前身份的显式只读请求，对 JSON Pointer 和草稿给出有界结构提示；
取消和结果隔离绑定 session/detail/draft/pointer。既不修改草稿也不替代 dry-run，
没有提示不等于完整 Schema/CEL/准入校验通过。见 [Schema](schema-assistance.md)。

## 未完成目标与优先级

| 优先级 | 范围 | 剩余工作 |
| --- | --- | --- |
| P1 当前 | 系统凭据库 #18 | 原生操作、真实 OS/Kubernetes 联动、最终证据和合并 |
| P1 | 凭据与身份 | 系统凭据库、OIDC/PKCE、身份绑定续期与企业撤权 |
| P1 | 编辑和交付 | 增量文档、补全插入、诊断位置、Schema 刷新、Helm/GitOps、批量变更 |
| P1 | 企业管控 | Gateway/Connector、租户隔离、可信策略/审批、远程审计和撤权 |
| P2 | 工作台深度 | 持久化设置、应用总预算、历史监控、聚合工作负载、拓扑与日志检索 |
| 发布硬门槛 | 发行及资格 | 签名安装、安全更新/恢复、完整 SBOM/漏洞审查、长稳/故障注入、物理 IME/无障碍/GPU/DPI |

绿色回归矩阵不是企业生产认证，也不证明未实现的范围完成。当前不发布正式 Release。

## 中断恢复与合并规则

先核查全部 refs、PR、Issue；历史辅助工作流不视作缺失产品。每个范围一个分支/PR。
下载源码和 CI 产物，核对哈希及实际 checkout；生成源码还须匹配文件清单和补丁。
真实测试只使用明确隔离的 kind 环境。轮询到实际结果，失败读取日志、修复并验证新 head，
不删除必需断言换取绿色。完成范围按用户授权以 expected head 合并，再核查 main CI。
