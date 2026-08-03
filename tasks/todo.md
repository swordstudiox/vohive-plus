# VoHive Windows 桌面化双运行路线计划

## 当前决策

- 源码基线：采用 `windloom/vohive-open`，不再以 `openvohive/openvohive` 作为主线。
- 运行路线：同时推进两条正式路线。
  - 路线 A：WSL2 运行路线。不是临时验证，而是可交付运行方式之一，优先利用当前机器已有 WSL2 快速跑通。
  - 路线 B：VirtualBox Headless + 最小 Debian。作为更可控、可封装的 VM 运行路线。
- 安装目标：构建阶段允许联网；用户安装/运行阶段不应再从 GitHub 拉源码或二进制。
- 桌面目标：Windows 桌面程序统一管理后端启动、停止、日志、Web UI 打开和运行环境诊断。

## 可选项

- WSL2 路线可选项：
  - 先使用用户现有 WSL2 发行版跑通。
  - 后续导入专用 `vohive-wsl` rootfs，降低对用户现有环境的污染。
- VirtualBox 路线可选项：
  - 先手工创建最小 Debian VM 验证 USB 直通。
  - 后续制作预置 OVA，由安装器自动导入。
- 桌面壳可选项：
  - 轻量优先：Tauri + WebView2。
  - 开发速度优先：Electron。
  - 原生优先：C# WPF/WinUI。

## 阶段 1：源码基线落地

- [x] 确认本项目目录中是否已有 `windloom/vohive-open` 源码。
- [x] 如果没有，将 `windloom/vohive-open` 作为主源码引入并提升到项目根目录。
- [x] 保留当前 `vohive-release` 安装脚本仓库内容作为参考，不把它当作主程序源码。
- [x] 记录 `windloom/vohive-open` 当前 commit、许可证说明和第三方依赖来源。
- [x] 明确不使用 `1239t/vohive` 的在线自更新逻辑。

## 阶段 2：WSL2 正式运行路线

- [x] 检查 Windows 侧 `wsl.exe`、目标发行版、WSL systemd 状态。
- [x] 检查或安装 `usbipd-win`，用于把 DJI 4G 模组挂载到 WSL2。
- [x] 编写 WSL2 手工验证步骤：
  - `usbipd list`
  - `usbipd bind --busid <BUSID>`
  - `usbipd attach --wsl --busid <BUSID>`
  - WSL 内检查 `lsusb`、`dmesg`、`/dev/cdc-wdm*`、`/dev/ttyUSB*`、`ip link`
- [x] 在 WSL2 内构建 `windloom/vohive-open`：
  - 前端：`npm ci --prefix web && npm run build --prefix web`
  - 复制前端产物到 `internal/web/dist`
  - 后端：`CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -buildvcs=false -tags "with_utls nomsgpack" -o dist/vohive ./cmd/vohive`
- [x] 在 WSL2 内准备运行目录：
  - `/opt/vohive/bin`
  - `/opt/vohive/config`
  - `/opt/vohive/data`
  - `/opt/vohive/logs`
- [x] 使用本地构建产物启动 `vohive`，不使用在线安装脚本下载 release。
- [ ] Windows 浏览器访问 `http://127.0.0.1:7575` 并验证登录、设备发现、日志。
- [ ] 验证断开重插 DJI 4G 模组后的恢复流程。
- [x] 形成 WSL2 路线的自动化脚本设计：
  - Windows 侧 attach USB。
  - WSL 侧启动/停止 `vohive`。
  - Windows 侧读取日志和健康检查。

## 阶段 2A：WSL2 USB 自动识别专项

### 根因

- [x] Windows 侧已识别 DJI 4G 模组：`2ca3:4006 Baiwang`，说明数据线可用。
- [x] `usbipd-win` 已能 `bind` 和 `attach --wsl`，说明 Windows 到 WSL2 的 USB/IP 通路成立。
- [x] WSL2 内 `2ca3:4006` 设备 5 个 interface 均为 vendor-specific，Linux 内核不会默认生成 VoHive 需要的 `/dev/cdc-wdm*`、`/dev/ttyUSB*`、`wwan*`。
- [x] 手工执行 `option new_id`，再把 `*:1.4` 从 `option` 切给 `qmi_wwan` 后，VoHive 可以发现 `control_path=/dev/cdc-wdm0`、`net_interface=wwan0`、`at_port=/dev/ttyUSB2`。

