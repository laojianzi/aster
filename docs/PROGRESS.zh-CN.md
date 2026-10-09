# Aster 实施状态与恢复记录

更新：2026-10-09。此文件不是生产验收证书；恢复时必须重新读取 GitHub refs、开放 PR、
评审和 CI，不能把上一提交的绿色结果用于新代码。分支去重决策见 [BRANCHES](BRANCHES.zh-CN.md)。

## 已合并的范围

| PR | 功能 | 合并提交 / 验证记录 |
| --- | --- | --- |
| #1 | Go/MyGo 原生基础工程 | 已合并；后续修正以主干为准 |
| #3 | 原生资源、日志/转发、命令、安全变更及恢复 | 6668bcb7a6a5c1d95c92aae20e5a5d4d97233e75；PR CI #51、main #52 |
| #4 | 凭据重定向阻断、写操作结果不确定时不重放 | dfc7632101ba6def43aabf0e3197702cbfd3c5ee；PR CI #54 |
| #5 | 受限资源关系及 UID 导航 | 0c4666d27b33e1c56ab18fbabf55a014c1229645；PR CI #58、main #59 |
| #6 | Pod/Node Metrics 与有界趋势 | afefc00ce2f9eb3c5804b6b09a6b5f7da36bfa23；PR CI #61、main #62 |
| #7 | 独立原生工作区、双集群隔离 | a795269fda0f4d965fbc40949e2d827210948a63；PR CI #63、main #64 |
| #8 | 已有对象 SSA、字段所有权与并发保护 | ed8a5b594364be2ee372f9f87d3b561d4274dae7；PR CI #68、main #69 |
| #10 | 加固原生交互式 Terminal/TTY | cf5c8ff5a66da1c6e3a3dd66544310d59a225977；PR CI #74、main #75 |
| #9 | 经批准的依赖更新配置 | 8d958719d02e45fbecc824d81de1958b54e60e2d；PR CI #78，官方 Renovate strict 校验 |
| #12 | 受控 exec 认证与凭据到期关闭 | 7c090d3a0fa0fe7f49ee053a1d3531a0023afa16；PR CI #81、main #82 |

#2 已关闭且未合并，其旧可变 Executor 由 #3 的不可变 Prepared 模型替代；不要重新引入。
#8 在上一轮复核最终 head `fc51a0091c7030f17af25244e26271377622618a` 后按用户授权合并。
源码 tree `c262a10d85ed007558ba54797e45bbbdfb3ea1d5`，实际测试 checkout
`01997f9dd282a5c495673b8d0786f3261ead172f`。CI `37875710570` 和源码产物
`37875710533` 成功；全部八份测试产物和源码已下载校验 SHA-256、必需用例和提交身份。
233 个单元/竞态用例（含子测试），三个 Kubernetes 版本各 64 项组合测试：31 API、8 真实
集群原生联动、25 原生组件。另有双集群场景和三平台两窗口冒烟。合并后 main CI
`37880381475` 成功。上述数字仅证明 #8，不是新终端代码的证据。

## 最近完成与当前接续

原生终端 #10 的最终 head `85e64260bffbdae796654a731c82929505758dec` 已完成全部八个
CI 作业并合并。下载校验源码和八份产物，253 个单元/竞态用例（含子测试）；每个 Kubernetes
版本 72 项组合测试（36 API、9 真实集群原生联动、27 原生 UI），独立双集群场景通过；
三个 OS 各 32 个原生用例（5 模拟器、27 UI）和双窗口终端绘制通过。软件渲染与 AT-SPI
不可用不算物理 GPU/无障碍资格。详细证据绑定在 #10 的最终 review。

#9 在原 renovate/configure 分支增量审查：禁用自动合并、需要 dashboard 批准后才生成
依赖更新 PR、Kubernetes 模块成组、MyGo 耦合补丁人工升级。官方校验器运行在受支持 Node24。
没有借配置 PR 升级产品依赖。

