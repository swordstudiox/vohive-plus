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

## 阶段 2C：DJI QMI 启动兼容性修复

### 根因假设

- [x] 用户日志显示自动选择 `qmi-proxy` 后失败，且 WSL 便携环境没有 `/usr/libexec/qmi-proxy`。
- [x] 设备配置只持久化 `device_backend: qmi` 和 `modem_imei`，未持久化运行时 `control_device/interface/at_port`，启动时需要重新识别当前枚举路径。
- [x] DJI 模块 raw QMI 身份探测可能超时，但同一 USB 拓扑中的 AT 口能读到 IMEI，因此只有 IMEI 的 QMI 配置应优先用兼容 AT 身份配对。
- [x] 完整托管 QMI 配置在旧控制口消失后仍要保留按 QMI IMEI 重绑能力，不能被其他设备的全局 AT 回退需求短路。

### 修复计划

- [x] QMI proxy 自动选择时允许 raw fallback；用户显式 `qmi_use_proxy: true` 时保持严格 proxy。
- [x] 只有 IMEI、缺少完整运行路径的 QMI 配置使用兼容 AT 身份发现，避免 DJI raw QMI 探测卡住启动。
- [x] 为“全局配置污染导致托管 QMI 重绑失败”新增回归测试。
- [x] 让 `AddWorkerFromConfig` 的硬件收集按当前设备配置判断 AT 回退，而不是依赖进程级全局配置。
- [x] 重新编译 Linux 后端、同步桌面壳资源并部署到 WSL2。
- [x] 实机验证不再出现 `qmi-proxy ... no such file` fatal，也不再因“未找到匹配 IMEI”或旧控制口不存在阻断启动。
- [ ] 剩余问题：WSL2 USB/IP 下 `/dev/cdc-wdm0` raw QMI 控制面仍反复 `context deadline exceeded`，DMS/WMS 服务未就绪；需要评估 qmi-proxy 打包、ECM/MBIM 模式或 VirtualBox USB 直通路线。

## 阶段 2D：DJI WSL2 下 AT 退路修复

### 根因假设

- [x] 最小 QMI 工具在停止后端后直接访问 `/dev/cdc-wdm0`，`CTL SYNC` 与 `CTL GET_VERSION_INFO` 仍超时，说明当前 WSL2 USB/IP 下 QMI 控制面本身不响应。
- [x] 临时 `device_backend: at` 实机启动可以读取 IMEI、ICCID、运营商、LTE 频段与漫游注册状态，说明 AT 控制面可用。
- [x] 但显式 AT 配置在兼容发现时被回填 `control_device/interface` 后，`requiresQMICore()` 又误判需要 QMI Core，导致 AT 退路仍被 QMI 超时噪声拖住。

### 修复计划

- [x] 调整 QMI Core 判定：显式 `device_backend: at` 时不因运行时发现到 `control_device/interface` 而启动 QMI Core。
- [x] 增加回归测试：显式 AT + QMI 运行时路径不需要 QMI Core；旧配置无 backend 但有 control device 仍按 QMI 兼容。
- [x] 重新运行 Go 设备/QMI 相关测试。
- [x] 重新编译 Linux 后端并部署到 WSL2。
- [x] 用正式配置验证 DJI 模块 AT 后端不再启动 QMI Core。
- [x] 修复 Web 添加设备保存逻辑：保存时保留用户选择的后端模式，不再把用户选择的 AT 覆盖回 QMI。
- [x] DJI/Baiwang `2ca3:4006` 在发现到 QMI 控制口且 AT 口可用时，添加设备默认选择 AT 后端，避免 WSL2 用户自然落入 raw QMI 超时路径。

### 验证结果

- [x] 当前 WSL `/opt/vohive/config/config.yaml` 已从 `device_backend: qmi` 切换为 `device_backend: at`，原配置已备份为 `/opt/vohive/config/config.yaml.bak-20260804-125939`。
- [x] Windows 侧 `http://127.0.0.1:7575/ping` 返回 `pong`。
- [x] `GET /api/devices` 显示 `wwan0 running=true healthy=true control_online=true physical_present=true worker_running=true radio_registered=true lifecycle_phase=online`，后端模式为 AT。
- [x] 已读取到 DJI 模块身份与网络状态：IMEI `863212060145346`、ICCID `89441600001002274233`、运营商 `中国联通`、`LTE BAND 3`、信号约 `-57` 至 `-71 dBm`、注册状态 `5`（漫游）。
- [x] `POST /api/devices/wwan0/actions/at` 发送 `AT+CPIN?` 返回 `+CPIN: READY`。
- [x] `npm run test --prefix web` 通过，21 项前端测试全绿。
- [x] `go test ./internal/device ./internal/qmi ./internal/api ./internal/db -count=1` 通过。
- [x] `npm run build --prefix web` 通过，并已同步 `web/dist` 到 `internal/web/dist`。
- [x] 已重新编译 `dist/vohive-open_linux_amd64`，同步到 `desktop/src-tauri/resources/vohive/vohive-open_linux_amd64`，并部署到 WSL `/opt/vohive/bin/vohive`。
- [x] `desktop` 下 `pnpm run build`、`desktop/src-tauri` 下 `cargo test`、`pnpm tauri build --debug` 通过；新版 debug exe 位于 `desktop/src-tauri/target/debug/vohive-plus-desktop.exe`。
- [ ] 剩余问题：WSL2 USB/IP 下 `/dev/cdc-wdm0` raw QMI 控制面仍 `context deadline exceeded`，QMI 数据面/代理能力需继续走 qmi-proxy/libqmi 侧车、ECM/MBIM 模式或 VirtualBox USB 直通专项。

## 阶段 2E：DJI QMI 数据面专项验证

### 目标

- [x] 验证 qmi-proxy/libqmi 侧车是否能让 WSL2 下 `/dev/cdc-wdm0` 控制面恢复可用。
- [x] 只读确认 DJI/Baiwang 模组当前 USB 网络模式和可切换模式范围，评估 ECM/MBIM 路线。
- [x] 检查 VirtualBox Headless + USB 直通前置条件。
- [ ] 安装 VirtualBox 后，用独立 VM 路线实机验证问题是否来自 WSL2 USB/IP。

### 验证计划

- [x] 路线 A：WSL2 内检查 `qmicli`、`qmi-proxy`、libqmi 包；必要时构建阶段联网安装 `libqmi-utils`，再用 `qmicli --device-open-proxy` 查询 DMS 身份。
- [x] 路线 B：通过 VoHive 当前 AT 后端只读执行 `AT+QCFG="usbnet"`、`AT+QCFG="usbnet"?`、`AT+QCFG=?`，先不写入新模式，避免设备重枚举后掉线。
- [x] 路线 C：Windows 侧检查 `VBoxManage` 是否可用；如果本机已安装 VirtualBox，再确认 USB filter/headless 所需能力；未安装则记录为后续手工验证前置项。

### 风险约束

- [x] 不在未确认回滚方式前写入 `AT+QCFG="usbnet",...` 或其它会改变 USB 枚举的 AT 命令。
- [x] 不停止当前 AT 后端，除非需要独占 QMI 控制口且已有恢复步骤。
- [x] 不自动安装 VirtualBox 或 Extension Pack；只检查本机条件并给出下一步。

### 验证结果

- [x] 当前 Windows 侧 `usbipd list` 显示 DJI/Baiwang `2ca3:4006` 仍为 `Attached`；WSL 内存在 `/dev/cdc-wdm0`、`/dev/ttyUSB0-3`、`wwan0`。
- [x] WSL 内原先没有 `qmicli/qmi-proxy`；已安装 `libqmi-utils`、`libqmi-proxy`、`libqmi-glib5`、`libmbim-glib4`、`libmbim-proxy` 等包，新增体积约 6.4 MB。
- [x] 安装后 `qmicli=/usr/bin/qmicli`，`qmi-proxy=/usr/libexec/qmi-proxy`，路径符合 VoHive 之前自动查找的 `/usr/libexec/qmi-proxy`。
- [x] `qmicli -d /dev/cdc-wdm0 --device-open-proxy --dms-get-manufacturer` 失败：`CID allocation failed in the CTL client: Transaction timed out`。
- [x] `qmicli -d /dev/cdc-wdm0 --dms-get-manufacturer` raw 模式同样在 CTL 分配 DMS client 阶段超时，说明问题不是 VoHive QMI 实现或缺少 qmi-proxy 单点导致。
- [x] qmicli 测试时间附近 WSL `dmesg` 继续出现大量 `vhci_hcd: urb->status -104`，支持“WSL2 USB/IP 控制传输不稳定/被断开”的判断。
- [x] 当前 DJI/Baiwang 模组 `ATI` 返回 `Baiwang QDC507 Revision: QDC507GLEFM21`。
- [x] 当前 `AT+QCFG="usbnet"` 和 `AT+QCFG="usbnet"?` 均返回 `+QCFG: "usbnet",0`，即当前仍是 QMI/RMNET 类模式。
- [x] 当前 USB interface class 全部为 `ff` vendor-specific，`1.0-1.3` 绑定 `option`，`1.4` 绑定 `qmi_wwan`；不是 ECM/MBIM 枚举。
- [x] `AT+QCFG=?` 在该模组上只返回 `>`，不适合作为安全范围查询；后续范围判断以项目现有模板和实机写入回滚方案为准。
- [x] 当前蜂窝状态仍正常：`AT+QNWINFO` 返回 LTE Band 3，`AT+CEREG?` 返回 `0,5`，`AT+CGATT?` 返回 `1`。
- [x] Windows 侧未发现 VirtualBox：`VBoxManage` 不在 PATH 和常见安装路径，未发现 `VBox*` 服务，也未发现 VirtualBox 卸载注册表项。
- [x] 验证后 VoHive 仍在线：`/ping` 返回 `pong`，`wwan0` 为 `online`，`healthy=true`，`esim_transport=at`。
- [ ] VirtualBox USB 直通需先安装 VirtualBox 后再继续：最小验证步骤是创建 Debian VM、安装 `usbutils/libqmi-utils/libmbim-utils/iproute2`、配置 USB filter 直通 `2ca3:4006`，再在 VM 内重跑 `qmicli`。