### 可选方案

- 推荐方案 A：WSL/Linux 环境准备层自动绑定驱动。
  - Windows 桌面壳或启动脚本负责 `usbipd list/bind/attach`。
  - WSL 内独立脚本负责 `modprobe`、`option new_id`、接口 unbind/bind 到 `qmi_wwan`。
  - VoHive 核心发现逻辑保持纯静态扫描，只读 sysfs，不写 sysfs。
- 备选方案 B：把驱动绑定放进 VoHive 后端启动和重扫链路。
  - 优点是用户点“重扫”时也能自动修复。
  - 缺点是 Go 后端要写 `/sys/bus/usb/drivers/*`，需要 root 权限，且会污染现有“发现函数不碰硬件”的边界。
- 备选方案 C：两层结合。
  - WSL adapter 做主绑定。
  - VoHive 后端仅返回诊断提示，例如“检测到 2ca3:4006 但缺少 qmi_wwan/cdc-wdm，请运行 USB 准备步骤”。

### 推荐设计

- [x] 新增 WSL USB 准备脚本，路径为 `packaging/wsl/vohive-usb-prepare.sh`。
- [x] 脚本只处理已验证的 `2ca3:4006 Baiwang`，不做泛化驱动绑定。
- [x] 脚本以 root 在 WSL 内运行，执行：
  - 检测 `/sys/bus/usb/devices/*/idVendor` 和 `idProduct` 是否存在 `2ca3:4006`。
  - 加载 `usbserial`、`option`、`qmi_wwan`、`cdc_wdm`。
  - 确保 `2ca3 4006` 写入 `/sys/bus/usb-serial/drivers/option1/new_id` 或等效 option driver `new_id`。
  - 等待 `/dev/ttyUSB*` 出现。
  - 找到目标 USB 设备的 `*:1.4` interface；如果它已绑定到 `option`，先 unbind，再 bind 到 `qmi_wwan`。
  - 等待 `/dev/cdc-wdm*`、`wwan*` 出现。
  - 输出结构化 JSON；未找到设备不作为进程错误，权限不足、驱动缺失、绑定失败返回非 0。
- [ ] Windows 启动脚本/桌面壳后续调用顺序：
  - `usbipd list` 找 `2ca3:4006`。
  - 未 Shared 时执行 `usbipd bind --busid <BUSID>`。
  - WSL 保持 Running 后执行 `usbipd attach --wsl --busid <BUSID>`。
  - `wsl.exe -d Ubuntu-24.04 -u root --exec /opt/vohive/bin/vohive-usb-prepare.sh`。
  - 启动或重扫 VoHive。
- [x] Web `添加设备`入口作为交互式探测入口：
  - 点击 `添加设备` 后先触发 WSL USB 准备步骤。
  - USB 准备完成后再调用现有 `/api/devices/discovered`。
  - 弹窗展示发现到的设备、退化状态和可操作提示。
  - 如果 Windows 侧 USB 还没有 attach 到 WSL，提示用户需要桌面壳/管理员动作处理，Web 后端不假装能直接接管 Windows USB。
- [ ] 保留启动/重扫/热插拔入口：
  - 桌面壳启动时执行一次 USB 准备，保证已配置设备能自动恢复。
  - `重新扫描` 时也可复用同一准备步骤。
  - udev 热插拔事件触发重扫前可复用同一准备步骤，避免必须手动点 `添加设备`。
- [ ] 后端可选增强：在 `/api/devices/discovered` 无设备时，如果 sysfs 存在 `2ca3:4006` 但缺少 QMI/TTY 节点，返回诊断字段而不是只返回空数组。

### 验证标准

- [x] 不手工执行 sysfs 命令，插入模块后由脚本自动生成 `/dev/cdc-wdm*`、`/dev/ttyUSB*`、`wwan*`。
- [x] 点击 Web `添加设备` 后能自动执行 WSL 侧 USB 准备并显示可添加设备。
- [x] `GET /api/devices/discovered` 能返回 DJI 模组。
- [ ] 断开重插后，重新 attach 并执行准备脚本能恢复发现。
- [x] 脚本重复执行是幂等的，不因为 `new_id` 已存在、接口已绑定而失败。
- [x] 不破坏现有 Quectel/Sierra/QMI/MBIM 静态发现测试。
- [x] `with_imei=1` 已在本机当前状态恢复成功；如后续再次超时，再作为 QMI/AT 稳定性单独任务处理。