#12 已完成并合并的 `feat/bounded-exec-authentication` 实现受控连接时 exec 认证：明确
信任、无 stdin、时间/输出/环境上限、Unix 进程组/Windows Job Object、令牌或证书快照、
到期关闭连接和全部流、旧回调隔离。无静默刷新，需要重新 Connect；静态凭据保持原行为。
详见 [认证契约](authentication.md)。这不等于 OIDC、系统密钥库或完整企业身份治理。
最终 CI/head/合并状态以该功能 PR 的验证记录为准，不用上一次数字代替新提交证据。

## 当前接续：Issue #14 / feat/native-schema-assistance

恢复时 main 为 `7c090d3a0fa0fe7f49ee053a1d3531a0023afa16`，没有开放 PR。
已有 Schema 分支与 main 完全相同，尚无提交；沿用该分支和 Issue #14，不创建重复任务。
#13 是未完成产品的单一跟踪单；#11 保持 Renovate 依赖审批面板，不复制依赖更新 PR。

本分支新增当前身份的 OpenAPI v3 字段帮助和有界结构提示，覆盖原生 Edit 的显式读取、
JSON Pointer、草稿检查、取消和过期回调隔离。读取不修改草稿、不产生变更计划，所有
写操作仍须已有 dry-run/确认。说明及明确限制见 [Schema assistance](schema-assistance.md)。
尚不是完整 Schema/CEL 校验、补全插入或增量编辑器；最终 CI/head/合并记录以功能 PR 为准。

## 当前已有可执行能力及边界

资源/CRD 浏览、Namespace/Label 范围、Discovery、有界 LIST/WATCH 与 410 重同步；
详情、YAML 编辑/字段 Diff、Events、日志、回环端口转发；创建/修改/扩缩容/重启/删除的
dry-run、名称确认、不可变单次 Prepared 与 UID/resourceVersion 前置条件；Health/就绪
跟踪；有限一跳 Related；Pod/Node 近期 Metrics；四个以内独立工作区；已有对象的无 force SSA。

Command 与 Terminal 都可能产生副作用，无法 dry-run；断开不代表容器进程终止。
TTY 的升级前 UID 检查不是 API 级原子身份锁定。指标不是完整历史监控，关系不是完整拓扑，
编辑器不是完整 Kubernetes IDE，多工作区不是跨身份共享缓存或企业访问网关。

## 未完成目标与优先级

| 优先级 | 范围 | 剩余工作 |
| --- | --- | --- |
| P1 当前 | 原生 Schema 辅助（#14） | 有界字段帮助/结构提示、原生交互、真实集群与最终证据；不重复 #12 已完成认证 |
| P1 后续 | 凭据与身份 | 系统密钥库、OIDC/PKCE、自动续期身份绑定、企业即时撤权 |
| P1 后续 | 原生编辑和交付工作流 | Schema 补全/诊断、增量文档、Helm、GitOps/PR、复杂批量变更 |
| P1 后续 | 企业管控 | Gateway/Connector、租户隔离、可信策略/审批、远程审计与撤权 |
| P2 | 更完整工作台 | 历史指标、聚合工作负载、拓扑、日志检索、持久化工作区与跨窗口总预算 |
| 发布硬门槛 | 发行和资格验证 | 签名安装包、安全更新/恢复、完整 SBOM/依赖审查、长稳/故障注入、真实 IME/无障碍/GPU/DPI |

这些不是“只差配置”，绿色回归矩阵也不是企业生产认证。目前不发布正式 Release。

## 中断恢复与合并规则

先读取所有分支和开放 PR，比较独有提交；历史辅助工作流不计作缺失产品代码。
同一功能只维护一个分支和一个开放 PR。下载源码和 CI 产物，校验哈希及实际 checkout；
生成源码还要核对生成文件清单和补丁哈希。真实测试只使用明确隔离的 kind 环境。
持续轮询到测试完成，失败时读取日志、修复并验证新的最终提交，不降低门槛换取绿色结果。
完成范围可按用户授权以 expected head SHA 合并，随后检查 main CI；其他范围仍如实保留。