## 阶段 2F：DJI ECM 模式可回滚验证

### 目标

- [x] 验证 `AT+QCFG="usbnet",1` 后 DJI/Baiwang 模组是否枚举为 ECM 类网卡。
- [x] 验证 ECM 模式下是否还能保留 AT 口，用于回滚到 `AT+QCFG="usbnet",0`。
- [x] 验证回滚后 WSL2 路线能恢复到当前 AT 后端 online 状态。

### 执行约束

- [x] 切换前保存当前状态：USBNET 模式、USB interface 绑定、`usbipd list`、VoHive 设备状态。
- [x] 优先使用 VoHive API 执行 `PATCH /api/devices/wwan0/usbnet-mode`，因为它走已有 AT 超时与错误处理。
- [x] 切 ECM 后等待 Windows/WSL USB 重枚举；如果设备从 WSL 掉出，先用桌面壳同等流程 `usbipd attach --wsl` + WSL USB prepare 恢复。
- [x] 只在确认 ECM 下有 AT 口后执行回滚；如果 VoHive worker 不可用，则用 WSL 内带超时的 AT 工具/项目 API 退路，不用 `cat < /dev/ttyUSB*`。

### 验证结果

- [x] 切换前基线：Windows `usbipd list` 显示 `2ca3:4006 Baiwang` 为 `Attached`；WSL 内为 `/dev/cdc-wdm0`、`/dev/ttyUSB0-3`、`wwan0`；USB interface `1.0-1.3` 绑定 `option`，`1.4` 绑定 `qmi_wwan`；`AT+QCFG="usbnet"?` 返回 `0`；VoHive `wwan0` online。
- [x] 通过 `PATCH /api/devices/wwan0/usbnet-mode {"mode":1}` 成功发送 ECM 切换，返回“指令已发送，设备正在重启...”。
- [x] 切 ECM 后设备从 WSL 掉回 Windows `Shared`，重新执行 `usbipd attach --wsl --busid 2-1` 后回到 WSL。
- [x] ECM 枚举形态：`1-1:1.4 class=02 sub=06 proto=00`，`1-1:1.5 class=0a sub=00 proto=00`，说明模组确实进入 CDC ECM 拓扑。
- [x] 因此前给 `option` 写过 `2ca3:4006 new_id`，ECM 的 `1.4/1.5` 初始会被 `option` 抢占；手工只释放 `1.4/1.5` 并绑定 `cdc_ether` 后，生成 ECM 网卡 `enx72175c718065`。
- [x] ECM 模式保留 AT 口：`/dev/ttyUSB2` 和 `/dev/ttyUSB3` 均可响应 `ATI`，可用于回滚。
- [x] ECM 链路层可用：`udhcpc` 从模组 DHCP 获得 `192.168.225.30/24`，默认网关 `192.168.225.1`，网关 ping 成功。
- [x] ECM 公网出网未验证成功：`curl --interface enx72175c718065 http://connectivitycheck.gstatic.com/generate_204` 超时，`ping -I enx72175c718065 1.1.1.1` 丢包；但 AT 显示 PDP context 1 已获得运营商地址 `10.52.234.131` 和 DNS `13.248.255.75`。
- [x] `AT+QNETDEVCTL=1,1` 和 `AT+QNETDEVCTL=1,1,1` 在该固件均返回 `ERROR`，不能作为 ECM 出网启动命令。
- [x] 回滚通过 `/dev/ttyUSB2` 执行 `AT+QCFG="usbnet",0`，返回 `OK`；随后设备重枚举回 Windows `Shared`，重新 attach 到 WSL 后运行 `/opt/vohive/bin/vohive --prepare-usb`，恢复 `/dev/cdc-wdm0`、`/dev/ttyUSB0-3`、`wwan0` 和 `qmi_wwan` 绑定。
- [x] 回滚后调用 `/api/devices/actions/rescan` 恢复 VoHive worker；`AT+QCFG="usbnet"?` 返回 `0`。
- [x] 回滚后的无线注册有约 2 分钟恢复窗口；期间可能出现 `CREG/CEREG/CGREG=3` 和 `No Service`，最终自动回到 `46001`，`CREG/CEREG/CGREG=5`，`CGATT=1`。
- [x] 最终 VoHive 状态：`wwan0 running=true healthy=true control_online=true physical_present=true worker_running=true radio_registered=true lifecycle_phase=online esim_transport=at`，运营商为中国联通，LTE Band 3。
- [ ] 后续如果要把 ECM 做成正式路线，需要新增 WSL USB prepare 的 ECM 分支：`usbnet=1` 时不要强制把 `1.4` 绑回 `qmi_wwan`，而是释放 `1.4/1.5` 给 `cdc_ether`，并明确 DHCP/出网启动策略。

## 阶段 2G：WSL USB Prepare ECM 自动绑定

### 目标

- [x] `PrepareWSLUSB` 能识别 DJI/Baiwang `usbnet=1` ECM 拓扑。
- [x] ECM 模式下自动释放被 `option` 抢占的 `1.4/1.5`，并把 `1.4` 绑定到 `cdc_ether`。
- [x] ECM 模式下保留 `1.0-1.3` AT 口，不强制回绑 `qmi_wwan`。
- [x] QMI 模式下现有 `qmi_wwan` 准备逻辑保持不变。

### 实施计划

- [x] RED：新增 `TestPrepareWSLUSBPreparesBaiwangECMInterface`，验证 ECM interface 从 `option` 切到 `cdc_ether`。
- [x] RED：新增 `TestPrepareWSLUSBIsIdempotentWhenECMAlreadyPrepared`，验证已绑定 `cdc_ether` 时不重复写 sysfs。
- [x] GREEN：`PrepareWSLUSB` 加载 `usbnet/cdc_ether`，按 interface class 分流 QMI/ECM 绑定逻辑。
- [x] GREEN：设备摘要和 ready 判定兼容 ECM：ECM 允许 `control_path` 为空，但必须有 `net_interface`、`ATPorts` 和 `driver_name=cdc_ether`。
- [x] 验证：运行 `go test ./internal/device -run 'PrepareWSLUSB' -count=1`，再运行 `go test ./internal/device ./internal/api -count=1`。
- [x] 实机验证：当前回滚后的 QMI 模式执行 `/opt/vohive/bin/vohive --prepare-usb` 仍返回 prepared。

### 验证结果

- [x] RED 验证：新增 ECM 测试后，旧实现会把 ECM `1.4` 错绑到 `qmi_wwan`，并在已绑定 `cdc_ether` 时尝试释放 `cdc_ether`，测试按预期失败。
- [x] `PrepareWSLUSB` 新增 interface class 分流：`1.4 class=02 sub=06` 且 `1.5 class=0a` 时走 ECM；否则保持 QMI 准备路径。
- [x] ECM 准备路径会释放 `1.4/1.5` 上非 `cdc_ether` 驱动，绑定 `1.4` 到 `cdc_ether`；已是 `cdc_ether` 时幂等无 sysfs 写入。
- [x] QMI ready 判定保持兼容：只要存在 `control_path`、`net_interface` 和 AT 口即可，不额外要求测试 sysfs driver 文件被模拟更新为 `qmi_wwan`。
- [x] 专项测试 `go test ./internal/device -run PrepareWSLUSB -count=1` 通过。
- [x] 影响范围测试 `go test ./internal/device ./internal/api -count=1` 通过。
- [x] 已重新编译 `dist/vohive-open_linux_amd64`，同步到 `desktop/src-tauri/resources/vohive/vohive-open_linux_amd64`。
- [x] 已部署新版二进制到 WSL `/opt/vohive/bin/vohive` 并重启后端；当前后端 PID `49748`，`/ping` 返回 `pong`。
- [x] 新版运行中 API `POST /api/devices/actions/prepare-usb` 在当前 QMI 模式返回 `prepared=true`，actions 仅包含原 QMI 路线模块 `usbserial/option/qmi_wwan/cdc_wdm`，设备仍为 `driver_name=qmi_wwan`。
- [x] 后端重启后一度未注册网络，执行 `AT+COPS=0` 和一次完整模组重启后恢复；最终 `CREG/CEREG/CGREG=5`，自动注册到 `46000`，`CGATT=1`，VoHive `wwan0` online。手动选 `46001` 被模组拒绝，保留自动选网。