## 阶段 2B：漫游开关正式功能

### 目标

- [x] 将“漫游开关”从 AT 模板提升为正式卡策略能力。
- [x] Web UI 使用“允许漫游”开关，默认开启；关闭时尝试禁止模块注册漫游网络。
- [x] 策略跟随 ICCID，而不是跟随设备硬件路径。
- [x] 在线当前卡切换时即时发送 AT 指令；离线卡或非当前 eSIM 卡切换时只保存策略，待卡激活/上线后投影生效。

### 推荐设计

- [x] `card_policies` 新增 `roaming_enabled` 字段。
  - 默认值为 `true`，避免升级后把已有卡静默变成“禁止漫游”。
  - 新增迁移逻辑：旧库首次添加列时把已有策略行初始化为 `true`。
- [x] API 层新增 `roaming_enabled` 字段和设备动作端点。
  - `GET /api/cards/:iccid/policy` 返回 `roaming_enabled`。
  - `PUT /api/cards/:iccid/policy` 接受 `roaming_enabled`。
  - `PATCH /api/devices/:device_id/roaming` 接受 `{ "enabled": true|false }`。
- [x] 设备动作端点语义：
  - `enabled=true`：发送 `AT+QCFG="roamservice",255,1`，恢复自动/允许漫游。
  - `enabled=false`：发送 `AT+QCFG="roamservice",1,1`，关闭漫游。
  - QMI/MBIM 后端使用临时 AT 会话，不依赖常驻 AT manager。
  - AT 后端优先使用已有 AT manager。
- [x] 前端策略面板新增“允许漫游”开关。
  - 主设备卡策略面板支持 live 切换。
  - eSIM 卡内联策略面板支持当前卡 live、非当前卡 stored。
  - 漫游开关不参与网络/VoWiFi/飞行互斥。
- [x] OpenAPI 文档同步字段和端点。

### 验证标准

- [x] DB 测试确认迁移列存在、默认策略 `roaming_enabled=true`。
- [x] API 测试确认 `PUT /cards/:iccid/policy` 可写入漫游策略，未传字段不会误覆盖。
- [x] API 测试确认 `PATCH /devices/:id/roaming` 会落库并选择正确 AT 指令。
- [x] 前端测试确认服务层暴露 `setRoaming`，卡策略 composable 支持独立漫游开关。
- [x] `go test ./internal/db ./internal/api ./internal/device -count=1` 通过。
- [x] `tsx --test tests/*.test.ts`、`vue-tsc --noEmit`、`npm run build --prefix web` 通过。

## 阶段 3：VirtualBox Headless + 最小 Debian 路线

- [ ] 手工创建最小 Debian VM，目标配置先按 1 核 CPU、512 MB 至 1 GB 内存、2 GB 至 4 GB 动态磁盘。
- [ ] 安装 VM 内基础包：`ca-certificates`、`usbutils`、`iproute2`、`systemd`、必要的调试工具。
- [ ] 将阶段 2 构建出的 Linux `vohive` 二进制和默认配置放入 VM。
- [ ] 配置 systemd 服务启动 `vohive`。
- [ ] 配置 NAT 端口转发：Windows `127.0.0.1:7575` 到 VM `7575`。
- [ ] 配置 VirtualBox USB filter，使 DJI 4G 模组自动直通到 VM。
- [ ] 用 `VBoxManage startvm <name> --type headless` 启动 VM。
- [ ] 验证 VM 内 `lsusb`、`/dev/cdc-wdm*`、`/dev/ttyUSB*`、`wwan*`。
- [ ] 验证 Windows 访问 `http://127.0.0.1:7575`。
- [ ] 固化为 OVA，并记录导入、启动、停止、USB filter 配置命令。

## 阶段 4：Windows 桌面壳阶段

### 阶段 4 目标

