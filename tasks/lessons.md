# Lessons

## 2026-08-03 WSL2 VoHive 路线

- WSL2 中没有 Go/Node 且 sudo 需要密码时，不要改用户发行版系统包；优先使用项目内 `.toolchains` 放置便携 Go/Node 构建工具链。
- `npm` 可能从 WSL interop 解析到 Windows 路径，但 Linux 构建需要 Linux 原生 `node`；构建命令必须显式设置 `.toolchains/node/bin` 到 `PATH` 前面。
- `wsl.exe --exec` 启动的后台 shell/`nohup` 容易被会话生命周期和 PowerShell 参数转义影响；桌面壳启动 VoHive 时应直接 `Start-Process wsl.exe` 并传递 `--cd /opt/vohive --exec /opt/vohive/bin/vohive -c /opt/vohive/config/config.yaml`，由 Windows 侧持有进程句柄。
- WSL 的 transient `systemd-run` unit 在当前调用方式下会随会话结束被回收，不适合作为桌面壳的默认启动方式；后续若要 systemd 常驻，应安装持久 unit 文件并明确 WSL 保活策略。
- WSL 内代理环境变量会影响 `curl`，本地健康检查应使用 Windows 侧请求，或在 WSL 内使用 `curl --noproxy '*' http://127.0.0.1:7575/`。
- `usbipd-win` 是 WSL2 USB 直通的硬前置；MSI 安装需要管理员权限。非管理员静默安装会报 `Error 1925`/`1603`，不能把“msiexec 进程返回”误判为安装完成。
- `usbipd-win` 安装后当前 PowerShell 进程的 PATH 可能不会刷新；检测时应先查服务和 `C:\Program Files\usbipd-win\usbipd.exe`，桌面壳可直接调用该绝对路径。
- `usbipd attach --wsl` 在 `usbipd-win 5.3.0` 下需要目标 WSL2 发行版保持 Running；可先启动隐藏 `wsl.exe -d Ubuntu-24.04 --exec sleep 3600` 保活，再执行 attach。
- 当前大疆模块在 Windows 侧枚举为 `2ca3:4006 Baiwang`。WSL 默认不会自动绑定驱动，需要临时执行 `option`/`qmi_wwan` 的 `new_id` 流程：前 4 个接口给 `option` 生成 `/dev/ttyUSB0-3`，最后接口 `1-1:1.4` 释放给 `qmi_wwan` 生成 `/dev/cdc-wdm0` 和 `wwan0`。
- 如果把 `2ca3:4006` 全部交给 `option`，会生成 `/dev/ttyUSB0-4`，但没有 `/dev/cdc-wdm0`；VoHive 的 QMI 设备发现需要 `qmi_wwan` 控制口和 `wwan0`，桌面壳/WSL adapter 必须补这个驱动绑定步骤。
- VoHive 能发现该模块为 `mode=qmi`；在 WSL USB 驱动绑定稳定后，`with_imei=1` 当前已能读到 IMEI。若后续再次出现 QMI initial sync 超时，应作为 QMI/AT 稳定性单独调试，不和 USB 绑定问题混在一起。
- VoHive 首次运行会生成 `/opt/vohive/data/vohive.db`，并可能下载 `data/mcc-mnc-table.json`；离线安装包应预置该数据文件，避免首次运行时依赖外网。
- 停止 WSL 内后端时不要用宽泛的 `pkill -f /opt/vohive/bin/vohive` 放在同一个 shell 命令串里；它会匹配到当前 `bash -lc ...` 命令行并杀掉自己。应先枚举 PID，或使用更精确的进程管理方式。

## 2026-08-03 漫游策略与桌面壳

