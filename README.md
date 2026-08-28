# VoHive Plus

本项目已从 [windloom/vohive-open](https://github.com/windloom/vohive-open) fork 为 [swordstudiox/vohive-plus](https://github.com/swordstudiox/vohive-plus)。

VoHive Plus 是面向 Windows + WSL2 的桌面化分支，目标是让大疆 4G 模块在 Windows 电脑上通过轻量桌面壳管理 Linux 后端、USB 直通和 Web 管理页面。

当前主线是 WSL2 路线。桌面软件会把内置的 Linux 后端运行时部署到 WSL2 的 `/opt/vohive`，再由 WSL2 负责识别蜂窝模块、建立 QMI/AT 设备节点并运行 VoHive Web 服务。

> 本项目里的“固件”指 VoHive Plus Linux 后端运行时二进制，不是大疆模块或通信模组的硬件固件，不会刷写硬件。

[![License: PolyForm Noncommercial 1.0.0](https://img.shields.io/badge/License-PolyForm--Noncommercial--1.0.0-blue.svg)](https://polyformproject.org/licenses/noncommercial/1.0.0)
[![Go](https://img.shields.io/badge/Go-1.26%2B-00ADD8?logo=go)](go.mod)
[![Vue 3](https://img.shields.io/badge/Vue-3-42b883?logo=vue.js)](web/package.json)

## Fork 说明

- 上游项目：`windloom/vohive-open`。
- 当前项目：`swordstudiox/vohive-plus`。
- 继承内容：VoHive 后端、Web 管理后台、模组管理、短信、eSIM、通知、代理等能力。
- 本项目新增重点：Windows 桌面壳、WSL2 USB 编排、大疆/Baiwang `2ca3:4006` 设备准备、Windows 便携包发布、漫游开关和本地运行体验。
- 源码整合：项目可见依赖放在 `third_party/`，用于在缺少不可用上游仓库时保持源码可构建；详情见 [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md)。

## 上游能力概览

VoHive 面向高通 4G/LTE/5G 模组，例如 Quectel EC20、EC25、EC21、EG25、EM20 等，提供模组管理、代理编排、短信收发、VoWiFi/IMS 通话、eSIM 生命周期管理和响应式 Web 管理后台。

| 模块 | 说明 |
| --- | --- |
| 多模组并发管理 | 支持 USB 热插拔发现、多设备状态监控和运行时设备管理。 |
| 轻量级代理引擎 | 支持 SOCKS5 / HTTP 代理实例，按设备网卡绑定出站流量。 |
| 通信与短信中心 | 通过统一 Web/API 管理 AT 短信收发、会话、联系人和 USSD 交互，短信可落库查询。 |
| eSIM 管理 | 通过 AT 指令通道管理 eSIM Profile，包括下载、启用、停用、重命名和删除。 |
| 全渠道通知 | 重要短信和系统告警可推送至 Telegram、Email、PushPlus、Bark、飞书、QQ 等渠道。 |
| 多架构构建 | 支持 amd64、arm64、armv7 等 Linux 运行时构建。 |

典型应用场景：

- 私有 IP 代理池：单主机挂载多张物理 SIM 卡或 eSIM，每张网卡对应独立代理实例。
- 统一接码/验证码中心：通过 Web 界面或 API 并行收发多卡短信，并推送到个人终端。
- VoWiFi 零信号通信：在地下室、弱覆盖等场景下，借助宽带网络隧道建立 IMS 连接。

## 当前能力

- Windows 桌面壳：检测 WSL2、检测 usbipd-win、枚举大疆/Baiwang `2ca3:4006` USB 设备。
- WSL2 USB 编排：连接 USB 到 WSL，准备 `/dev/ttyUSB*`、`/dev/cdc-wdm0`、`wwan0` 或 ECM 网卡。
- 后端管理：从桌面界面启动/停止 WSL2 内的 VoHive 后端，查看日志并打开 Web UI。
- Web 管理：添加设备、重新扫描、短信/卡策略等原有 VoHive 能力。
- 蜂窝策略：支持蜂窝数据开关、VoWiFi 开关、飞行模式、APN/IP 版本和漫游开关。
- Release 自动化：GitHub Actions 构建 Linux 后端运行时和 Windows x64 桌面便携包。

## 普通用户使用

### 环境要求

- Windows 10/11 x64。
- 已启用虚拟化和 WSL2，推荐发行版 `Ubuntu-24.04`。安装说明见 [Microsoft WSL 文档](https://learn.microsoft.com/windows/wsl/install)。
- 已安装 `usbipd-win`。项目主页见 [dorssel/usbipd-win](https://github.com/dorssel/usbipd-win)，安装包见 [usbipd-win Releases](https://github.com/dorssel/usbipd-win/releases)。
- 已安装 Microsoft Edge WebView2 Runtime。Windows 11 和多数 Windows 10 通常已内置；精简系统可从 [Microsoft WebView2](https://developer.microsoft.com/microsoft-edge/webview2/) 安装 Evergreen Runtime。
- 大疆 4G 模块或 Windows 侧枚举为 `2ca3:4006 Baiwang` 的设备。
- 可传输数据的 USB 线和可用 SIM 卡。

桌面便携包不会安装或配置 WSL2，也不会下载、内置或安装 `usbipd-win`。这些属于系统级运行前置环境，需要用户先准备好。普通用户无需安装 Go、Node、Rust、pnpm、Docker 或 VirtualBox。

可参考命令：

```powershell
wsl --install -d Ubuntu-24.04
winget install dorssel.usbipd-win
```

首次安装 WSL 发行版后，请先启动一次 `Ubuntu-24.04` 完成 Linux 用户初始化。如果 USB attach 或 WSL 驱动模块异常，可先执行 `wsl --update` 更新 WSL 内核后重试。

`usbipd-win` 是系统级 USB/IP 驱动和 Windows 服务，安装/更新需要管理员权限；本项目不保存它的安装包，避免分发过期驱动。`usbipd bind` 也可能需要管理员权限。如果桌面壳提示需要管理员命令，请用管理员 PowerShell 执行界面给出的命令。

### 下载与启动

1. 打开 [Releases](https://github.com/swordstudiox/vohive-plus/releases)。
2. 下载 `vohive-plus-desktop_1.0.5_windows_x64.zip`。
3. 解压到一个普通目录，例如 `D:\Apps\VoHivePlus`。
4. 双击 `vohive-plus-desktop.exe`。
5. 插入大疆 4G 模块。
6. 在桌面壳中依次执行：
   - `启动 WSL`
   - `连接 USB 到 WSL`
   - `准备 WSL USB`
   - `启动后端`
   - `打开 Web`

Web 默认地址为 `http://127.0.0.1:7575/`，默认登录账号为 `admin/admin`。

### 桌面壳按钮说明

- `启动 WSL`：启动并保活目标 WSL2 发行版。
- `连接 USB 到 WSL`：调用 `usbipd-win` 将 `2ca3:4006 Baiwang` 设备 attach 到 WSL2。WSL 未运行时只提示用户先启动，不会自动启动。
- `准备 WSL USB`：在 WSL2 内绑定所需 Linux 驱动，生成 VoHive 需要的设备节点或网卡。
- `启动后端`：把桌面包内置的 Linux 后端运行时、默认配置和 USB 准备脚本部署到 `/opt/vohive`，并启动 Web 服务。
- `停止后端`：停止 WSL2 内的 VoHive 后端进程。
- `打开 Web`：打开本机 Web 管理页面。

### 常见问题

- 找不到设备：确认 USB 线支持数据传输，并在 Windows 设备列表中能看到 `2ca3:4006 Baiwang`。
- 连接 USB 到 WSL 失败：先点击 `启动 WSL`，再重试；如提示管理员命令，请用管理员 PowerShell 执行。
- 准备 WSL USB 失败：拔插设备后按顺序重新执行 `连接 USB 到 WSL` 和 `准备 WSL USB`。
- Web 打不开：检查后端是否启动，确认端口 `7575` 未被其他程序占用。
- 桌面包不是安装包：直接解压运行即可；删除解压目录即可移除桌面程序本体。

## Release 产物

- `vohive-plus-desktop_1.0.5_windows_x64.zip`：Windows x64 桌面便携包，内含桌面壳、Linux amd64 后端运行时、默认配置和 WSL USB 准备脚本。
- `vohive-plus-firmware_1.0.5_linux_amd64`：Linux amd64 后端运行时，适用于 WSL2 或 Linux x86_64 主机。
- `vohive-plus-firmware_1.0.5_linux_arm64`：Linux arm64 后端运行时。
- `vohive-plus-firmware_1.0.5_linux_armv7`：Linux armv7 后端运行时。
- `*.sha256`：对应产物的 SHA256 校验文件。

## 开发者说明

### 技术栈

- 后端：Go 1.26.4、Gin、GORM、SQLite。
- Web：Vue 3、Vite、Element Plus。
- 桌面壳：Tauri 2、Rust、Vue 3。
- WSL 路线：Windows 调用 `wsl.exe` 和 `usbipd.exe`，Linux 侧运行 VoHive 后端。

### 本地构建后端

构建源码时允许联网拉取依赖。后端构建前需要先构建 Web 并同步到 `internal/web/dist`。日常开发可使用较快的 `npm run build --prefix web`；发布前请使用 `npm run build:check --prefix web`，它会先执行 `vue-tsc --noEmit` 再打包。

在 Windows 项目盘挂载到 WSL2 的路径（例如 `/mnt/f/...`）上执行 Web 类型检查会明显变慢。若只是构建前端资源，优先使用 Windows 原生 Node 或普通 `build` 快速路径；Release 和 CI 仍会执行完整校验。

```bash
npm ci --prefix web
npm run build:check --prefix web
rm -rf internal/web/dist
mkdir -p internal/web
cp -R web/dist internal/web/dist

GOWORK=off CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
  go build -trimpath -buildvcs=false -tags "with_utls nomsgpack" \
  -ldflags "-s -w -X 'github.com/swordstudiox/vohive-plus/internal/global.Version=1.0.5'" \
  -o dist/vohive-open_linux_amd64 ./cmd/vohive
```

### 本地构建桌面软件

桌面壳需要内置 Linux amd64 后端运行时，但该二进制不进入 Git。先按“本地构建后端”生成：

```text
dist/vohive-open_linux_amd64
```

然后在 Windows 环境执行：

```powershell
cd desktop
pnpm install
pnpm tauri build
```

`pnpm tauri build` 会先执行 `pnpm sync:backend`，把 `dist/vohive-open_linux_amd64` 同步到 Tauri 资源目录。当前项目不生成 NSIS 安装包。正式发布通过 GitHub Actions 把 `vohive-plus-desktop.exe` 和 `resources/vohive/*` 打成便携 zip。

### 测试命令

```bash
go test ./internal/api ./internal/db ./internal/device -count=1
npm run test --prefix web
npm run typecheck --prefix web
npm run build --prefix web
```

```powershell
node --test tests/*.test.mjs
cd desktop
node --test tests/*.test.mjs
pnpm run build
cd src-tauri
cargo test
```

### 发布流程

1. 更新版本号和 `.github/release-notes/vX.Y.Z.md`。
2. 提交变更。
3. 推送 `main`：

```bash
git push origin main
```

GitHub Actions 会在 `main` 推送后读取 `desktop/package.json` 中的版本号，构建后端多架构运行时、Windows 桌面便携包，并读取 `.github/release-notes/v1.0.5.md` 发布到 [vohive-plus/releases](https://github.com/swordstudiox/vohive-plus/releases)。如果需要重发历史版本，也可以手动运行 Release workflow 或推送 `vX.Y.Z` tag。

后续版本遵循语义化版本规则：

- 修复兼容性问题：递增 patch，例如 `1.0.1`。
- 增加向后兼容功能：递增 minor，例如 `1.1.0`。
- 发生破坏性变更：递增 major，例如 `2.0.0`。

## 项目状态与限制

- WSL2 是当前可运行主路线。
- VirtualBox Headless + 最小 Debian 是后续方向，当前 Release 不包含 VM 镜像。
- 桌面壳不会自动安装 WSL2、usbipd-win 或 WebView2 Runtime。
- 默认账号 `admin/admin` 和默认监听所有地址是当前版本保留行为。
- 当前 GitHub Actions 不构建或上传容器镜像。
- 请自行确认所在地区法律法规、运营商服务条款和硬件使用风险。

## 免责声明

- 用途定位：本项目主要面向个人学习、技术研究与功能测试场景，不建议直接用于生产环境或关键业务系统；由此产生的部署及使用风险由使用者自行承担。
- 非官方项目：VoHive Plus 为第三方独立开发的软件，与大疆、Quectel、高通公司及其他任何模组/芯片厂商均无官方关联、授权或合作关系，亦不对模组硬件本身的功能、质量或安全性负责。
- 合规使用：使用本项目搭建的服务时，请自行确保符合所在地区的法律法规及电信运营商的服务条款，不得用于任何违法违规用途。因违规使用造成的一切法律责任由使用者自行承担。
- 无担保：本软件按“现状”提供，不附带任何明示或暗示的担保。因使用或无法使用本软件造成的任何直接或间接损失，作者及贡献者不承担责任。

## 许可证

本源码整合树不是单一许可项目。根项目基于 [PolyForm Noncommercial License 1.0.0](LICENSE)，`third_party/vowifi-go` 使用 AGPL-3.0，`third_party/quectel-qmi-go`、`third_party/netlink`、`third_party/qqbot` 等组件按各自许可证授权。发布公开二进制前，请先确认组合分发的许可证义务；详情见 [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md)。