- [x] 交付一个 Windows 桌面入口，用户双击后默认使用 WSL2 路线启动 VoHive。
- [x] 桌面壳统一负责 Windows 侧能力：运行环境检测、USB 枚举、`usbipd bind/attach --wsl`、管理员权限引导、WSL 内 USB 准备脚本调用、后端启动/停止、健康检查、日志查看和打开 Web UI。
- [x] Web 页面继续保留“添加设备/重新扫描”的交互式探测入口，但不直接承担 Windows USB 接管；Windows 专属动作由桌面壳编排。
- [ ] 为 VirtualBox Headless + 最小 Debian 预留完整 runtime adapter 接口，阶段 4 首个可运行版本优先完成 WSL2。

### 技术路线可选项

- 推荐方案 A：Tauri + WebView2。
  - 优点：安装体积小，适合做“壳 + 本地进程编排 + 内嵌 Web UI”。
  - 缺点：需要 Rust/Tauri 构建链；构建阶段允许联网，可以接受。
- 备选方案 B：Electron。
  - 优点：开发快，Node 侧执行 Windows/WSL 命令方便。
  - 缺点：体积明显更大，不符合“轻量桌面壳”的主要目标。
- 备选方案 C：C# WPF/WinUI。
  - 优点：Windows 原生，进程和权限模型直接。
  - 缺点：前端体验和现有 Vue Web 管理台复用成本较高。

### 推荐实施边界

- [x] 阶段 4A：创建桌面壳工程骨架，默认采用 Tauri + WebView2。
  - 新目录建议：`desktop/`。
  - 桌面前端只做启动器/状态面板，不重写现有 VoHive Web 管理台。
  - 后端 Web UI 仍由 `vohive` 提供，桌面壳用 WebView 打开 `http://127.0.0.1:7575/`。
- [x] 阶段 4B：实现统一 runtime 接口。
  - `detect()`
  - `installOrImport()`
  - `attachUsb()`
  - `prepareUsbInGuest()`
  - `start()`
  - `stop()`
  - `status()`
  - `logs()`
  - `openWeb()`
- [x] 阶段 4C：实现 WSL2 runtime adapter MVP。
  - 检测 `wsl.exe` 和固定目标发行版；WSL 版本、systemd 深度诊断后续增强。
  - 检测 `usbipd-win` 是否安装，并优先使用 `C:\Program Files\usbipd-win\usbipd.exe`。
  - Windows 侧枚举 `2ca3:4006 Baiwang`，找到 busid。
  - 未 Shared 时执行或引导管理员执行 `usbipd bind --busid <BUSID>`。
  - WSL 保持 Running 后执行或引导管理员执行 `usbipd attach --wsl --busid <BUSID>`。
  - attach 成功后调用 WSL 内 `/opt/vohive/bin/vohive-usb-prepare.sh`。
  - 启动 `/opt/vohive/bin/vohive -c /opt/vohive/config/config.yaml`。
  - 通过 Windows 侧 `http://127.0.0.1:7575/` 做健康检查。
- [x] 阶段 4D：实现桌面 UI 最小闭环。
  - 运行路线当前固定为 WSL2；VirtualBox 路线选择后续支持。
  - 状态区：后端状态、Web 端口、WSL 发行版、USB 状态、最后一次准备结果。
  - 操作按钮：启动、停止、重新连接 USB、打开 Web UI、查看日志。
  - 基础诊断提示：缺少 WSL2、缺少 `usbipd-win`、未插入 DJI 模组、管理员命令失败时给出明确反馈。
- [x] 阶段 4E：日志与诊断。
  - 捕获 Windows 命令输出：`wsl.exe`、`usbipd.exe`。
  - 捕获后端 stdout/stderr。
  - 保留最近日志，桌面 UI 可复制诊断信息。
  - 对端口占用、WSL 未运行、USB 未 Shared、attach 失败、WSL 内驱动绑定失败分别给出明确状态。
- [ ] 阶段 4F：为 VirtualBox adapter 预留实现点。
  - 先定义接口和状态模型。
  - 不在首个阶段 4 MVP 中强行实现 VM 导入、USB filter、NAT 端口转发。
  - 阶段 3 实机验证完成后再接入真正的 VirtualBox adapter。

### 阶段 4 暂不做