- GORM 布尔字段如果需要让用户显式写入 `false`，不要依赖 `gorm:"default:true"` 这类默认标签；应在策略创建/迁移层显式处理默认值，否则容易把“用户关闭”吞成默认开启。
- `card_policies` 这种按 ICCID 生效的策略字段，新增能力时要同时检查默认策略、旧库迁移、API PATCH/PUT 的“未传字段不覆盖”语义，以及设备上线后的策略投影。
- 对硬件即时生效的 live API 不要先发 AT 再落库；应先保存用户意图，保存失败就不碰硬件，硬件应用失败时再按旧策略回滚或明确暴露分裂状态。
- 前端设备/卡详情请求必须防旧响应覆盖；切换设备或 ICCID 时先清空旧状态，再用请求序号和 ICCID 归一比较确认响应仍属于当前对象。
- Windows Node 不能可靠复用 WSL Linux 的 `node_modules`；Web 测试和构建应明确使用 WSL 内 Linux Node，并把 `.toolchains/node/bin` 放到 `PATH` 最前。
- 不要并行对同一个 `node_modules` 目录跑多个 `pnpm` 命令；pnpm 可能把“不同包管理器安装”的依赖移动到 `.ignored`，导致本轮测试 runner 丢失。
- 在 WSL `/mnt/f` 上跑主 Web `npm run build` 可能超过 3 分钟，超时不等于构建失败；遇到无输出超时要先检查残留 `node/vite` 进程和 `dist` 更新时间，再决定是否重跑或清理。
- Tauri 桌面命令返回 `ActionResult { ok:false }` 后，前端必须显式展示 `message` 和 `suggested_admin_command`；只等待 Promise resolve 会把失败路径吞掉。
- 桌面壳持有 `Child` 句柄不代表后端仍在运行；状态刷新应使用 `try_wait()` 更新退出状态并释放句柄，否则后端异常退出后 UI 会误报运行中。
- WSL/usbipd 这类外部命令必须带超时，并把超时错误展示给桌面 UI；否则 Windows 服务、WSL 发行版或 USB 栈卡住时，用户只会看到按钮一直 busy。
- Tauri Windows 应用构建要求存在 `.ico` 图标资源；即使是 MVP，也要先准备 `src-tauri/icons/icon.ico`，否则构建会卡在资源校验。
- Tauri/Rust 在 Windows debug 运行时可能返回 `\\?\F:\...` 这种 verbatim path；转换给 WSL 使用前必须剥掉 `\\?\` 前缀，否则 Linux 会看到不存在的 `//?/F:/...`。
- 桌面启动按钮必须先看健康检查，`/ping` 已正常时应复用既有后端；否则用户从外部启动过 WSL 后端时，桌面壳会误以为“进程未运行”并尝试启动第二个实例。
- Windows GUI 程序如果直接打开出现黑框，检查 PE subsystem；Rust/Tauri 入口可用 `#![cfg_attr(windows, windows_subsystem = "windows")]` 隐藏 console。
- Windows 上如果 `npm.ps1` 指向 `C:\Users\<用户>\AppData\Roaming\npm\node_modules\npm\bin\npm-cli.js` 且目标不存在，不要继续用坏 shim；可直接调用 Node 安装目录自带的 `node_modules\npm\bin\npm-cli.js`，并用项目内 cache 避免用户级 npm cache 权限问题。
- `usbipd attach --wsl` 不能假设 WSL2 会自动启动；桌面壳应提供显式“启动 WSL”按钮，`连接 USB 到 WSL` 只做运行状态检查和提示，不要擅自启动 WSL。
- WSL/USB-IP 下从 `option` 切换 `qmi_wwan` 时，`unbind` 后立即写 `qmi_wwan/bind` 可能短暂返回 `no such device`；应短轮询重试并复查 interface driver，避免把内核异步绑定误报为失败。
- 如果 Windows 桌面安装包不能安装/配置 WSL2，也不能安装 `usbipd-win`，它就不要作为交付目标；优先保留可直接运行的桌面 exe 和后续便携包策略，避免生成误导性的 setup.exe。

## 2026-08-03 Git 提交规范

- 每次本地提交都要编写详细中文提交说明，至少包含本次变更的背景、核心改动、验证命令和已知剩余风险；不要只写一句泛泛的 `update` 或 `fix`。

## 2026-08-04 Fork 发布路径

