# Lessons

## 2026-08-03 WSL2 VoHive 路线

- WSL2 中没有 Go/Node 且 sudo 需要密码时，不要改用户发行版系统包；优先使用项目内 `.toolchains` 放置便携 Go/Node 构建工具链。
- 仓库内 `.toolchains` 里的 Go/Node 才算这条项目线的有效工具链；当前 shell 的 `PATH` 可能是空的，必须显式调用 `.toolchains/go/bin/go`、`.toolchains/go/bin/gofmt` 和 `.toolchains/node/bin/node`，不要拿系统环境是否装过来判断能不能构建。
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

## 2026-08-05 漫游语义与版本辨识

- “漫游注册”和“数据漫游”不能混为一谈。`RegStatus=5` 表示模块已在漫游网络驻网，普通用户的“数据漫游”开关应控制数据连接/代理出站，不应阻止驻网或收短信。
- `AT+QCFG="roamservice"` 是模组侧注册漫游服务控制，语义接近“是否允许注册漫游网络”，不等同于手机设置里的“数据漫游”；普通卡策略不要自动下发它，只保留为高级 AT 能力。
- 切卡期间 `TargetICCID` 不等于已确认当前卡。写入卡策略、展示信号/运营商/漫游状态时必须以 confirmed ICCID 或身份确认状态为准，避免切卡失败后仍显示旧卡运行态。
- 切卡旧运行态不只会出现在设备管理页；dashboard、lite、stream、status detail 等所有展示运行态的 API 都要一起检查，否则用户仍会在首页看到旧信号、旧运营商或旧漫游状态。
- live 策略开关一旦包含硬件动作，就必须按事务思路处理：先保存意图，硬件动作失败时回滚策略和 worker 展示态，并把真实错误返回给 UI，不能让数据库/UI 与模块实际状态分裂。
- “数据漫游关闭”不能只拦截用户点击启动数据网络的瞬间；设备可能在数据面已连接后才进入 `RegStatus=5`，运行态刷新、serving system 更新、健康同步路径都要触发断开守卫。
- 桌面壳不能只看自己持有的 child 句柄判断后端进程。从 Release 版接管已有 WSL 后端时，要探测 WSL 内 `/opt/vohive/bin/vohive` 进程，并用 `/ping` 健康检查作为兜底。
- WSL 状态刷新里的“进 WSL 探测进程”本身可能启动已停止的发行版。任何 `wsl.exe -d ... --exec ...` 调用前都要先用 `wsl -l -v` 确认发行版 Running，尤其是 `停止 WSL` 后返回状态时。
- 不只 `连接 USB 到 WSL` 会隐式启动 WSL；`准备 WSL USB`、`停止后端`、状态探测等任何 `wsl -d <distro> --exec` 调用都可能在发行版 stopped 时反向启动 WSL，必须先 preflight。
- 桌面壳 `/ping` 健康检查不能只找响应文本里的 `200`；要解析 HTTP 状态码并确认 body 是 VoHive 的 `pong` 响应，否则其它本机服务占用 7575 会让后端状态和启动按钮误判。
- `roaming_enabled` 是卡策略字段，不是设备配置字段；OpenAPI、Go DTO、前端类型三者必须同步，否则生成客户端或文档用户会误以为可以在设备配置层修改数据漫游。
- Release zip 版本、Tauri app version、Rust crate version、窗口标题、README 和 release notes 必须同步，避免同一个版本号对应不同内容，导致用户无法判断当前打开的是哪个软件。
- Release workflow 戳 Rust `Cargo.toml` 版本号时不要用全局正则替换所有 `version =`；应只在 `[package]` 段内替换当前 crate 版本，避免误改 dependency、workspace 或其它表。
- 当前 Linux 后端运行体没有 `--version` 参数；构建后验证版本注入应通过运行中的版本 API、Release 资产名、ldflags 记录或二进制字符串检查，不要把 `--version` 失败误判成编译失败。
- 后端 `version = "Unknown"` 不是可展示的真实版本，前端必须把它和空值一起归一化到构建版本；只判断“有字符串就显示”会把兜底值错误暴露给用户。
- 版本显示修复必须同时验证源码、构建产物和 WSL 运行体：源码改了不代表当前浏览器里看到的是新版本，`/api/system/info` 和 `/ping` 都要复核。

## 2026-08-06 WSL USB qmi_wwan 动态 ID

- DJI/Baiwang `2ca3:4006` 在 WSL2 中准备 QMI 拓扑时，`option1/new_id` 只解决串口接口接管问题；`qmi_wwan/bind` 是否接受 `1-1:1.4` 还取决于 `/sys/bus/usb/drivers/qmi_wwan/new_id` 是否登记同一个 VID/PID。
- 遇到 `qmi_wwan/bind: no such device` 时，不能先把它归为内核异步竞态。要检查 `qmi_wwan/new_id`、interface 当前 driver、`usbmisc/cdc-wdm*` 和 `net/wwan*` 是否一致，再决定是补动态 ID、重试还是走 ECM 分支。
- ECM 与 QMI 的 USB prepare 分支必须保持互斥：vendor-specific 接口才登记并绑定 `qmi_wwan`，`class=02/sub=06` 的 ECM 控制接口应走 `cdc_ether`，否则会把用户切到 ECM 后的拓扑再次破坏。

## 2026-08-06 本机号码识别