- [x] 不重写 VoHive 后端或现有 Web 管理台。
- [x] 不把 WSL2 当临时验证；WSL2 是阶段 4 MVP 的正式运行路线。
- [x] 不在安装/运行阶段从 GitHub 下载源码或二进制。
- [x] 不把 VirtualBox adapter 做成首个阻塞项，避免拖慢当前已有 WSL2 路线交付。
- [x] 不在 Web 前端里直接执行 Windows 管理员 USB 操作。

### 阶段 4 验证标准

- [ ] 从 Windows 双击桌面程序后，可以一键启动 WSL2 内的 VoHive。
- [x] 未插入 DJI 模组时，桌面壳能明确提示“未发现 2ca3:4006 Baiwang”。
- [x] 插入 DJI 模组后，桌面壳能枚举 busid，并完成或引导完成 `usbipd bind/attach --wsl`。
- [x] WSL 内 USB 准备脚本返回成功后，Web `添加设备`能发现 `/dev/cdc-wdm*`、`wwan*`、`/dev/ttyUSB*`。
- [x] 后端异常退出时，桌面壳能显示失败原因并允许重新启动。
- [x] 端口 `7575` 被占用时，桌面壳能给出明确诊断，不静默失败。

## 阶段 5：打包与离线安装

- [ ] 构建阶段联网拉取 Go/npm 依赖。
- [ ] 安装包内置已编译的 `vohive` 二进制、默认配置、启动脚本。
- [ ] WSL2 路线安装包不在安装时下载 `vohive`。
- [ ] VirtualBox 路线安装包不在安装时下载 `vohive`。
- [ ] 明确 VirtualBox 本体是否内置、提示用户安装，或作为外部前置依赖。
- [ ] 输出标准安装包和便携包策略：
  - 标准安装包：适合首次安装。
  - 便携包：适合已有 WSL2 或已有 VirtualBox 的用户。

## 验证清单

- [ ] WSL2 路线：从 Windows 双击启动后能进入 Web UI。
- [ ] WSL2 路线：DJI 4G 模组能被 Linux 识别并被 `vohive` 发现。
- [ ] VirtualBox 路线：headless VM 能自动启动并加载 USB 设备。
- [ ] VirtualBox 路线：Windows 能访问 VM 内 `vohive` Web UI。
- [ ] 两条路线：安装/运行阶段不从 GitHub 下载 `vohive` 二进制。
- [ ] 两条路线：断开重插 USB 后有明确恢复流程。
- [ ] 两条路线：日志能从 Windows 桌面程序查看。

## 边缘情况

- WSL2 发行版未启用 systemd。
- WSL2 内核或 usbipd 版本不支持当前 USB 设备。
- DJI 4G 模组在 Windows 侧被驱动占用，无法 attach 到 WSL2 或 VM。
- VirtualBox USB 直通需要 Extension Pack 或用户授权。
- Windows 防火墙拦截本地端口访问。
- 端口 `7575` 已被占用。
- 用户机器未开启虚拟化支持。
- Hyper-V 与 VirtualBox 在部分机器上存在性能或兼容性差异。
- `windloom/vohive-open` 的 VoWiFi 相关 AGPL 许可证义务影响后续分发策略。

## 评审记录