- Fork 成独立项目并准备推送到新远程仓库时，不能只改 git remote 和 README；Go 根模块路径、内部 import、构建 `ldflags`、CI workflow、示例命令和子模块 `go.mod` 也要同步迁移，否则 `go test` 会继续输出旧上游包路径，Release 二进制也会把旧路径作为版本变量注入目标。
- 这类迁移要加仓库级回归测试，扫描核心源码、构建脚本和文档中的旧根 module path，防止后续合并上游代码时把旧路径带回来。

## 2026-08-04 Web 构建性能

- 在 WSL2 的 `/mnt/f` 这类 Windows 挂载盘上跑 `vue-tsc --noEmit` 会非常慢；本项目实测同一份 Web 类型检查在 WSL `/mnt/f` 约 204 秒，Windows 原生 Node 约 10 秒。不要把慢路径误判为 Vite 卡死。
- Web 脚本应拆分“快速打包”和“完整发布校验”：日常 `npm run build` 只生成 `dist`，发布/CI 使用 `npm run build:check` 执行 `typecheck + build`，既保留质量门槛，也避免本地反复构建被类型检查拖慢。

## 2026-08-04 QMI Proxy 回退

- WSL2 便携部署不应假设系统安装了 libqmi 的 `/usr/libexec/qmi-proxy`。如果 QMI 控制口瞬时被探测流程占用，自动选择 `qmi-proxy` 但关闭 raw fallback，会把“缺少外部代理程序”升级成设备启动失败。
- 区分“用户显式启用 qmi-proxy”和“程序自动选择 qmi-proxy”：显式模式保持严格，自动模式必须允许 `ProxyFallbackToRaw`，让无外部依赖的 WSL2 包能恢复到 raw QMI。
- `AddWorkerFromConfig` 这类单设备启动流程不能把“是否需要 AT 兼容身份发现”建立在 `config.ListDevices()` 的全局状态上；全局配置会被其他测试或其他设备污染，导致完整托管 QMI 重绑被无关设备短路。
- 大疆/Baiwang `QDC507` 在 WSL2 中可以通过 AT 口稳定读取 `ATI`、`AT+QCFG="usbnet"`、`CPIN/CEREG/QNWINFO`，即蜂窝注册和 AT 控制面可以是好的；这不等于 `/dev/cdc-wdm0` raw QMI 控制面也可用。
- WSL USB/IP 下不要用 shell `cat < /dev/ttyUSB*` 这类方式读串口做诊断，打开/读取可能卡住并留下进程；优先通过项目的临时 AT API 或 Go 层带硬超时的串口探测。
- 如果 WSL2 下 `qmi_wwan`、`cdc-wdm0`、`wwan0` 都存在但 QMI Core 长期 `context deadline exceeded`，应把它当作 USB/IP QMI 控制传输问题单独处理；候选路线包括打包可控的 qmi-proxy/libqmi 侧车、切换 ECM/MBIM 模式，或用 VirtualBox USB 直通验证是否为 WSL USB/IP 限制。
- 官方 `qmicli` 走 `--device-open-proxy` 仍在 `CID allocation failed in the CTL client: Transaction timed out` 失败时，可以排除“只是 VoHive QMI 实现不兼容”或“只是缺少 qmi-proxy”的单点假设；要继续看 USB/IP、模组 USB 组态或真实 USB 直通。
- Ubuntu 24.04 的 `libqmi-proxy` 会把 `qmi-proxy` 安装到 `/usr/libexec/qmi-proxy`，和 VoHive 当前探测路径一致；打包侧车时这个路径可以作为兼容目标，但它不能修复 WSL2 USB/IP 本身丢 URB 的问题。
- 对 DJI/Baiwang `QDC507`，`AT+QCFG="usbnet"` 与 `AT+QCFG="usbnet"?` 都能查询当前模式；`AT+QCFG=?` 在当前固件上只返回 `>`，不要把它作为安全的能力范围探测命令。
- 当前 `usbnet=0` 下，该模块在 WSL2 中仍是 vendor-specific 接口加 `option/qmi_wwan` 绑定，不是 ECM/MBIM 枚举；验证 ECM/MBIM 必须写入模式并重启模组，执行前要有回滚步骤和可重新 attach 的桌面流程。
- `AT+QCFG="usbnet",1` 后 DJI/Baiwang `QDC507` 会枚举出 CDC ECM 形态：控制接口 `class=02 sub=06`，数据接口 `class=0a`；但如果此前给 `option` 写过 `2ca3:4006 new_id`，`option` 会先抢占 ECM 的 `1.4/1.5`，需要只释放这两个接口并绑定 `cdc_ether`，才能生成 `enx*` ECM 网卡。
- ECM 模式下 AT 口仍可存在；本机实测 `/dev/ttyUSB2` 和 `/dev/ttyUSB3` 都能响应 `ATI`。VoHive worker 因旧 `wwan0/cdc-wdm0` 消失进入 `usb_wait` 时，应使用带硬超时的临时串口工具回滚，不要依赖已不可用的 API worker。
- ECM DHCP 成功不等于公网出网成功。本机实测 `udhcpc` 可从模组拿到 `192.168.225.30/24`，网关 `192.168.225.1` 可 ping；但 `curl --interface` 和公网 ping 仍超时，同时 AT 侧 PDP context 已有运营商地址。这说明 ECM 正式化还需要单独处理数据连接控制、APN/拨号状态或模组 NAT 出口行为。
- 该固件上 `AT+QNETDEVCTL=1,1` 和 `AT+QNETDEVCTL=1,1,1` 都返回 `ERROR`，不能作为 DJI/Baiwang `QDC507` 的 ECM 出网启动命令。
- WSL USB prepare 支持 ECM 时，不能简单把 Baiwang `1.4` 固定绑到 `qmi_wwan`。应先读 `bInterfaceClass/bInterfaceSubClass`：`class=02 sub=06` 加 `1.5 class=0a` 走 `cdc_ether`，vendor-specific 才走 `qmi_wwan`。
- 给 WSL prepare 增加 ECM 分支后，QMI ready 判定不要额外收紧到必须读到 `driver_name=qmi_wwan`；测试 sysfs 和部分内核异步状态可能已有 `/dev/cdc-wdm*`/`wwan*`，但 driver 文件未及时反映，旧 QMI 行为应保持“节点齐全即 prepared”。
- 后端重启或 USB 模式切换后，DJI/Baiwang 模组可能停在 `COPS: 0`、`No Service`、`CREG/CEREG/CGREG=0/3`；如果 `CFUN=1`、`CPIN=READY`、漫游允许且 USB 模式正确，可先发 `AT+COPS=0` 重新触发自动选网，再轮询 1 至 2 分钟。若仍无服务，可用 `AT+CFUN=1,1` 完整重启模组并重新 attach/prepare/rescan；自动选网可能选择 `46000` 而不是基线时的 `46001`，只要 `CREG/CEREG/CGREG=5` 和 `CGATT=1` 即可认为蜂窝注册已恢复。
- 从 ECM 回滚到 `usbnet=0` 后，USB 拓扑可以恢复，但蜂窝注册可能要等约 2 分钟才从 `No Service`/`CREG=3` 回到 `CREG/CEREG/CGREG=5`；不要在刚重枚举后的短暂窗口误判回滚失败。
- VirtualBox USB 直通验证不能只看项目代码是否支持；Windows 侧必须先有 `VBoxManage`、VirtualBox 驱动/服务和可用 USB filter。未安装 VirtualBox 时，应记录为前置条件，不要在调试流程里自动安装整套虚拟化软件。
- 添加设备弹窗里的“自动填充发现路径”和“用户选择后端”要分清阶段：选择硬件时可以给默认值，保存时只能刷新路径/IMEI，不能再次按发现结果重算并覆盖用户选择，否则用户选 AT 会被悄悄保存成 QMI。
- 对 DJI/Baiwang `2ca3:4006` 这种在 WSL2 USB/IP 下 raw QMI 控制面超时、但 AT 口稳定可用的设备，发现到 AT 口时添加设备应默认 AT 后端；QMI 应作为后续数据面专项，而不是默认路径。
- 单测不要硬编码真实 Linux 设备节点名（例如 `/dev/cdc-wdm0`、`/dev/ttyUSB2`）来制造“初始化失败”；当实机正好接入时测试会误打开真实硬件并变成环境相关。应使用明显不存在的 `/dev/vohive-test-*` 路径。