## 阶段 2B：漫游开关正式功能

> 2026-08-05 更正：本阶段最初把 `roaming_enabled` 解释为“允许模块注册漫游网络”，并自动下发 `AT+QCFG="roamservice"`。该语义已在阶段 6E 废弃；当前正式语义为“数据漫游”：只控制漫游注册状态下的数据连接/代理出站，不阻止模块驻网和收短信。

### 目标

- [x] 将“漫游开关”从 AT 模板提升为正式卡策略能力。
- [x] Web UI 曾使用“允许漫游”开关；阶段 6E 已更正为“数据漫游”开关。
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
  - 阶段 2B 旧实现曾下发 `AT+QCFG="roamservice"`；阶段 6E 已更正为只保存数据漫游策略。
  - `AT+QCFG="roamservice"` 仅保留在高级 AT 模板/手动命令，不再由普通卡策略自动发送。
- [x] 前端策略面板新增“数据漫游”开关。
  - 主设备卡策略面板支持 live 切换。
  - eSIM 卡内联策略面板支持当前卡 live、非当前卡 stored。
  - 漫游开关不参与网络/VoWiFi/飞行互斥。
- [x] OpenAPI 文档同步字段和端点。

### 验证标准

- [x] DB 测试确认迁移列存在、默认策略 `roaming_enabled=true`。
- [x] API 测试确认 `PUT /cards/:iccid/policy` 可写入漫游策略，未传字段不会误覆盖。
- [x] API 测试已在阶段 6E 更正：`PATCH /devices/:id/roaming` 只落库数据漫游策略，不发送 `roamservice` AT。
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
- [x] 用户确认：默认 `admin/admin` 和默认监听所有地址本轮以及以后都不修。

### 阶段 4G：代码审查问题修复

- [x] 修复卡策略面板请求竞态：切换设备/ICCID 时不展示或操作上一张卡策略。
- [x] 修复漫游 live API 状态一致性：避免 AT 成功但数据库失败后 UI/DB/模组状态分裂。
- [x] 修复桌面壳资源缺失静默跳过部署：发布资源不完整时给明确失败。
- [x] 修复 Web `prepare-usb` 业务失败识别：`prepared:false` 不再继续静默发现/重扫。
- [x] 修复策略开关错误提示和 pending 状态：异常时不永久卡住，并展示后端原因。
- [x] 修复 `PUT /cards/:iccid/policy` 无法清空 APN。
- [x] 修复桌面壳 WSL/usbipd 命令无超时导致 UI 卡住。
- [x] 修复 `attach_usb` 对已 `Attached` 设备不幂等。
- [x] 收敛 `DeviceConfigDTO.roaming_enabled` 误导性契约。
- [x] 清理“四开关”相关注释和 Tauri schema 生成文件策略。

### 阶段 4H：WSL 启动与 USB attach 前置保活

### 根因

- [x] `usbipd attach --wsl --busid <BUSID>` 在当前 `usbipd-win 5.3.0` 下要求至少有一个 WSL2 发行版处于 Running。
- [x] 当前桌面壳“连接 USB 到 WSL”只执行 `bind/attach`，没有先启动或保活目标 WSL2 发行版。
- [x] 当前界面没有显式“启动 WSL”按钮，用户无法在连接 USB 前主动拉起 WSL。

### 修复计划

- [x] Rust 侧补测试：需要 attach 的 USB 状态必须先保活 WSL；WSL keepalive 命令固定使用目标发行版 `Ubuntu-24.04`。
- [x] 前端补测试：runtime service 暴露 `start_wsl`，界面展示“启动 WSL”按钮。
- [x] 桌面壳新增 `start_wsl` 命令，启动隐藏 keepalive WSL 进程并等待目标发行版 Running。
- [x] `attach_usb` 在执行 `usbipd attach` 前自动调用 WSL 保活，避免用户必须手工开 WSL 终端。
- [x] UI 在运行环境卡片提供“启动 WSL”按钮，并复用现有提示/状态刷新链路。
- [x] 重新验证、重编译桌面 debug exe；当时曾生成 NSIS 安装包，阶段 4J 后已取消安装包构建。

### 阶段 4I：WSL 手动启动边界与 USB 准备重试

### 根因

- [x] 用户确认：`连接 USB 到 WSL` 不应自动启动 WSL；如果 WSL 未运行，只提示需要先启动 WSL。
- [x] 截图中的 `绑定 interface 1-1:1.4 到 qmi_wwan 失败: write .../bind: no such device` 可在 WSL 内复现；失败后短时间内内核实际完成了 `qmi_wwan 1-1:1.4` 绑定并生成 `/dev/cdc-wdm0`、`wwan0`。
- [x] 根因是 `option` 释放 `1-1:1.4` 后，立即写 `qmi_wwan/bind` 存在 WSL/USB/IP 内核状态切换竞态；当前代码把一次 `no such device` 当成最终失败。

### 修复计划

- [x] 新增 RED 测试：attach preflight 在 WSL 未 Running 时返回“请先启动 WSL”，且不代表自动启动。
- [x] 新增 RED 测试：`qmi_wwan/bind` 首次返回 `no such device` 后，短暂重试/复查 driver，最终已绑定时不失败。
- [x] `attach_usb` 改为检查目标 WSL2 发行版是否 Running；未 Running 时返回提示和保活命令建议，不调用 `ensure_wsl_running`。
- [x] `PrepareWSLUSB` 增加可测试的 sysfs 写入/等待注入点，并对 qmi bind 瞬时失败做短轮询。
- [x] 重新验证、重编译桌面 debug exe；当时曾生成 NSIS 安装包，阶段 4J 后已取消安装包构建。

### 阶段 4J：取消 NSIS 安装包构建

### 根因

- [x] 当前 NSIS 安装包只封装桌面壳和资源，不会自动安装/配置 WSL2。
- [x] 当前 NSIS 安装包不会自动安装 `usbipd-win`。
- [x] 在这些前置能力仍需用户手动准备的情况下，安装包和 `vohive-plus-desktop.exe` 的功能边界基本一致，继续生成安装包会增加误导和维护成本。

### 修复计划

- [x] Tauri 配置禁用 bundler，不再生成 NSIS 安装包。
- [x] 保留 `resources/vohive/*` 资源声明，确保单 exe 构建仍携带后端二进制、默认配置和 USB 准备脚本。
- [x] 新增桌面配置测试，防止后续重新启用 `nsis` target。
- [x] 删除本地旧 NSIS debug 输出目录，避免误用过期安装包。
- [x] 后续阶段 5 改为便携包/离线运行策略，不再规划标准安装包。

### 阶段 4 验证标准

- [ ] 从 Windows 双击桌面程序后，可以一键启动 WSL2 内的 VoHive。
- [x] 未插入 DJI 模组时，桌面壳能明确提示“未发现 2ca3:4006 Baiwang”。
- [x] 插入 DJI 模组后，桌面壳能枚举 busid，并完成或引导完成 `usbipd bind/attach --wsl`。
- [x] WSL 内 USB 准备脚本返回成功后，Web `添加设备`能发现 `/dev/cdc-wdm*`、`wwan*`、`/dev/ttyUSB*`。
- [x] 后端异常退出时，桌面壳能显示失败原因并允许重新启动。
- [x] 端口 `7575` 被占用时，桌面壳能给出明确诊断，不静默失败。

## 阶段 5：便携包与离线运行

- [ ] 构建阶段联网拉取 Go/npm 依赖。
- [ ] 便携包内置已编译的 `vohive` 二进制、默认配置、启动脚本。
- [ ] WSL2 路线便携包不在运行时下载 `vohive`。
- [ ] VirtualBox 路线便携包不在运行时下载 `vohive`。
- [ ] 明确 VirtualBox 本体是否内置、提示用户安装，或作为外部前置依赖。
- [ ] 输出便携包策略，优先面向已有 WSL2 或已有 VirtualBox 的用户。

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

