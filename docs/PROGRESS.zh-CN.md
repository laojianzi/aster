# Aster 实施状态

## 当前实施：#123 原生首次安装界面与 #125 捕获诊断 — 2026-10-28

候选 `3527be1ba17a47b4af3ff86627d4fc46a511752e` 的正式 CI/Source/OIDC 已通过；
原始 race 和 coverage 各 4,082 个具名用例通过，UI 分别为 564/564、214.086 秒及
44.474 秒。另一本地完整 UI race 在原五分钟预算内 257.563 秒通过。它们不是该
候选的完整资格：Migration `40669824285` attempt 1 的 Windows 旧 Discard capture
在 10.83 秒返回固定 ErrRender；同 head、原参数完整 attempt 2 的旧 Lifecycle/hook
capture 在 15.84 秒返回 context deadline exceeded。两轮新 42 名均通过，但 Windows
checked 失败后 production/build/smoke 未运行；所有失败原件保留，停止同 head 重跑。

[#125](https://github.com/laojianzi/aster/issues/125) 已提升为 P1 验收阻塞，由同一个
PR #124 / `feat/native-helm-install` 承接，不再建重叠分支。普通 Windows 与 migration
checked 使用相同默认构建，两次新增并行尾组均晚于旧 capture 失败。没有证据证明
checked 独有开销、杀毒扫描、文件系统或 Job Object 是根因。

本分支追加[有界捕获诊断](helm-render-diagnostics.md)：旧 Render/Helper API 的固定
错误、优先级、15/10 秒、私有输入和真实进程 containment 保留；显式诊断 API 返回
每次调用独立的固定阶段、类别、单调耗时和清理状态。旧 UI 的失败 status 附加安全
摘要，使原有 captureHelmUI 的失败原流可以直接定位阶段，不修改旧 Go 测试。
不输出私有内容、原始错误、路径、参数、环境或 stdout/stderr，也不额外 render/重试。

新增诊断仅三个精确 Go 测试文件；其 17 个 renderer、13 个 helper、5 个 UI 名称独立
追加到适用的原门禁。原生安装 42+4 census 不变，原 263/278 路径、mode 和所有旧
聚合哈希保留；历史 scope 夹具显式追加独立诊断集合，或扣除该集合后继续核对原数量/摘要。
旧 PNG helper 的唯一标识符例外保持精确逆变换。当前诊断增量仍需自己的完整本地
验证、最终 PR/实际 main 四门及新原件；新增诊断并不表示两个历史失败根因已修复。

当前已独立验收的主干为 `85e9b697b0cc4329f8398197bffcf260af6b5a90`，tree
`cfd7d7db7dd6f126052c2065e1f198f501bcca32`。[#112](https://github.com/laojianzi/aster/issues/112#issuecomment-6464349745)
及 [#121](https://github.com/laojianzi/aster/issues/121#issuecomment-6464338760) 已结项；
PR #122 最终 PR 与实际 main 各自完整四门及全部新原件通过。实际 main 使用 CI/Source/OIDC
attempt 1、Migration attempt 2；Windows 首次捕获失败完整保留，原样复验通过不证明延迟根因已修复。

真实退役 run `40656172725` 先保存 before.bundle，再按既有流程原子退役；其后完成全部
原件审核、六图审核集合、parity 和独立恢复。恢复完整核对 49 refs（含 HEAD）、325 commits、
3,112 objects。该实际顺序与 #121 原计划不同，原第六项保留不勾选；技术范围及可恢复性已结项。
旧 46 tags 原映射保持，唯一新增 `maintenance/completed-pr-122` 指向该 main；不存在 #117 收据。
[项目结项记录](https://github.com/laojianzi/aster/issues/13#issuecomment-6464361295)保存准确边界和时间线。

2026-10-28 08:35:57 UTC 入场重新核对：一个远端 main、47 tags、0 open PR、8 open Issues。
接续仅创建 [#123](https://github.com/laojianzi/aster/issues/123) / `feat/native-helm-install`；
[草稿 PR #124](https://github.com/laojianzi/aster/pull/124) 于 09:25:57 UTC 实际创建，
复用 capture、GET 准备、server dry-run、v2 WAL、持久执行及混合历史/新授权只读恢复后端。
最小产品范围是 revision-1、同 namespace、1–32 个普通 ConfigMap；独立可信捕获、GET、
dry-run 和精确 release 名加独立执行确认，各阶段均需主动同意，不自动写入。

冻结候选后不再访问原工具/chart/values 文件；输入选择、连接身份改变，以及取消、关闭或到期均撤销当前流程。
成功 dry-run 的 origin 绑定整个流程，而非短期 worker；执行与 journal 由 worker 收尾，
窗口销毁不依赖 UI completion 完成日志关闭。双击/重入即刻阻断，真实效果与本地日志完整性分别展示。
预算不足、锁冲突及未决 v1/v2 历史由既有后端在持久请求前拒绝；不伪造卸载修订身份。

本 scope 追加原生及真实集群接线证据，保留本基线 263 个旧 Go 测试文件、278 个受保护
路径的断言/mode/path。正式 a13 超时后，仅增加下述精确 PNG 编码标识符例外；严格逆变换
恢复接受基线全部字节。旧入口禁令只迁移为精确获准调用位置，全部原 inventory 和历史哈希保留。
九个 workflows、工具 pins、5 分钟整包和 20 分钟 job 预算不变。

本地完整 Python gate 首轮 264 项、实际 PR 登记后 268 项均通过；最终 E2E 源仅编译通过，
未在本机运行真实集群。
第一轮完整 UI race 在 300.042 秒中止，并暴露新增入口挤压旧 1.5 倍字号预览的回归；
入口现仅在未选 revision 的列表占用额外行，进入检查/预览后恢复原父布局。
修复后原 522 个具名用例全部通过，但整包仍在 300.056 秒超时；新增 42 名中
32 run、27 pass，其余未完成，不能把该流记为通过。新增测试集中运行并分别串行化
真实 capture 和 renderer，保留原候选期限、独立授权、全部旧测试及包级预算。
上述失败原件和较早夹具失败均保留在 PR 记录中；最终资格及退役恢复以 #124 的准确
最终 PR/实际 main 收据为准。本段不是已完成或发布声明。

候选 `a13b6c99be25f36a3c2d1773cfe02127cda0bb5f` 的实际 Windows CI 及迁移验证
发现新增 busy 测试夹具在持有排他锁时从另一个句柄读取 `journal.lock`，被 Windows 正确拒绝。
修复仅调整新增夹具的完整磁盘快照时机：before 在持锁前，after 在释放锁后；执行及
零网络/零写入/NotStarted UI 断言仍在真实锁内，全部 lock/WAL 字节比较和 Fatal 清理保留。
单次 Linux busy 定向 race 根与子例 2/2 通过（6.957 秒），不替代最终 Windows CI。
原失败 ZIP `12806286257`（native）及 `12805532856`（migration）均完整保存并匹配 API 摘要，
后续候选必须重新获得自身门禁，不能复用失败候选的其他成功任务作最终资格。

a13 的正式完整 race 也在 300.080 秒达到原五分钟上限，原 522 名全部完成时已耗
277.334 秒；UI 共 560 run、556 pass，零具名 fail/skip，新增未全部完成。
原 unit ZIP `12806388056` 及完整 JSONL 保留。容量夹具消除重复历史扫描后，仍逐件
验证 128 条真实 writer 生成的原样 WAL、准确 RecordLimit 原因和完整磁盘字节；
renderer 锁仅覆盖 busy 子例的全部 UI 及清理。随后新 42 名定向 race 全过，但实际
尾组仍为 35.783495 秒，不能记作已解决整包预算。

三张同源帧的 race 诊断中，NoCompression 纯编码耗时约为 BestSpeed 的 24%–33%，
像素/alpha/尺寸完全相同；原始 PNG 明显增大。为保留全部截图及原五分钟门槛，唯一旧
Go helper 变动限定为 `workbench_test.go` 的 `png.BestSpeed` → `png.NoCompression`。
新全文件必须精确匹配 `e5c0f77aedf6e8407d58223b6e9b4ee56ff261f4f7d6cabba560fdc9feabb0e2`，
逆变换恢复接受的 BestSpeed 全文件和原 263/278 聚合 `4d753b61…`；三个历史聚合也保留。
这不是文件豁免，Source 证据始终哈希真实提交字节。

已完成同一接受基线原件的 143 帧独立证明：133,202,400 个不透明像素全部一致，输入
不变；BestSpeed 重编码 143/143 的 PNG SHA 也与原件相同。单次 race 下纯编码总 wall
58.444426271 → 14.748345946 秒，PNG 总体积 14,847,906 → 399,902,587 bytes，
最大单张 15,560,743 bytes。4096 像素、全部 256 种 alpha 的额外探针，两模式解码
像素完全相同且 alpha 保留；两者相对 premultiplied 源均有相同的 1479 个 RGB 量化差异，
不能扩大为任意 RGBA 源无损往返。完整结果 SHA-256
`6b1517097abc320313c66fe52b002f6f8a21e6fe280e88bb484953b9b5bb720d`。
该局部 CPU 证明不包含 UI、实际磁盘或 ZIP 上传成本。完整包和正式最终 PR/main 门禁
仍须实际验证；不放宽 ZIP 展开、单成员或 job 预算。
15 项相关 Python 守卫在 0.448 秒全部通过，包括两个精确逆变换和四个真实 inventory
守卫的正负矩阵；旧 263/278 数量、聚合、全部旧 mandatory 与新增 42+4 census 未改。

P2 #118 底栏、#119 顶部布局、#113 字号缩放、#115 OIDC issuer readiness；P3 #116 到期
文案、#120 表单标签；#11 依赖及其余 MVP/发行验收仍独立开放。企业权威仍延后 P3。

## 历史接续：#121 修复主干验证阻断 — 2026-10-28

最后完成独立验收的基线是 `cb1afdcf6ba7ad9bfe17faf0e760b248f06b2b6a`；
[#111/#114 最终收据](https://github.com/laojianzi/aster/issues/111#issuecomment-6461462336)
包含真实主干原件、实际退役和完整 bundle 恢复，#111 已 completed。

当前集成基线为 `54af1534cbbb0b31306093c82380bedb282f100c`。#112/PR #117
的终端准备修复已经合并，默认入口在内外 TMPDIR 的 prepare/build 均通过，33 个生成
文件与 CI source 原件逐字节一致；但实际主干 CI 两次触发 UI 整包五分钟超时。
因此 54af 不是已通过全部门槛的基线，#112 继续开放；不生成 completed-pr-117 收据。

唯一当前实施的 P1 范围是 [#121](https://github.com/laojianzi/aster/issues/121)，针对已测量的无损
截图编码成本做窄修复。候选仅把 saveNativeScreenshot 的默认 PNG 压缩改为 BestSpeed；
生产代码、全部旧测试断言、图片范围、同步写入/关闭错误、race/shuffle、五分钟包期限
和二十分钟 job 预算保持。六帧局部编码测量不能代替完整包或主干资格；完整 UI 基线/
候选使用同一失败 seed 顺序测量，最终结果与原件以 #121 的收据为准。
历史 scope 哈希保护只允许精确逆变换恢复原 helper 字节及原聚合哈希，不豁免整文件。

本机同 seed 的完整 Go-only UI race A/B 已通过：两轮各 522 个具名 run/pass、209 个
顶层测试和 143 张图；package Elapsed 为 285.401→260.207 秒（减少 8.83%），五分钟
余量 14.599→39.793 秒。该单机单次结果不是跨 runner 保证，也没有在本机重现超时。
全部143个不透明冻结帧的133,202,400像素及alpha重编码一致；跨执行112张同像素、
31张差异原件保留，不称全部跨跑图相同。最终PR/新main仍须独立完整验收。

维护接续 [PR #122](https://github.com/laojianzi/aster/pull/122) 于 06:39 UTC 实际创建，
复用已保留的 fix/terminal-preparation-tmpdir 分支，沿用 #81→#83 先例，
避免新建同类远端分支。仅在创建后登记实际编号 122；它须完成自身最终 PR 与实际
新 main 四组门槛、新原件和真实退役恢复，旧 #117 两次失败原件保持历史事实。
恢复核验必须区分 last-qualified cb1/46 tags 与实际集成父提交 54af，不能把后者改称
已验收基线。完成该接续后才显式完成 #121/#112；不重复实现终端准备或安装恢复。

其后按 [#13 原生首次安装 UI 计划](https://github.com/laojianzi/aster/issues/13#issuecomment-6462109098)
复用已有 capture/prepare/preflight/WAL/执行/恢复。P2 #118 底栏、#119 顶部布局、
#113 实际字号缩放和 #115 OIDC issuer readiness 均独立保留；P3 #116 到期文案、
#120 表单标签/端口说明、#11 依赖以及其余 MVP/发行验收继续开放。企业权威仍延后 P3。

## #112 / PR #117 入场记录 — 2026-10-28

**本次基于实际 main `cb1afdcf6ba7ad9bfe17faf0e760b248f06b2b6a`，Go 1.27.3、MyGo v0.5.5。**
[#111 / PR #114](https://github.com/laojianzi/aster/pull/114) 的安装恢复实现已合并；
[最终 PR 原件收据](https://github.com/laojianzi/aster/pull/114#issuecomment-6461159712)
记录 14 ZIP、28 原始流、17,227 次具名 run/pass、六个完整二进制与独立截图审阅。
入场时 #111 的实际主干原件和退役/恢复仍在验收；其闭环是本次修复合并的前置条件。
最终结果见 [#111](https://github.com/laojianzi/aster/issues/111)，不能以 PR 原件替代 main 原件。
#103/#104 捕获/准备、#105/#106 server dry-run、#107/#108 WAL、#109/#110 后台执行
和 #111/#114 恢复均直接复用，不新建同类分支。原生 install 执行控件仍未启用。

**本次唯一修复范围为已有 [#112](https://github.com/laojianzi/aster/issues/112) /
[PR #117](https://github.com/laojianzi/aster/pull/117) /
`fix/terminal-preparation-tmpdir`。** 用户在同一工作期间报告了实际构建阻断；不重复创建
Issue，不混入安装恢复。2026-10-28 05:13 UTC 入场快照为 47 个实际 Issues
（40 closed，#11/#13/#111/#112/#113/#115/#116 open）、69 个 PR
（68 merged、#2 closed/unmerged、0 open）、main 和待退役恢复分支两个远端分支、
45 个恢复 tags、0 releases。独立修复分支由上述 actual main 起步；这是当时快照，
不是后续状态承诺。PR #117 于 05:17 UTC 实际创建后才登记；只批准该 PR 的准确分支，
新增退役回归拒绝 Issue #112、前一 PR #114、活动/移动/受保护分支和不完整主干门槛。

真实 A/B 已复现：同一组 31 个固定上游文件和原补丁，在仓库外实际应用六个补丁；
仓库内 TMPDIR 的 Git check/apply 却退出 0、六项全 Skipped。真实 dev.py prepare
错误报告成功，随后 build 以两个内部包导入错误失败；没有据此声称未加固程序可运行。
修复隔离 Git 环境、配置、attributes 和祖先仓库发现；发布前逐文件核对六个固定
postimage、其余 25 个原哈希及完整库存。成功退出不能代替实际加固内容。

新增清单是正式版本化准备输入，receipt 记录其 SHA；最终 source/六平台 provenance
必须与准确 Git blob 字节核对。原 UPSTREAM、补丁、依赖、九个工作流和旧测试原样保留。
本地 Linux 已实际运行内外 TMPDIR prepare/build，33 个生成文件、receipt 和二进制一致；
接续 main 后完整 Python 245 项通过，仍属于未提交候选本地证据。新增九个真实准备
方法由既有 Linux CI discovery 执行；三系统普通/production 构建和窗口冒烟另行验收，
不能称为三系统内外 TMPDIR 都完成 A/B。详见 [终端准备说明](terminal.md)。
补丁/输入/库存失败在发布前拒绝；没有承诺原生库下载等所有阶段的跨目录原子回滚。

最终精确 PR、实际新 main 四组门槛与独立原件、实际 PR 的准确退役登记、真实 bundle
和完整离线恢复仍需完成，再更新 #13、显式关闭 #112。旧开发包是其原提交快照，
使用修复需要包含修复的新源码并重新构建；本次不发布新 devkit 或 Release。

[#11 依赖面板](https://github.com/laojianzi/aster/issues/11) 的四项候选仍独立待审。
[#113](https://github.com/laojianzi/aster/issues/113) 为 P2 文字缩放，
[#115](https://github.com/laojianzi/aster/issues/115) 为 P2 OIDC 真集群夹具 issuer 就绪诊断，
[#116](https://github.com/laojianzi/aster/issues/116) 为 P3 浏览器会话到期提示分类。
这些是具体未完成问题，不能因旧测试通过而关闭，也不混入当前 P1 构建修复。

## 已完成、必须复用的能力

| 能力 | 已完成工作 |
| --- | --- |
| 原生资源/CRD、日志、转发、命令、TTY 与不可变审阅变更 | #1、#3、#4、#8、#10；#2 已被替代，不恢复可变 Executor |
| 资源关系、Metrics、独立工作区和双集群隔离 | #5、#6、#7 |
| exec 身份认证、OS 凭据库、OIDC/PKCE 与显式内存续期/发行方登出 | #12、#18/#19、#24/#25、#26、#27/#28、#37/#38 |
| TextBuffer、原始字节/快照、Unicode Find、位置导航与粘贴保护 | #29/#30、#32/#34、#35/#36、#63/#64 |
| 有界 Schema 结构/标量、multipleOf、可移植 pattern、date/byte/uuid 与结构化 enum | #42/#43、#46/#47、#48/#49、#50/#51、#52/#53 |
| 有界字符串/int64/bool map-list 与 set-list 身份提示 | #54/#55、#56/#57、#59/#60、#61/#62、#65/#66 |
| 已渲染清单的有序导入、逐对象不可变审阅、确认和部分结果核对 | #44/#45 |
| 可信本地 chart/values 快照、固定官方 Helm 4.3.0、渲染捕获/丢弃/接受 | #67/#68；不是 Helm release 写入或 GitOps |
| Helm Secret-driver 元数据协商、单命名空间分页与显式刷新/取消 | #69/#70；不是 release 内容或生命周期写入 |
| 单次内容同意、精确 Secret GET、UID/resourceVersion 与内容一致性只读摘要 | #71/#72；不认证真实性、不提供生命周期写入 |
| 不可变卸载范围、最新修订与实时对象观察、受限卸载/精确缺失契约 | #76/#77、#78/#79、#80/#81/#82；不重复只读清单 |
| 保留原始载荷的条件状态编码、完整受限目标准备、独立服务器 dry-run | #84/#85、#86/#87、#88/#89；读/预检同意不授予持久执行权限 |
| 独立单次卸载执行、真实条件效果、严格确认/实际缺失与保留部分/Unknown结果 | #90/#91；后端直接复用 |
| 强制私有 journal、真实进程中断离线恢复、新授权 GET 核对与未决阻止 | #92/#93；历史结果与当前事实分开 |
| 原生独立准备/dry-run/执行确认、结果/恢复展示及 worker 生命周期 | #94/#95；固定 home journal，未知历史与当前 GET 事实独立 |
| existing-only 本地 journal 准入说明与完整操作容量预留 | #96/#97；只读报告不授予执行权限 |
| 终结且无未决记录的同目录有界归档及原生独立审阅/确认/浏览 | #99/#100、#101/#102；保留原字节，不 purge/export/清除 Unknown |
| 不可变受限安装捕获、纯 revision-1 codec、GET-only 目标准备 | #103/#104；不创建 release 或目标 |
| 独立一次性、冻结字节与原期限绑定的安装 server dry-run | #105/#106；服务端预检不授予持久写入权限 |
| 安装 v2 WAL、跨操作共享锁/准入/预算、惰性离线恢复 | #107/#108；日志和恢复不重建执行许可 |
| 后台独立受限安装、严格响应/独立 GET、写前容量拒绝与 Partial/Unknown | #109/#110；新 revision-1、1–32 ConfigMaps，原期限/预算与 WAL 直接复用 |
| MyGo v0.4.8/v0.5.1/v0.5.5 来源与运行时、输入/布局/时钟保护 | #58、#74、#75；历史升级不重做 |
| Go 1.27.3 固定工具链与验收 | #98；与当前终端准备修复分开 |

证据以各自 Issue/PR 为准，不用合并数或绿色 CI 百分比代替 MVP 完成度。
归档/维护 tags 保存恢复历史，不是待开发分支。已完成分支仅在精确 head、合并/主干
门槛、活动 PR、保护状态与可达性检查后退役。存在实际 bundle 才验证离线恢复；
机器人提前删除而返回 already-absent 时，不制造归档或恢复证明。

## 首版 MVP 范围与剩余优先级

**企业权威服务按 2026-10-12 决定延后至 P3，排除首个 MVP，不作为首版验收条件。**
现有凭据处理、当前身份授权、不可变审阅和本地会话清理契约继续有效。

**当前优先产品 P1。** #112/#121 与 #111/#114 均已结项；继续 #123 原生 install
工作流，复用已有捕获/准备/preflight/WAL/后台执行及独立恢复，不从 GET 核对或历史
记录授予持久写入权限。#113/#115/#118/#119 为独立 P2，#116/#120 为独立 P3；
#11 依赖候选分别审查，不批量升级或为清单清零关闭。

**P1 — Helm release / GitOps。** #68/#70/#72 的本地渲染、修订元数据和内容一致性摘要直接复用。
已完成原生受限卸载、恢复、准入说明以及可保留原始证据的终结记录归档。
未决记录继续保留并阻止新执行；归档不能释放 Unknown/Partial，不能用新 GET 或只读
报告隐式放行。#111 之后仍有原生 install 接入、upgrade/rollback 和
GitOps repository/sync/reconcile 的可信执行、并发、失败/Unknown 与确认边界。
purge/export、未决记录处置和断电/文件系统持久性资格分别需要独立政策与验证。
不得把元数据标签、渲染或普通逐对象 SSA 重命名成完整 Helm 生命周期；不得直接运行
带集群凭据的 CLI 绕过现有 Prepared、strict dry-run、精确名称确认和 no-force 保护。

**P1 — 安全编辑器事务与大输入。** 已验收 MyGo v0.5.5 不代表全部组合契约已满足；
版本升级不等于完成原始换行、单一历史、IME 和任意大输入的验收。
安全补全仍需公开且实测的 raw 混合 CRLF/LF、单一 Undo/Redo、selection/composition、
文档身份和过期结果组合行为。任意巨型非剪贴板 Type/IME 的同步布局预算、跨卸载历史
仍开放；不使用私有 handle、模拟生产按键或第二套 Undo 栈绕过。
见 [上游编辑器审查](editor-upstream-mygo-0.4.8-review.md)、[buffer](manifest-buffer.md)
及 [clipboard](manifest-clipboard.md)。

**P1 — 剩余 Schema。** 复用已完成的标量/枚举/集合有界子集。更广泛的 format/pattern、
组合、ordinary uniqueItems、nullable/default-dependent 身份、完整列表/SSA、CEL 与准入
仍按实际 CRD/服务器语义、预算和隐私单独验收；不把本地提示替代真实 server dry-run。
见 [Schema 契约](schema-assistance.md)。

**独立身份策略。** 跨启动刷新凭据持久化、RFC7009 撤销/联合登出及可选自动续期需要
独立政策和失败行为审查。已有 memory-only renewal / browser handoff 不代表这些完成。

**P2 — 产品深度。** 持久设置、统一超时/读取预算、历史/监控/拓扑、工作负载聚合与日志
检索。复用现有 relationships、Metrics、workspace cancellation 和 bounded logs。

**发布验收。** 签名安装、可信升级/恢复、完整 SBOM/漏洞审查、长稳和故障注入、物理
键盘/IME、字体、无障碍、GPU 与混合 DPI 仍开放。已有未签名 CI 应用不是生产 Release。
只读 HTTP/native 测试和 runner 截图不建立这些发布资格。

**P3 延后。** Gateway/Connector、租户隔离、可信策略/审批/审计和企业会话撤权，需要
将来的可信服务边界；持有直接集群凭据的其他客户端不受桌面提示强制约束。

## 历史检查点

旧进度正文原样保存为 [截至 PR #68 的历史检查点](progress-checkpoints-through-pr68.md)。
其中“本轮”“当前 pin”“后续候选”和未勾选项描述当时状态，不能据此重建已完成任务。
从 Git 历史和对应 Issue/PR 追溯原始验收、失败重试与退役时序；不要倒置自动退役和
后来独立审查的顺序。新的实施/验收结果继续写入当前实际 Issue，而非重复堆叠旧快照。