## 2026-08-04 URC 日志降噪

- 桌面/Web 日志重复刷屏时，先确认是前端重复渲染还是后端真实重复输出；本次 `QSIMSTAT/CPIN/CREG/Modem RDY` 是后端每次收到同值 URC 都写 INFO，不应在前端用隐藏行来掩盖根因。
- URC 降噪只能压日志和重复状态触发，不能阻断业务分发。`+CMTI` 新短信、`+CUSD` USSD、来电/挂断等事件即使内容相同也要继续按事件处理。
- `+CPIN: READY` 在本项目里同时承担“SIM 状态”和“RDY 兜底信号”两个含义；去重时要按“进入 READY”触发 RDY，而不是每次 READY 都广播，否则设备池会被周期性 READY 唤醒并反复写 `[事件驱动] Modem RDY`。
- 注册状态 URC 可能是 `+CREG: 0,5` 查询式字段，也可能是 `+CREG: 5` 单字段上报；做状态签名前必须先把 `stat` 解析正确，否则变化日志会被误压掉。

## 2026-08-04 桌面内置后端资源

- `dist/`、`desktop/src-tauri/target/` 和 `desktop/src-tauri/resources/vohive/vohive-open_linux_amd64` 都是构建/运行阶段的大二进制副本，不应长期提交到 Git；Git 只保留源码、小型配置资源和同步脚本。
- Tauri 的 `resources/` 目录是打包输入，不等于必须由 Git 跟踪。CI 可以在构建桌面前把后端 artifact 复制进去，本地也可以由同步脚本从 `dist/` 复制进去。
- 从 Git 中移除已跟踪的大二进制时，用 `git rm --cached` 保留本地文件，再加 `.gitignore`；这解决“以后不跟踪”和“远程当前分支删除文件”。如果要清掉历史 commit 中的大文件对象，需要单独走历史重写和 force-push 流程，不能混在普通修复提交里。
- 如果用户确认要连历史一起清掉，必须先检查未推送提交中是否还有“大二进制先修改、后删除”的序列；这种序列直接推送仍会把新 blob 带到远程历史，应先重写本地可达历史，确认 `git rev-list --objects --all` 已查不到目标路径，再 force push 目标分支和相关 tag。