## 阶段 6：GitHub 远程、版本、CI 与 Release

### 目标

- [x] 将当前本地 fork 推送到 `swordstudiox/vohive-plus`。
- [x] 固件/后端 Linux 运行时版本号设为 `1.0.0`。
- [x] Windows 桌面壳版本号设为 `1.0.0`。
- [x] GitHub Actions 在 tag 发布时自动构建 Linux 后端运行时和 Windows 桌面便携包。
- [x] Release notes 先写入 `.github/release-notes/v1.0.0.md`，发布时自动读取并作为 Release 页面说明。
- [x] README 改为面向 VoHive Plus，包含普通用户、开发者、环境要求、WSL2/usbipd、构建和发布产物说明。
- [x] 根 Go module path 迁移为 `github.com/swordstudiox/vohive-plus`，避免构建和测试输出继续显示旧上游 `github.com/iniwex5/vohive`。
- [x] Dockerfile / Dockerfile.github 的 Go `ldflags -X` 版本注入路径同步迁移到 `github.com/swordstudiox/vohive-plus`。
- [x] Web 构建脚本拆分为快速 `build` 和完整 `build:check`；Release workflow 与 Dockerfile 使用完整校验构建，日常本地构建避免反复等待 WSL `/mnt/f` 上的慢速 `vue-tsc`。

### 待确认

- [x] Release 发布目标仓库：用户已确认发布到 `swordstudiox/vohive-plus/releases`。

### 推荐方案

- [x] 远程策略：把当前 `origin` 从 `windloom/vohive-open` 改名为 `upstream`，新增 `origin=https://github.com/swordstudiox/vohive-plus.git`，再推送 `main` 和 `v1.0.0` tag。
- [x] 版本策略：`v1.0.0` 作为首个正式版本；CI 从 tag 名读取版本，写入 Go `internal/global.Version`，同步桌面 `package.json`、`tauri.conf.json`、`Cargo.toml` 到 `1.0.0`。后续版本遵循语义化版本：修复用 patch，兼容功能用 minor，破坏性变化用 major。
- [x] 构建产物：不恢复 NSIS 安装包；Release 上传 Linux 后端运行时二进制和校验文件，以及 Windows x64 桌面便携 zip。桌面 zip 内含 `vohive-plus-desktop.exe`、`resources/vohive/vohive-open_linux_amd64`、示例配置和 WSL USB 准备脚本。
- [x] Workflow 策略：改造现有 `.github/workflows/binary-release.yml`，保留 Linux 多架构后端构建，新增 Windows 桌面构建 job，并在 release job 统一上传所有产物。
- [x] Release notes 策略：新增 `.github/release-notes/v1.0.0.md`；workflow 优先读取对应版本文件，文件不存在时使用兜底说明。
- [x] 模块路径策略：`go.mod`、`web/go.mod`、内部 Go import、Makefile、Dockerfile、Release workflow 和 README 使用 `github.com/swordstudiox/vohive-plus`；新增回归测试防止旧上游根模块路径回流。

### 可选项

- [x] 可选 A：只发布到 `swordstudiox/vohive-plus/releases`。用户已确认，产物和源码同仓库，GitHub Actions 权限最简单。
- [ ] 可选 B：源码推送到 `swordstudiox/vohive-plus`，但 Release 产物发布到 `swordstudiox/esp32_sms_forwarding/releases`。不推荐，需要额外 token 和跨仓库权限，用户也更容易下载错项目。
- [ ] 可选 C：桌面发布裸 exe。暂不推荐，因为运行还依赖旁边的 `resources/vohive/*`，裸 exe 容易缺资源。
- [x] 可选 D：桌面发布 zip 便携包。最符合“无需安装包”的当前边界。

### 实施步骤

- [x] 确认 Release 目标仓库。
- [x] 更新版本号到 `1.0.0`。
- [x] 新增 `.github/release-notes/v1.0.0.md`。
- [x] 改造 GitHub Actions release workflow：Linux 后端、多架构校验、Windows 桌面 zip、Release notes 自动读取。
- [x] 重写 README。
- [x] 迁移 Go module path 和内部 import 到 `github.com/swordstudiox/vohive-plus`。
- [x] 补齐 Dockerfile / Dockerfile.github 旧 `ldflags` 路径残留并扩大模块路径回归测试扫描范围。
- [x] 拆分 Web 快速构建和完整发布构建，并新增脚本回归测试。
- [x] 验证拆分效果：WSL `/mnt/f` 下 `npm run build --prefix web` 耗时约 107 秒，`npm run build:check --prefix web` 耗时约 270 秒；发布入口仍保留完整类型检查。
- [x] 本地验证 workflow 关键脚本、桌面测试、前端构建和 Rust 测试。
- [x] 本地提交，提交说明使用详细中文。
- [x] 调整 git remote，推送 `main`。
- [x] 创建并推送 `v1.0.0` tag 触发 GitHub Actions。
- [ ] 检查 Actions 和 Release 页面产物。本环境 GitHub API、浏览器控制和普通 HTTPS 页面查询受限，已通过 `git ls-remote` 确认远端 `main` 和 `v1.0.0` tag 存在；Actions/Release 页面仍需在 GitHub 网页确认。

## 阶段 2H：URC 状态日志降噪

### 目标

- [x] 同值、周期性状态 URC 不再按 INFO 反复刷屏。
- [x] `+CPIN: READY` 重复上报时不再反复广播 RDY，避免设备池每分钟输出 `[事件驱动] Modem RDY`。
- [x] 状态首次出现和真实变化仍按 INFO 输出。
- [x] 新短信、USSD、来电、挂断、PCM 流控等事件型 URC 不被降噪。

### 推荐方案

- [x] 在 modem 层处理根因，而不是在前端隐藏重复行。
- [x] 只抑制日志和重复 READY 广播，不抑制 SIM 状态 handler、USSD 分发、短信读取等业务事件。
- [x] 新增小型状态缓存，按 URC key 和解析后的字段生成稳定签名；首次或签名变化时记录，重复同值时静默。
- [x] 用 TDD 覆盖 `+CREG/+CGREG/+CEREG`、`+CPIN`、`+QSIMSTAT` 和 `+CMTI` 的差异。

### 实施步骤

- [x] 写失败测试：状态型 URC 首次记录、重复同值不记录、变化后再记录。
- [x] 写失败测试：重复 `+CPIN: READY` 不重复触发 RDY。
- [x] 写失败测试：`+CMTI` 新短信通知每次都应保留。
- [x] 实现 modem 层 URC 状态签名缓存和 READY 触发降噪。
- [x] 运行 `go test ./internal/modem -count=1`。
- [x] 运行相关后端测试并重新编译 Linux 后端二进制。
- [x] 同步后端二进制到桌面资源和 WSL `/opt/vohive/bin/vohive`。
- [x] 实机观察日志至少 2 分钟，确认重复状态日志不再刷屏。

## 阶段 4K：桌面内置后端二进制去 Git 跟踪

### 目标

- [x] `dist/`、Tauri `target/` 和桌面内置 Linux 后端二进制都不再进入 Git 跟踪。
- [x] 本地可达历史和远程 `origin/main` 不再包含 `desktop/src-tauri/resources/vohive/vohive-open_linux_amd64`。
- [x] 远程 `v1.0.0` tag 同步到清理后的历史。
- [x] 桌面构建仍能在构建前获得 Linux 后端资源，不依赖仓库提交大二进制。
- [x] 用户已确认执行历史清理；允许重写本地未推送历史，并对 `origin/main` 与 `v1.0.0` tag 执行 force push。

### 推荐方案

- [x] 保留 `desktop/src-tauri/resources/vohive/config.example.yaml` 和 `vohive-usb-prepare.sh` 作为小型运行资源。
- [x] 新增 `.gitignore` 规则，只忽略 `desktop/src-tauri/resources/vohive/vohive-open_linux_amd64`。
- [x] 用 `git rm --cached` 从索引移除二进制，保留本地文件方便当前 debug exe/本地构建继续使用。
- [x] 新增桌面资源同步脚本：优先从 `dist/vohive-open_linux_amd64` 复制；若目标资源已存在则复用；二者都不存在时给出明确错误。
- [x] 调整 Tauri `beforeBuildCommand`，本地/CI 打桌面包前自动检查或同步后端资源。

### 实施步骤

