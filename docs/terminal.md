# 原生交互式 Pod Terminal

Terminal 使用 MyGo Native UI 和固定版本 libghostty-vt 自绘，不是 WebView，
也不会在本机启动 Shell 或调用 kubectl。原来的 Command 面板继续用于非交互式命令。

## 使用

打开运行中的 Pod → Terminal → 选择容器和明确的 JSON argv → 输入准确的 Pod 名称 → Open terminal。
初始 argv 是可见的 `["/bin/sh"]`，并不隐式解释字符串；目标镜像必须具有所选程序。
修改 argv/容器会清除确认。TTY 合并 stdout/stderr，真实退出码来自 Kubernetes 的状态通道，
不能从本地终端模拟器的退出码推断。进程退出后可审阅当前屏幕，Close terminal 后可重新连接。

切换详情页签、关闭详情、断开集群、关闭窗口或期限到达都会关闭本地连接。默认最长十五分钟，
不自动续期/重连/重放，也不在 WebSocket 升级失败后自动改用 SPDY。
底层会话 API 最大允许三十分钟，桌面入口固定十五分钟。

**关闭连接、超时或超限不保证容器内进程被终止。** Pod UID 与容器运行状态在升级前检查，
但 Kubernetes exec 接口按 Pod 名称寻址，无法原子地绑定 UID 或阻止容器在升级间隙重启。
服务端与代理必须支持 WebSocket v5；Kubernetes 授权仍是最终权限边界。

## 容量与安全边界

每个工作区最多一个尚未回收的终端；关闭后必须等本地执行器和 IO 回收才能再次连接。
输入队列同时计入排队与正在写入的内容：128 KiB、256 块；单次文本输入/粘贴最多 16 KiB
（编码后的队列块另留 16 字节控制序列预算）。超过预算明确断开，不静默丢输入。
输出通过 io.Pipe 背压保序；原生显示保留约 2 MiB scrollback，网格上限 400×200。
这些是分项预算，不是整个应用 RSS、原生解析器或 GPU 内存的硬上限。

远端 OSC 52 写入被拒绝，远端通知/标题回调关闭，终端中的链接不启动本地应用。
用户主动选择、复制和粘贴仍然可用。命令参数和终端字节不写入应用历史或持久化文件，
历史只记录目标与会话结果；这不是企业审计或终端录像。关闭屏幕释放原生状态，但不承诺
操作系统剪贴板清除或进程内存的密码学擦除。

## 可复现构建

使用 `python scripts/dev.py build`（类 Unix 系统可用 `make build`），测试用
`python scripts/dev.py test`。首次构建需要下载 go.mod 固定的公共 Go 模块和原生库。
离线时向这两个命令传入 `--source PATH/terminal --library-source PATH/lib`，仍校验同一组哈希。

`third_party/native-terminal/UPSTREAM.json` 固定 MyGo v0.3.6 的每个使用文件；
`hardening.patch` 是可审阅的输入、剪贴板、生命周期与库加载补丁。
`scripts/prepare_terminal.py` 验证文件哈希，严格应用补丁、改写内部导入路径，生成
`internal/nativeterm/upstream/`。不要直接修改生成目录，也不要仅运行未准备依赖的 go build。
CI 保存生成文件清单、补丁哈希和生成源码，便于与确切提交共同复核。

原生库从上游固定 manifest 校验 SHA-256，随开发二进制一起打包；运行时仅从应用资源或
可执行文件目录读取，不搜索环境变量/用户缓存，不自动下载。Windows 依赖 DLL 搜索限制到
库目录和系统目录。安装目录必须不可被不受信任的用户修改；加载前哈希校验不消除已被攻击者
控制的目录中的 TOCTOU，也不构成原生代码沙箱。

开发产物附带 MyGo/Ghostty 主许可证和 terminal-provenance.json，但尚不是签名安装包。
签名、公证、完整 SBOM/第三方依赖审查、升级恢复、长稳、真实 IME/无障碍/GPU/DPI 资格验证
仍是独立的生产发布门槛。

## 验证

单元/竞态测试覆盖输入上限、中文原生输入、OSC52、库篡改拒绝、尺寸合并、IO 取消回收、
权限/协议失败不重放、目标替换检查和最小窗口布局。真实 kind E2E 另外验证交互式 stdin、
远端 `test -t 0`、stty 尺寸、退出码 7、期限、取消、RBAC 拒绝及独立检查未执行。
原生联动测试使用真正的 MyGo 控件和实际 API Server；Linux/macOS/Windows 的窗口冒烟
渲染原生模拟器与两个窗口，但不等同于实物键盘/IME或全场景桌面自动化。