- 很多 SIM/eSIM 不会把 MSISDN 写入卡内资料。`AT+CNUM` 为空、EF_MSISDN (`6F40`) 全 `FF` 时，程序没有可靠来源推断“本机号码”，只能显示为空或让用户手动录入。
- 自动学习来源要分层保存，不能把用户手动确认的号码伪装成 VoWiFi 或 modem 读取值；最终展示优先级应是 `manual > vowifi > modem`，清除手动号码后再回退到自动来源。
- 手动本机号码必须绑定当前已确认的 IMSI/ICCID；eSIM 切卡身份未确认时应拒绝写入，避免把号码写到旧卡。
- 从 PowerShell 调 WSL 构建时不要在 Bash 片段里依赖 `$(date ...)` 这类嵌套展开；本地构建版本信息可由 PowerShell 先生成 UTC 时间，再作为普通字符串传入 Go `-ldflags`，避免 `BuildTime` 被注入为空。
- VoWiFi 启动失败后的网络恢复意图不能只读会被 UI/策略同步改写的 `NetworkEnabled`；要在断网前单独捕获恢复意图，并且后续准备步骤只能保留或增强这个意图，不能再把它覆盖回 `false`。

## 2026-08-06 Release notes 版本差异核对

- 发布说明不能凭正在处理的最后一个问题或记忆手写；每次发布前必须用 `git log --oneline v上一版本..v当前版本` 核对完整提交列表，并逐项确认用户可见变更是否进入 `.github/release-notes/v当前版本.md`。
- 如果同一版本内包含多个阶段提交，Release notes 开头和“更新内容”都要覆盖完整 tag range；不能只描述最后一个提交，否则 GitHub Release 会漏掉已经发布的修复。

## 2026-08-06 VoWiFi MNC 归一化

- MNC 是有长度语义的标识，`00`、`000`、`01`、`001` 都不能用普通整数或 `TrimLeft("0")` 处理；全零 MNC 不是空值。
- VoWiFi ePDG 域名派生必须优先使用实时 SIM 归属 MCC/MNC；只有 MNC 完全缺失时才从 IMSI 回退推导，否则 `454/00` 这类二位 MNC 会被 IMSI 前三位误判成 `454/003`。
- 遇到 SWU 连接 `127.0.0.1:4500` 超时时，先查日志里的 `epdg` 和 DNS 解析结果，不要只看 UDP 端口或本机监听状态。

## 2026-08-27 部分 ISIM 与短信中心号

- ISIM 只读到部分字段时，不应把“卡不完整”直接升级成启动失败；应优先和 profile 合并，缺失项交给后续注册层按 IMSI/域名规则补齐。
- AT 模式发送短信时，`AT+CMGS` 依赖当前 SMSC 状态；如果模组里 `AT+CSCA` 没有被刷新，`CMS ERROR: 350` 往往更像中心号/承载状态问题，而不是 PDU 长度本身有错。
- WSL 后端配置文件如果是 `600 root:root`，桌面壳和手工重启都必须按 `wsl.exe -u root ...` 启动；用普通用户起后端会直接因为读不到 `/opt/vohive/config/config.yaml` 而失败。
- 发送短信前先读出当前 `AT+CSCA?` 再回写一遍 `AT+CSCA=...`，可以把“中心号未刷新”与真正的承载/资费问题分开，别把 `CMS ERROR: 350` 全都归到 PDU 编码上。

## 2026-08-28 审查问题复核与长命令处理

- 代码审查结论必须二次验证，不能把“建议”直接当事实；修复前要补能失败的回归测试，确认失败原因是目标行为而不是测试写错。
- ePDG 地址带显式端口时，DNS/IP 候选重试必须保留端口；`host:port` 不能在 `LookupIPAddr` 或候选规范化后退化成默认 `4500`。
- partial ISIM 要分清字段来源：读到 IMPI/IMPU 时可以优先 ISIM；只有 Domain 时，IMPI/IMPU 仍来自 profile，日志和 AKA 偏好不能标成完整 ISIM。
- SMSC 刷新不能假设模块一定能从 `AT+CSCA?` 返回中心号；发送短信和 VoWiFi 启动画像都应支持配置兜底，且模块值优先于配置值。
- 前端发布版本号不能在组件里硬编码；应从构建元数据注入，接口返回空值或 `Unknown` 时再归一化到构建版本。
- 长时间无输出的验证命令必须提前设置合理超时并持续汇报；如果超过预期仍无输出，要明确中止或切换验证方式，不能让用户看起来像任务卡死。

## 2026-08-28 Release 触发规则

- 用户要求“推送 `main` 即发布”时，不能只更新版本号并 `git push origin main`；Release workflow 必须显式监听 `push.branches: main`，并且发布 job 条件也要允许 `refs/heads/main`。
- `main` 推送触发发布时，版本号应从仓库版本元数据读取，例如 `desktop/package.json`，不要只依赖 workflow_dispatch 的输入默认值或手工 tag 名。
- 发布流程文档必须和 CI 行为一致；如果 README 仍写“创建 tag 并推送”才发布，用户会看到 Actions 成功但 Release 停在旧版本。

## 2026-08-28 USB 模组识别范围

- WSL `prepare-usb` 不能只写死当前截图里的单个 PID；Quectel 模组应按厂商 ID `2c7c:*` 支持，并按实际枚举到的 VID/PID 写入 `option1/new_id` 与 `qmi_wwan/new_id`。
- `2ca3:4006` 是 DJI/Baiwang 伪装后的精确 ID，需要保留；桌面 `usbipd list` 还可用设备名包含 `Baiwang` 或 `Quectel` 做辅助识别。
- 不要把 `05c6:*` 这类 Qualcomm 厂商 ID 无条件放进 prepare-usb 支持范围；里面可能包含下载/诊断模式，必须先结合运行拓扑或实机证据再扩展。