- [x] 修改 `.gitignore`，忽略桌面内置 Linux 后端二进制。
- [x] 新增桌面资源同步脚本和测试。
- [x] 调整桌面构建脚本/配置调用同步脚本。
- [x] 从 Git 索引移除已跟踪二进制，但不删除本地工作文件。
- [x] 运行桌面脚本测试和相关配置测试。
- [x] 重写本地可达历史，移除已跟踪二进制路径。
- [x] 清理本地旧引用与不可达大对象。
- [x] 通过代理推送 `origin/main` 和 `v1.0.0` tag。
- [x] 验证远程 `main` 和 tag 已指向清理后的提交。

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
- 2026-08-03 阶段 4G 代码审查修复：用户确认默认 `admin/admin` 和默认监听所有地址本轮以及以后都不修；其余审查问题已修复，包括卡策略竞态、漫游 live API 状态一致性、APN 清空、prepare-usb 业务失败识别、策略开关错误展示、桌面壳命令超时、USB attach 幂等、资源缺失校验和 DTO 契约收敛。提交前验证：`git diff --check` 仅 CRLF 提示；`go test ./internal/api ./internal/db ./internal/device -count=1`、`node --test tests/*.test.ts`、`node node_modules/vue-tsc/bin/vue-tsc.js --noEmit`、`node node_modules/vite/bin/vite.js build`、`cargo test`、`pnpm run build` 均通过。
- 2026-08-03 产物重编译：已重新执行 Web build 并同步到 `internal/web/dist`；已在 WSL2 中重新编译 Linux amd64 后端二进制 `dist/vohive-open_linux_amd64`，并覆盖桌面壳内置资源 `desktop/src-tauri/resources/vohive/vohive-open_linux_amd64`；已执行 `pnpm tauri build --debug` 重新生成 `desktop/src-tauri/target/debug/vohive-plus-desktop.exe` 和 NSIS debug 安装包。
- 2026-08-03 阶段 4H 修复：截图中的 `usbipd: error: There is no WSL 2 distribution running` 根因已修复；桌面壳新增“启动 WSL”按钮，`attach_usb` 在执行 `usbipd attach --wsl` 前会自动启动并保活 `Ubuntu-24.04`，不再要求用户手动打开 WSL 终端。验证：新增 RED 测试后确认失败；实现后 `cargo test` 13 项通过、`node --test tests/*.test.mjs` 2 项通过、`pnpm run build` 通过、`pnpm tauri build --debug` 通过。
- 2026-08-03 阶段 4I 修正：用户确认 `连接 USB 到 WSL` 不应自动启动 WSL；已改为仅检查 `Ubuntu-24.04` 是否 Running，未运行时提示先点“启动 WSL”。`PrepareWSLUSB` 对 `qmi_wwan/bind` 瞬时 `no such device` 增加重试和 driver 复查；新后端实机 `--prepare-usb` 返回 `prepared=true`，设备为 `/dev/cdc-wdm0` + `wwan0` + `/dev/ttyUSB0-3`。验证：RED 测试确认失败后修复，`go test ./internal/api ./internal/db ./internal/device -count=1`、`cargo test`、`node --test tests/*.test.mjs`、`pnpm run build`、`pnpm tauri build --debug` 均通过。
- 2026-08-03 阶段 4J 决策：用户确认当前安装包不安装/配置 WSL2，也不安装 `usbipd-win`，功能上与桌面壳 exe 基本一致，因此取消 NSIS 安装包构建；后续只保留桌面 exe 与便携包路线。
- 待实施后补充：VirtualBox 路线实际体积、启动耗时、USB 稳定性。
- 2026-08-04 阶段 6 发布前修正：用户指出 Go 测试输出仍显示旧根路径后，已确认并修复 `go.mod`、`web/go.mod`、内部 import、Makefile、Release workflow、Dockerfile 和 Dockerfile.github 中的项目根模块路径；当前 WSL2 Go 测试输出为 `github.com/swordstudiox/vohive-plus/...`。同时将 Web 构建拆分为快速 `build` 和完整 `build:check`，Release/Docker 继续使用完整校验，本地快速构建避免每次等待慢速 `vue-tsc`。
- 2026-08-04 阶段 6 验证：`node --test tests/*.test.mjs` 3 项通过；`desktop` 下 `node --test tests/*.test.mjs` 5 项通过；`git diff --check` 通过但有 CRLF 提示；WSL2 `go test ./cmd/vohive ./internal/api ./internal/db ./internal/device -count=1` 通过；WSL2 `npm run test --prefix web` 17 项通过；WSL2 `npm run build --prefix web` 通过，耗时约 107 秒；WSL2 `npm run build:check --prefix web` 通过，耗时约 270 秒；`desktop` 下 `pnpm run build` 通过；`desktop/src-tauri` 下 `cargo test` 15 项通过。
- 2026-08-04 阶段 6 推送状态：已创建提交 `8bd57b3 发布 VoHive Plus 1.0.0` 并推送到 `origin/main`；已创建并推送注释标签 `v1.0.0`，标签对象为 `06016fea...`，指向提交 `8bd57b3...`。`git ls-remote origin refs/heads/main` 返回 `8bd57b3...`，`git ls-remote origin refs/tags/v1.0.0` 返回 `06016fea...`。本机未安装 `gh`，GitHub API/普通 HTTPS 查询和浏览器控制不可用，因此未能在本环境直接确认 Actions/Release 页面产物。
- 2026-08-04 大疆 QMI 连接修复：用户日志显示自动选择 `qmi-proxy` 后失败，错误为 `/usr/libexec/qmi-proxy` 不存在且 `qmi_proxy_fallback_to_raw=false`。实查 `/opt/vohive/config/config.yaml` 未显式配置 `qmi_use_proxy`，只有 `device_backend: qmi`，说明这是瞬时控制口持有者触发的自动 proxy 选择。已修改 QMI client options：显式用户开启 proxy 时保持严格 proxy；自动选择 proxy 时开启 `ProxyFallbackToRaw`，使缺少 `qmi-proxy` 的 WSL2 环境能回退 raw QMI。
- 2026-08-04 大疆启动识别修复：针对只有 `device_backend: qmi` + `modem_imei` 的配置，启动时优先通过同 USB 拓扑的 AT 口确认 IMEI，再把当前 `/dev/cdc-wdm0`、`wwan0`、`/dev/ttyUSB2` 合回 QMI 设备，避免 DJI raw QMI 身份探测超时导致“未找到匹配 IMEI”。同时修复 `collectRescanHardware` 读取进程级全局配置导致完整托管 QMI 重绑被无关设备 AT 回退需求短路的测试顺序问题。
- 2026-08-04 大疆实机验证：修复后二进制已部署到 WSL `/opt/vohive/bin/vohive` 并启动。`/ping` 返回 200；`/api/devices/discovered?with_imei=1` 返回 `imei=863212060145346`、`control_path=/dev/cdc-wdm0`、`net_interface=wwan0`、`at_port=/dev/ttyUSB2`、`configured=true`。启动早期 worker 可注册，但 raw QMI 持续超时后被健康阈值下线，最终 `/api/devices` 显示 `physical_present=true`、`worker_running=false`、`control_online=false`、`lifecycle_phase=usb_wait`、`lifecycle_reason=qmi_health_threshold`。AT 查询确认模块为 `Baiwang QDC507`，`AT+QCFG="usbnet"` 返回 `0`，SIM `READY`，`CEREG: 0,5`，`QNWINFO` 为 LTE/46001/Band 3。剩余问题是 WSL2 USB/IP 下 raw QMI 控制面仍 `context deadline exceeded`，需后续专项处理。
- 2026-08-04 阶段 2H URC 日志降噪：重复刷屏根因是后端每分钟收到同值 `+QSIMSTAT/+CPIN/+CREG` 仍按 INFO 输出，且重复 `+CPIN: READY` 会再次广播 RDY 触发设备池日志。已在 modem 层新增状态型 URC 签名缓存，只抑制日志和重复 READY 兜底广播，不阻断 SIM 状态 handler、短信、USSD、来电等业务分发。验证：`go test ./internal/modem -count=1` 与 `go test ./internal/api ./internal/device ./internal/modem -count=1` 通过；新二进制已部署到桌面资源和 WSL `/opt/vohive/bin/vohive`；实机日志显示 17:03:45 重启后只记录首次状态和 `CREG 5 -> 2 -> 5` 真实变化，不再按分钟重复 `QSIMSTAT/CPIN/Modem RDY/CREG=5`。
- 2026-08-04 阶段 4K 桌面内置后端二进制去 Git 跟踪：`desktop/src-tauri/resources/vohive/vohive-open_linux_amd64` 已从 Git 索引移除并加入 `.gitignore`，本地文件保留；新增 `desktop/scripts/sync-backend-resource.mjs`，Tauri dev/build 前自动从 `dist/vohive-open_linux_amd64` 同步或复用 CI 已复制的资源。验证：桌面 Node 测试通过，`git ls-files` 已查不到三个大二进制路径，`git check-ignore` 均命中忽略规则。
- 2026-08-04 阶段 4K 历史清理完成：用户确认后，已用历史重写从本地可达 refs 中移除 `desktop/src-tauri/resources/vohive/vohive-open_linux_amd64`；删除 `refs/original/*`、过期 reflog 并执行 GC 后，`git rev-list --objects --all` 与 `git log --all -- desktop/src-tauri/resources/vohive/vohive-open_linux_amd64` 均无输出，`git count-objects -vH` 显示 pack 约 2.55 MiB。通过 `http://127.0.0.1:10808` 代理和 `http.sslBackend=openssl` 推送远程：`origin/main` force update 到 `8b0eee2d742b52dc014ada85678712cbcaa0cc86`，`v1.0.0` tag force update 到 tag object `12c1fbcff35ba32a8b5b880a0eeb172affc55cc5`，指向提交 `87e0f1108c47fe9ea032da6ee33f92dbc2fce42f`。已用 `git ls-remote` 通过代理确认远程 refs 一致。