## 2026-08-04 GitHub 网络代理

- 当 `git push`、`git ls-remote` 等 GitHub HTTPS 操作报 `Connection was reset`、`Could not connect to server` 或 443 端口连接失败时，先用本机 HTTP 代理 `http://127.0.0.1:10808` 做一次性重试，例如 `git -c http.proxy=http://127.0.0.1:10808 -c https.proxy=http://127.0.0.1:10808 push origin main`。
- 优先使用命令级 `-c http.proxy=... -c https.proxy=...`，不要直接改全局 Git 代理配置；这样不会影响局域网、WSL、包管理器或其他仓库的网络行为。

## 2026-08-04 Fork 后的 CI 发布边界

- Fork 成独立项目后，不能保留旧上游的容器发布 workflow 和旧镜像名；如果当前项目不发布容器镜像，就删除容器发布/构建 workflow，而不是要求用户去补旧仓库的容器仓库密钥。
- 对 GitHub Actions 报 `Username and password required` 这类登录失败，先检查 workflow 是否仍在引用旧容器仓库账号、密钥、旧镜像名或登录 action，不要直接把“补账号密码”当成默认修复。

## 2026-08-04 桌面便携包外部依赖说明

- `usbipd-win` 是系统级 USB/IP 驱动和 Windows 服务，不应随项目便携包私自内置或分发过期安装包；README 应提供官方项目和 Release 链接，让用户从官方渠道安装。
- Tauri 便携 exe 仍依赖 Windows WebView2 Runtime。即使大多数 Windows 10/11 已内置，也应在普通用户环境要求里说明精简系统需要手动安装。
- 首次安装 WSL 发行版后需要先完成 Linux 用户初始化；仅执行 `wsl --install` 不代表发行版已可被桌面壳直接使用。