- 2026-08-03 阶段 1 已执行：本目录原先只有 `tasks/`，未发现上游源码；已克隆 `windloom/vohive-open` 到 `upstream/vohive-open`，当前 commit 为 `de689a554d1b86b97dcc71140bfbee250eff1d4e`。
- 2026-08-03 追加迁移：用户确认后，已将 `upstream/vohive-open` 中的源码和 `.git` 提升到项目根目录 `F:\mySoftwareTools\vohive-plus`，并删除空的 `upstream/`。当前根目录即为 git 工作区；`.toolchains/` 与 `downloads/` 已加入 `.gitignore`，`tasks/` 暂时保留为未跟踪的任务记录目录。
- 2026-08-03 阶段 1 基线确认：`go.mod` 使用本地 `replace` 指向 `third_party/netlink`、`third_party/qqbot`、`third_party/quectel-qmi-go`、`third_party/vowifi-go`；`THIRD_PARTY_NOTICES.md` 记录根项目 PolyForm Noncommercial 1.0.0、`vowifi-go` AGPL-3.0 等许可证来源；`internal/updater/updater.go` 已禁用应用内二进制自更新。
- 2026-08-03 阶段 2 环境确认：Windows 可用 `wsl.exe`；目标发行版为 `Ubuntu-24.04`，WSL version 2，系统为 Ubuntu 24.04.4 LTS，PID 1 为 `systemd`。
- 2026-08-03 阶段 2 构建确认：由于 WSL 内无 Go/Node 且 sudo 需密码，已将便携构建工具链放入 `.toolchains/`；Go `1.26.4 linux/amd64`、Node `v20.20.2`、npm `10.8.2`。前端 `npm ci --prefix web` 与 `npm run build --prefix web` 通过；后端产物迁移后位于 `dist/vohive-open_linux_amd64`，约 65 MB，静态 ELF，可执行。
- 2026-08-03 阶段 2 运行确认：已复制二进制和默认配置到 WSL `/opt/vohive`；用隐藏 `wsl.exe` 直接运行 `/opt/vohive/bin/vohive -c /opt/vohive/config/config.yaml` 后，WSL 内监听 `*:7575`，Windows 侧 `http://127.0.0.1:7575/` 返回 HTTP 200。默认登录为 `admin/admin`，仍需用户在浏览器中完成一次登录确认。
- 2026-08-03 阶段 2 硬件阻塞：`usbipd-win` 未预装；已下载官方 `usbipd-win_5.3.0_x64.msi` 到 `downloads/`，但静默安装失败，MSI 日志显示 `Error 1925`/`1603`，原因是当前进程没有管理员权限。安装完成前无法执行 `usbipd list/bind/attach`，也无法验证 DJI 4G 模组、`/dev/cdc-wdm*`、`/dev/ttyUSB*` 和断开重插恢复。
- 2026-08-03 追加确认：用户手动安装 `usbipd-win` 后，Windows 服务 `usbipd` 已运行，安装位置为 `C:\Program Files\usbipd-win\`，版本为 `5.3.0`。当前 `usbipd list` 仅枚举到摄像头 `2-7` 和 Intel 蓝牙 `2-10`，未发现 DJI/Quectel/4G 模组设备，因此 USB attach、Linux 设备节点和断开重插恢复仍未验证。
- 2026-08-03 追加硬件验证：用户插入大疆模块后，`usbipd list` 新增 `2-1  2ca3:4006  Baiwang`，说明当前 USB 线具备数据能力。管理员执行 `usbipd bind --busid 2-1` 后设备状态为 `Shared`；保持 `Ubuntu-24.04` 运行后，管理员执行 `usbipd attach --wsl --busid 2-1`，设备状态为 `Attached`。
- 2026-08-03 WSL 驱动验证：WSL sysfs 识别到 `VID:PID=2ca3:4006`、`PRODUCT=Baiwang`、`MANUFACTURER=BAIWANG`，5 个接口均为 vendor-specific。临时执行 `option` 驱动 `new_id` 后生成 `/dev/ttyUSB0` 至 `/dev/ttyUSB4`；随后将 `1-1:1.4` 从 `option` 释放并绑定到 `qmi_wwan`，生成 `/dev/cdc-wdm0` 与 `wwan0`，保留 `/dev/ttyUSB0` 至 `/dev/ttyUSB3`。
- 2026-08-03 VoHive 设备发现验证：通过 API 登录 `admin/admin`、调用 `/api/devices/discovered`，VoHive 返回 1 个设备：`control_path=/dev/cdc-wdm0`、`net_interface=wwan0`、`at_port=/dev/ttyUSB2`、`mode=qmi`、`network_capable=true`，但 `degraded=true`。带 `with_imei=1` 的发现请求超时，日志显示 QMI initial sync / version info query 超时；后续需要针对该模块做 QMI/AT 稳定性和 IMEI 探测。
- 2026-08-03 阶段 2 自动化设计结论：桌面壳 WSL adapter 先检测 `wsl.exe`、发行版、`systemd`、`usbipd.exe`；启动后端时优先用隐藏 `wsl.exe --cd /opt/vohive --exec /opt/vohive/bin/vohive -c /opt/vohive/config/config.yaml` 并持有 Windows 子进程句柄；停止时终止该子进程或在 WSL 内按进程名结束；健康检查用 Windows 侧 `http://127.0.0.1:7575/`；硬件 attach 由 Windows 侧执行 `usbipd bind/attach --wsl`。
- 2026-08-03 阶段 2A 设计结论：WSL2 USB 自动识别的根因不是 VoHive 扫描不到已就绪设备，而是 `2ca3:4006` 在 WSL 内未被内核默认绑定为 modem/QMI 拓扑。推荐先做 WSL/Linux 环境准备脚本，保持 VoHive 核心发现函数纯静态；后端诊断提示作为可选增强。
- 2026-08-03 阶段 2A 交互补充：Web `添加设备`按钮适合作为交互式探测入口，点击后执行“WSL USB 准备 -> 设备发现 -> 弹窗选择添加”。但它不能是唯一入口；已配置设备的开机恢复、重新扫描和热插拔恢复也需要复用同一 USB 准备步骤。
- 2026-08-03 阶段 4 职责补充：Windows 侧 USB 枚举、`usbipd bind/attach --wsl`、管理员权限引导和 WSL 内 USB 准备脚本调用，后续应由 Windows 桌面壳统一编排；Web 页面只负责发起交互式探测和展示状态，不直接承担 Windows USB 接管。
- 2026-08-03 阶段 2A 已实施：新增 `device.PrepareWSLUSB`、`POST /api/devices/actions/prepare-usb`、`vohive --prepare-usb` 和 `packaging/wsl/vohive-usb-prepare.sh`；前端 `添加设备`发现和 `重新扫描` 前会先调用 prepare API。已在当前 WSL2 实机返回 `prepared=true`，设备为 `/dev/cdc-wdm0` + `wwan0` + `/dev/ttyUSB0-3`。
- 2026-08-03 阶段 2A 验证：`go test ./internal/device ./internal/api -count=1`、前端 `tsx --test tests/*.test.ts`、`vue-tsc --noEmit`、`go test ./cmd/vohive -count=1` 均通过；新版二进制已部署到 `/opt/vohive/bin/vohive`，Windows 访问 `http://127.0.0.1:7575/` 返回 200。
- 2026-08-03 阶段 2B 已实施：`card_policies` 新增 `roaming_enabled`，默认和旧库迁移均为 `true`；`GET/PUT /api/cards/:iccid/policy` 支持漫游策略；`PATCH /api/devices/:device_id/roaming` 会落库并发送 `AT+QCFG="roamservice",255,1` 或 `AT+QCFG="roamservice",1,1`；主卡策略面板和 eSIM 内联策略面板均新增“允许漫游”开关。
- 2026-08-03 阶段 4 MVP 已实施：新增 `desktop/` Tauri + Vue 桌面壳，内置 Linux 后端二进制、示例配置和 WSL USB 准备脚本；支持 WSL2/usbipd 检测、Baiwang 2ca3:4006 枚举、`bind/attach --wsl`、WSL 内 USB 准备、后端启动/停止、`/ping` 健康检查、日志 ring buffer、打开 Web UI、失败消息和管理员命令建议展示。
- 2026-08-03 阶段 2B/4 验证：`go test ./internal/db ./internal/api ./internal/device -count=1`、`go test ./cmd/vohive -count=1`、`node node_modules/tsx/dist/cli.mjs --test tests/*.test.ts`、`npm run build`、`cargo test`、`pnpm build`、`pnpm tauri build --debug` 均已通过。本轮 Tauri debug 产物为 `desktop/src-tauri/target/debug/vohive-plus-desktop.exe` 和 `desktop/src-tauri/target/debug/bundle/nsis/VoHive Plus_0.1.0_x64-setup.exe`。
- 2026-08-03 阶段 4 缺陷修复：修复 Tauri debug resource path 的 Windows verbatim 前缀 `\\?\F:\...` 转 WSL 路径错误，避免部署时报 `cp: cannot stat '//?/F:/...'`；启动按钮在 `/ping` 已正常时改为幂等复用既有 WSL 后端；停止按钮改为先停止 WSL 内 `/opt/vohive/bin/vohive` 进程并避免阻塞等待 Windows `wsl.exe` 子进程；debug exe 已设置 Windows GUI 子系统，打开不再显示黑色控制台框。
- 待实施后补充：VirtualBox 路线实际体积、启动耗时、USB 稳定性。