## 阶段 6B：README 合并上游说明与删除 Docker 发布 workflow

### 目标

- [x] README 明确写明本项目已从 `windloom/vohive-open` fork 为 `swordstudiox/vohive-plus`。
- [x] README 合并上游 `windloom/vohive-open` 原始说明里的核心特性、典型应用场景、架构、免责声明和许可证信息。
- [x] README 不再保留旧容器镜像名、容器仓库使用说明或容器发布承诺。
- [x] GitHub Actions 不再登录容器仓库，也不再构建/上传容器镜像。

### 推荐方案

- [x] 保留当前 VoHive Plus 的普通用户、WSL2、桌面壳、Release 和开发者说明。
- [x] 把上游原 README 内容并入“上游能力概览”一节，而不是覆盖 VoHive Plus 的 Windows 桌面化说明。
- [x] 删除容器发布/构建 workflow，避免 Actions 触发容器仓库构建。
- [x] 增加 Node 回归测试，防止容器仓库登录 workflow、旧镜像名和 fork 声明缺失回流。

### 实施步骤

- [x] 写失败测试：README 必须包含 fork 声明和上游能力概览。
- [x] 写失败测试：workflow 不得包含容器仓库登录、旧镜像名或容器构建发布 workflow。
- [x] 更新 README。
- [x] 删除容器发布 workflow。
- [x] 运行 Node 回归测试和必要检查。
- [x] 提交并通过代理推送主变更。

### 评审记录

- 2026-08-04 阶段 6B README/Actions 修复：已将 `windloom/vohive-open` 原 README 中的项目定位、核心特性、典型应用场景、技术栈、免责声明和许可证信息合并到 `README.md`，并在顶部明确写明本项目已从 `windloom/vohive-open` fork 为 `swordstudiox/vohive-plus`。按用户要求删除容器发布路线，不再保留旧容器镜像名或容器仓库使用说明；已删除容器发布/构建 workflow，避免 Actions 继续请求容器仓库账号密码。新增 `tests/repositoryDocs.test.mjs` 防止 README fork 声明缺失和容器发布 workflow 回流。验证：`node --test tests/*.test.mjs` 与 `desktop` 下 `node --test tests/*.test.mjs` 均通过。
- 2026-08-04 阶段 6B 推送记录：主变更提交 `f286408853afa7732b8ae14f0104df020b7e1a9e` 已通过命令级代理推送到 `origin/main`，并通过 `git ls-remote origin refs/heads/main` 确认远程 main 指向该提交。

## 阶段 6C：普通用户前置依赖说明补充

### 目标

- [x] 明确桌面壳点击 `启动后端` 会自动把内置 Linux 后端部署到 WSL `/opt/vohive`，普通用户不需要手动部署。
- [x] 明确桌面便携包不会下载、内置或安装 `usbipd-win`，用户需要从官方渠道安装。
- [x] 补充 WSL、usbipd-win、WebView2 官方链接和首次 WSL 初始化、`wsl --update` 提示。
- [x] 明确普通用户不需要 Go、Node、Rust、pnpm、Docker 或 VirtualBox。

### 实施步骤

- [x] 在仓库文档回归测试中增加运行前置依赖说明断言。
- [x] 更新 `README.md` 的环境要求、安装边界和限制说明。
- [x] 更新 `.github/release-notes/v1.0.0.md` 的环境要求和已知限制。
- [x] 运行文档测试并提交推送。

### 评审记录

- 2026-08-04 阶段 6C 文档补充：已补充 WSL、usbipd-win、WebView2 官方链接，并明确便携包不会下载/内置/安装系统级依赖；普通用户无需安装开发工具链或 VirtualBox。验证：`node --test tests/*.test.mjs` 7 项通过，`git diff --check` 无 whitespace 错误，仅有 Windows 换行提示；关键说明扫描已命中 README、Release note 和回归测试。

## 阶段 6D：发布 VoHive Plus 1.0.1

### 版本判断

- [x] 当前最新 tag 为 `v1.0.0`。
- [x] `v1.0.0` 之后的变更是修复、CI 发布边界、README/Release 文档和依赖说明，没有破坏性变更，也没有新增用户功能。
- [x] 按 README 的语义化版本规则，本次应递增 patch，发布 `v1.0.1`。

### 目标

- [x] 新增 `.github/release-notes/v1.0.1.md`，说明相对 `v1.0.0` 的更新。
- [x] 将 README 中面向用户和发布示例的当前版本号更新为 `1.0.1`。
- [x] 将 GitHub Actions 手工触发默认版本更新为 `1.0.1`。
- [x] 增加/更新仓库文档测试，防止当前发布说明和版本号漏改。
- [x] 运行验证，提交，创建并推送 `v1.0.1` tag 触发 GitHub Release。

### 风险与边界

- [x] 本次不改应用代码，不重新设计发布 workflow。
- [x] 推送 tag 后由 GitHub Actions 构建后端多架构运行时和 Windows 桌面便携包，并发布到 `swordstudiox/vohive-plus/releases`。

### 评审记录

- 2026-08-04 阶段 6D 版本准备：按语义化规则选择 patch 版本 `1.0.1`；已新增 `.github/release-notes/v1.0.1.md`，更新 README 当前产物名、开发构建示例、发布示例和 `binary-release.yml` 手工触发默认版本。验证：`node --test tests/*.test.mjs` 8 项通过，`desktop` 下 `node --test tests/*.test.mjs` 8 项通过，`git diff --check` 无 whitespace 错误，仅有 Windows 换行提示。
- 2026-08-04 阶段 6D 发布完成：提交 `0894f5701e1e21cc577b6cecc7464fb21609eabf` 已推送到 `origin/main`；注释 tag `v1.0.1` 已推送，tag object 为 `004a9fb541f8d1651dd10e75a98d719ac8a6d1f0`，指向提交 `0894f5701e1e21cc577b6cecc7464fb21609eabf`。GitHub Actions run `30906757503` 已完成且结论为 `success`，Release `VoHive Plus 1.0.1` 已发布到 `https://github.com/swordstudiox/vohive-plus/releases/tag/v1.0.1`。Release 资产包含 Windows x64 桌面便携 zip、Linux amd64/arm64/armv7 后端运行时及对应 sha256 文件。

## 阶段 6E：卡策略驻网/数据/漫游语义澄清

### 根因调查

- [x] 用户实机现象：UI 关闭“允许漫游”后，模块仍可注册网络。
- [x] 当前“允许漫游”实现只下发 `AT+QCFG="roamservice",255,1` 或 `AT+QCFG="roamservice",1,1`，没有验证注册状态，也没有作为数据面漫游守卫。
- [x] 当前“开启网络”实际是启动/停止数据网络连接；关闭后仍会保持 QMI/MBIM registration reconcile，模块仍可驻网，SMS 恒开。
- [x] 当前“飞行模式”才是关闭射频/断开驻网的开关，但它是反向语义，不能等同于“允许模块注册网络、收短信”的正向功能。
- [x] 当前概览把 `RegStatus=5` 漫游注册归类为 `registered`，没有在 UI 上清晰区分归属地注册和漫游注册。

### 已确认方案

- [x] 将“开启网络”重命名为“蜂窝数据”，描述为“控制数据连接/代理出站，不影响驻网和短信”。
- [x] 将原 `roaming_enabled` 策略重定义为“数据漫游”：当设备处于 `RegStatus=5` 漫游注册时，关闭后应阻止启动数据网络/代理连接，但不阻止驻网和 SMS。
- [x] 不再把普通卡策略里的“漫游”解释为“禁止模块注册漫游网络”；`AT+QCFG="roamservice"` 仅作为高级 AT 模板/手动指令保留。
- [x] 新增正向“驻网与短信”开关，UI 语义上复用并反转现有 `airplane_enabled`，避免新增数据库字段。
- [x] 让概览/API/UI 明确展示 home/roaming/searching/denied，而不是把 home 和 roaming 都压成 `registered`。
- [x] 切卡后 SIM 身份未就绪时，概览不能继续用旧运行态展示“有信号/已注册漫游”，需要降级显示，避免误导用户以为切卡成功。

### 实施步骤

- [x] RED：注册状态 `RegStatus=5` 应投影为 `roaming`。
- [x] RED：卡策略漫游开关只保存“数据漫游”策略，不再发送 `AT+QCFG="roamservice"`。
- [x] RED：漫游注册且数据漫游关闭时，不应启动蜂窝数据。
- [x] RED：切卡身份未就绪时，概览不展示旧信号和旧漫游注册态。
- [x] RED：前端卡策略显示“驻网与短信 / 蜂窝数据 / 数据漫游”，删除“关闭后模块不会注册漫游网络”等错误文案。
- [x] GREEN：实现后端策略行为和 API 投影。
- [x] GREEN：实现前端显示和类型更新。
- [x] 验证 Go、Web 单测、类型检查和构建。
- [x] 记录评审和教训。
- [x] 提交、推送并发布 `v1.0.2`。

## 阶段 6F：桌面壳 WSL 进程识别与版本展示

### 根因调查

- [x] 用户截图显示：WSL 已启动、USB 已 Attached、健康检查正常，但桌面壳“后端进程”显示未运行。
- [x] 当前桌面壳 `backend_status()` 只看本桌面进程持有的 `state.backend` 子进程句柄；从 GitHub 下载版启动时，如果 WSL 内已有 `/opt/vohive/bin/vohive` 后端，桌面壳没有句柄，因此误报“未运行”。
- [x] 当前运行环境区域只有“启动 WSL”，缺少显式“停止 WSL”入口。
- [x] 桌面壳标题和 Web 后端管理界面标题缺少版本号；同版本本地编译版、GitHub 下载版和运行中的 WSL 后端难以区分。

### 实施步骤

- [x] RED：桌面 UI 必须同时提供“启动 WSL”和“停止 WSL”动作。
- [x] RED：桌面 runtime service 必须暴露 `stop_wsl` 调用。
- [x] RED：桌面标题显示 `VoHive Plus v<桌面版本>`。
- [x] RED：后端状态在健康检查正常或 WSL 内存在 `/opt/vohive/bin/vohive` 进程时，应显示为运行中。
- [x] RED：Web 管理壳品牌显示 `VoHive v<后端版本>`。
- [x] GREEN：实现 WSL 停止命令、WSL 内后端进程探测和版本展示。
- [x] 验证桌面 Node 测试、Rust 测试、Web 测试/类型检查/构建。
- [x] 记录评审和教训。
- [x] 提交、推送并发布 `v1.0.2`。

### 版本发布准备

- [x] 本次是用户可见 bugfix 和语义修正，按语义化版本规则从 `1.0.1` 递增到 `1.0.2`。
- [x] 桌面本地元数据、Tauri 配置、Release workflow 默认版本和 README 当前产物名已同步为 `1.0.2`。
- [x] 新增 `.github/release-notes/v1.0.2.md`，说明相对 `v1.0.1` 的更新。

### 评审记录

- 2026-08-06 阶段 6E/6F 验证：`node --test tests/*.test.mjs` 8 项通过；`desktop` 下 `node --test tests/*.test.mjs` 12 项通过；`cargo test --manifest-path desktop/src-tauri/Cargo.toml --offline` 21 项通过；WSL2 `go test ./internal/api ./internal/device ./internal/db -count=1` 通过；WSL2 `npm run test --prefix web` 22 项通过；WSL2 `npm run typecheck --prefix web` 通过；WSL2 `npm run build --prefix web` 通过；`desktop` 下 `pnpm run build` 通过；`desktop` 下 `pnpm tauri build --debug` 通过。
- 2026-08-06 产物重编译：已同步最新 `web/dist` 到 `internal/web/dist`；已用 `global.Version=1.0.2` 重新编译 Linux amd64 后端 `dist/vohive-open_linux_amd64`；已同步到桌面壳资源并重新生成 debug 桌面程序 `desktop/src-tauri/target/debug/vohive-plus-desktop.exe`。
- 2026-08-06 检查结果：`git diff --check` 无 whitespace error，仅有 Windows 换行提示；首次 Rust 在线测试被 crates.io/TLS 环境阻断，改用既有 Cargo 缓存 `--offline` 完成验证；一次并行 Web 类型检查残留进程已清理后单独重跑通过。
- 2026-08-06 本地审查补救：发现 `停止 WSL` 后状态刷新会通过 WSL 内 `pgrep` 探测后端，从而可能重新启动刚停止的 WSL；已改为只有发行版处于 Running 时才执行后端 PID 探测，并新增单测覆盖 stopped/running 两种分支。
- 2026-08-06 审查补救追加：`/api/dashboard/devices` 也已接入切卡身份未确认时的运行态压制，避免 dashboard 继续显示旧卡信号、运营商和漫游状态；live 网络、VoWiFi、飞行模式和数据漫游开关在硬件动作失败时已回滚策略与 worker 展示态，避免 UI 和真实硬件状态分裂；Release workflow 的 `Cargo.toml` 版本戳改为仅替换 `[package]` 段，避免误改其它段的 `version` 字段。补充验证：`node --test desktop\tests\releaseWorkflow.test.mjs` 通过；WSL2 `go test ./internal/api -run "DeviceNetworkEnableRollsBack|DeviceVoWiFiEnableRollsBack|DeviceFlightModeRollsBack|DeviceRoamingDisableRollsBack|DashboardDevicesSuppresses|StatusDetailSuppresses|OpenAPIRoaming" -count=1` 通过。
- 2026-08-06 提交前审查补救：数据漫游关闭后，如果运行态刷新发现设备从归属地注册进入 `RegStatus=5` 漫游驻网，会自动执行既有数据网络断开守卫；桌面壳 `准备 WSL USB` 和 `停止后端` 在 WSL stopped 时不再调用 `wsl -d ... --exec` 反向启动发行版；桌面健康检查改为严格解析 HTTP 状态码和 VoHive `/ping` body；OpenAPI `DeviceConfigDTO` 已移除错放的 `roaming_enabled`。RED/GREEN 验证：新增测试先失败，修复后 `cargo test --manifest-path desktop/src-tauri/Cargo.toml --offline` 26 项通过；WSL2 `go test ./internal/device -run TestWorkerRuntimeServingSystemStopsConnectedDataWhenRoamingDisallowed -count=1` 通过；WSL2 `go test ./internal/api -run TestOpenAPIDeviceConfigDTODoesNotExposeRoamingPolicy -count=1` 通过。
- 2026-08-06 发布前本地产物确认：已重新执行 Web build 并同步 embed dist；Linux amd64 后端已用 `global.Version=1.0.2` 重编译，二进制大小约 49.55 MB，已确认包含 `1.0.2` 版本字符串；桌面 debug 构建生成 `desktop/src-tauri/target/debug/vohive-plus-desktop.exe`。注意后端二进制当前没有 `--version` 参数，版本验证不能使用该参数。
- 2026-08-06 最终提交前验证：`node --test tests/*.test.mjs` 8 项通过；`desktop` 下 `node --test tests/*.test.mjs` 12 项通过；`cargo test --manifest-path desktop/src-tauri/Cargo.toml --offline` 26 项通过；WSL2 `go test ./internal/api ./internal/device ./internal/db -count=1` 通过；WSL2 `npm run test --prefix web` 22 项通过；WSL2 `npm run typecheck --prefix web` 通过；WSL2 `npm run build --prefix web` 通过；`desktop` 下 `pnpm run build` 通过；`desktop` 下 `pnpm tauri build --debug` 通过；`git diff --check` 无 whitespace error，仅有 Windows CRLF 提示。
- 2026-08-06 阶段 6E/6F 发布完成：发布提交 `7b34068812221369836001b584e47543ab6058f4` 已推送到 `origin/main`；注释 tag `v1.0.2` 已推送，tag object 为 `cbe0b79263223dfe55c7e911749575e65be7edeb`，指向提交 `7b34068812221369836001b584e47543ab6058f4`。GitHub Actions run `31068541380` 已完成且结论为 `success`，Release `VoHive Plus 1.0.2` 已发布到 `https://github.com/swordstudiox/vohive-plus/releases/tag/v1.0.2`。Release 资产共 8 个：Windows x64 桌面便携 zip、Linux amd64/arm64/armv7 后端运行时，以及对应 sha256 文件。

## 阶段 6G：WSL USB qmi_wwan 动态 ID 修复

### 根因调查

- [x] 用户插入 DJI/Baiwang 模块后，桌面 UI 报错：`绑定 interface 1-1:1.4 到 qmi_wwan 失败: write /sys/bus/usb/drivers/qmi_wwan/bind: no such device`。
- [x] 实机状态显示 `1-1:1.4` 从 `option` 释放后未绑定驱动，且没有生成 `/dev/cdc-wdm0`；`qmi_wwan` 驱动目录存在 `new_id`。
- [x] 临时实机验证确认：先写 `/sys/bus/usb/drivers/qmi_wwan/new_id` 为 `2ca3 4006`，再写 `qmi_wwan/bind` 后可生成 `/dev/cdc-wdm0` 与 `wwan0`。
- [x] 根因不是单纯的 bind 竞态，而是 WSL USB prepare 只给 `option1/new_id` 登记了 Baiwang VID/PID，漏给 `qmi_wwan/new_id` 登记。

### 实施步骤

- [x] RED：新增 `TestPrepareWSLUSBRegistersBaiwangIDWithQMIWWANBeforeBind`，模拟未登记 `qmi_wwan/new_id` 时 bind 稳定返回 `no such device`。
- [x] GREEN：在 QMI interface bind 前确保 `qmi_wwan/new_id` 已登记 `2ca3 4006`。
- [x] 补齐既有 QMI 测试夹具中的 `qmi_wwan/new_id` 文件和断言。
- [x] 保持 ECM 路径不写 `qmi_wwan/new_id`，避免 ECM 模式被错误拉回 QMI。
- [x] 重新编译 Linux amd64 后端，同步桌面资源并部署到 WSL `/opt/vohive/bin/vohive`。

### 评审记录

- 2026-08-06 阶段 6G 验证：WSL2 `go test ./internal/device -run "PrepareWSLUSB" -count=1` 通过；WSL2 `go test ./internal/device ./internal/api -count=1` 通过；修复后的 `/opt/vohive/bin/vohive --prepare-usb` 返回 `supported_device_found=true`、`prepared=true`，设备为 `/dev/cdc-wdm0` + `wwan0` + `/dev/ttyUSB0-3`，driver 为 `qmi_wwan`。

## 阶段 6H：本机号码自动识别与手动录入

### 根因调查

- [x] 用户截图显示 DITO eSIM 详情中 IMEI/ICCID/IMSI 可读，但“本机号码”为 `--`。
- [x] 后端 UI 字段 `local_phone` 来自数据库 `sim_subscriptions.phone_number`，启动同步会通过 `GetMSISDN` 尝试写入。
- [x] 实机 `AT+CNUM` 返回成功但响应为空；`AT+CRSM` 读取 EF_MSISDN (`6F40`) 返回全 `FF`，说明当前 DITO eSIM 没把号码写入 SIM 文件。
- [x] 当前 AT 后端缺少 MBIM 已有的 EF_MSISDN fallback；即使部分 SIM 的 `AT+CNUM` 为空但 EF 有号码，AT 模式也读不到。
- [x] 对当前这张 eSIM，自动来源为空时只能通过 VoWiFi 学习、运营商专用 USSD/短信内容或用户手动录入补全，不能凭空推断。

### 实施步骤

- [x] RED：AT `QueryMSISDN` 在 `AT+CNUM` 为空时应读取 EF_MSISDN 记录并解码。
- [x] RED：手动号码应有独立字段，优先级高于 VoWiFi 和 modem 自动学习。
- [x] RED：设备详情 API 可按当前 IMSI/ICCID 保存手动本机号码。
- [x] GREEN：实现 AT fallback、数据库手动号码优先级和设备 API。
- [x] GREEN：前端设备详情提供本机号码编辑入口，保存后刷新详情。
- [x] 验证 Go 单测、Web 类型检查/构建、实机 API 保存。
- [x] 记录评审和教训。

### 版本发布准备

- [x] 本次是用户可见 bugfix，按语义化版本规则从 `1.0.2` 递增到 `1.0.3`。
- [x] README、Release workflow 默认版本、桌面 package/Tauri/Cargo 元数据已同步为 `1.0.3`。
- [x] 新增 `.github/release-notes/v1.0.3.md`，说明相对 `v1.0.2` 的更新。

### 评审记录

- 2026-08-06 阶段 6H 根因确认：用户截图中的 DITO eSIM 可读 IMEI/ICCID/IMSI，但 `AT+CNUM` 为空，EF_MSISDN (`6F40`) 读取为全 `FF`，说明该 eSIM 没把本机号码写进 SIM 文件；程序不能从 IMSI/ICCID/运营商名称反推出号码。
- 2026-08-06 阶段 6H 实现：AT 后端新增 EF_MSISDN fallback；数据库新增 `manual_phone_number` 并把最终显示优先级集中为 `manual > vowifi > modem`；新增 `PATCH /api/devices/{device_id}/local-phone`，切卡身份未确认时返回 409，空字符串用于清除手动号码；设备详情页“本机号码”旁新增编辑按钮，保存后刷新概览。
- 2026-08-06 阶段 6H 验证：`go test ./internal/modem ./internal/db ./internal/api -run "QueryMSISDNFallsBack|ManualPhone|SetLocalPhone" -count=1` 通过；`go test ./internal/modem ./internal/db ./internal/api ./internal/device -count=1` 通过；`npm run test --prefix web` 25 项通过；`npm run typecheck --prefix web` 通过；`npm run build --prefix web` 通过；`node --test tests/repositoryDocs.test.mjs` 6 项通过；桌面 `node --test tests/*.test.mjs` 12 项通过；`cargo test --manifest-path desktop/src-tauri/Cargo.toml --offline` 26 项通过；`pnpm run build` 和 `pnpm tauri build --debug` 通过。
- 2026-08-06 阶段 6H 实机验证：WSL 后端已部署并以 `1.0.3` 启动，`/api/system/info` 返回 `version=1.0.3`、`build_time=2026-08-06T05:36:53Z`；当前设备 `wwan0` 在线，`PATCH /api/devices/wwan0/local-phone` 写入测试号码 `+15550001003` 后 overview 立即显示，随后用空字符串清除，清除后 overview 回到空值，测试号码未保留。
- 2026-08-06 阶段 6H 产物重编译：已同步最新 `web/dist` 到 `internal/web/dist`；已用 `global.Version=1.0.3` 和构建时间重新编译 Linux amd64 后端 `dist/vohive-open_linux_amd64`；已同步到桌面壳资源并重新生成 debug 桌面程序 `desktop/src-tauri/target/debug/vohive-plus-desktop.exe`。
- 2026-08-06 阶段 6H 发布说明补救：复查 `git log --oneline v1.0.2..v1.0.3` 后确认 `46c3740 修复 WSL USB 准备漏登记 qmi_wwan 动态 ID` 被漏写进 `v1.0.3` 发布说明；已补充 `.github/release-notes/v1.0.3.md`，并同步更新 GitHub Release 页面正文。

## 阶段 6I：VoWiFi ePDG MNC=00 解析修复

### 根因调查

- [x] 用户截图显示 VoWiFi 启动失败：`SWU tunnel establishment failed: read udp 127.0.0.1:47024->127.0.0.1:4500: i/o timeout`。
- [x] WSL 日志显示当前 SIM 实时归属为 `mcc=454, mnc=00`，但 VoWiFi 准备画像变成 `matched_plmn=454/003`，ePDG 被派生为 `epdg.epc.mnc003.mcc454.pub.3gppnetwork.org`。
- [x] DNS 验证显示 `mnc003` 域名返回 `127.0.0.1`，而 `mnc000` 域名返回公网 ePDG 地址；因此失败不是 UI 开关或 UDP 监听缺失，而是 MNC 归一化把合法的全零二位 MNC 误当空值。

### 实施步骤

- [x] RED：新增 `TestPrepareStartPreservesAllZeroTwoDigitMNC`，锁定 `MNC=00` 时应保留为 `00` 并生成 `mnc000` ePDG。
- [x] GREEN：调整 `identity.NormalizeProfile`，仅在 MNC 完全缺失时从 IMSI 推导，不再对显式 MNC 执行 `TrimLeft("0")`。
- [x] 验证 VoWiFi identity、SWU 和项目 VoWiFi 相关测试。

### 部署记录

- [x] 2026-08-06 已重新编译 Linux amd64 后端到 `dist/vohive-open_linux_amd64`。
- [x] 2026-08-06 已用 WSL root 备份旧后端为 `/opt/vohive/bin/vohive.bak-20260806103845`，并部署新后端到 `/opt/vohive/bin/vohive`。
- [x] 2026-08-06 已停止旧进程并用隐藏 `wsl.exe` 启动新后端；`/ping` 返回 `pong`，新进程 PID 为 `8311`。
- [x] 2026-08-06 已确认 `/opt/vohive/bin/vohive` 与本地 `dist/vohive-open_linux_amd64` 的 SHA256 均为 `bd55b5b760134023a710d70c17dc54e72ae8e1a2cb56d8eea35522e325bccad9`。
