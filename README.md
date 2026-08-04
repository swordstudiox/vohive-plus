# VoHive Plus

VoHive Plus 是基于 `windloom/vohive-open` 的 Windows + WSL2 桌面化分支，目标是让大疆 4G 模块在 Windows 电脑上通过轻量桌面壳管理 Linux 后端、USB 直通和 Web 管理页面。

当前主线是 WSL2 路线。桌面软件会把内置的 Linux 后端运行时部署到 WSL2 的 `/opt/vohive`，再由 WSL2 负责识别蜂窝模块、建立 QMI/AT 设备节点并运行 VoHive Web 服务。

> 本项目里的“固件”指 VoHive Plus Linux 后端运行时二进制，不是大疆模块或通信模组的硬件固件，不会刷写硬件。

## 当前能力

- Windows 桌面壳：检测 WSL2、检测 usbipd-win、枚举大疆/Baiwang `2ca3:4006` USB 设备。
- WSL2 USB 编排：连接 USB 到 WSL，准备 `/dev/ttyUSB*`、`/dev/cdc-wdm0`、`wwan0`。
- 后端管理：从桌面界面启动/停止 WSL2 内的 VoHive 后端，查看日志并打开 Web UI。
- Web 管理：添加设备、重新扫描、短信/卡策略等原有 VoHive 能力。
- 蜂窝策略：支持蜂窝数据开关、VoWiFi 开关、飞行模式、APN/IP 版本和漫游开关。
- Release 自动化：GitHub Actions 构建 Linux 后端运行时和 Windows x64 桌面便携包。

## 普通用户使用

### 环境要求

- Windows 10/11 x64。
- 已启用虚拟化和 WSL2，推荐发行版 `Ubuntu-24.04`。
- 已安装 `usbipd-win`。
- 大疆 4G 模块或 Windows 侧枚举为 `2ca3:4006 Baiwang` 的设备。
- 可传输数据的 USB 线和可用 SIM 卡。

桌面便携包不会安装或配置 WSL2，也不会安装 `usbipd-win`。这些属于运行前置环境，需要用户先准备好。

可参考命令：

```powershell
wsl --install -d Ubuntu-24.04
winget install dorssel.usbipd-win
```

`usbipd bind` 可能需要管理员权限。如果桌面壳提示需要管理员命令，请用管理员 PowerShell 执行界面给出的命令。

### 下载与启动

1. 打开 [Releases](https://github.com/swordstudiox/vohive-plus/releases)。
2. 下载 `vohive-plus-desktop_1.0.0_windows_x64.zip`。
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
- `准备 WSL USB`：在 WSL2 内绑定 `option` 和 `qmi_wwan` 驱动，生成 VoHive 需要的设备节点。
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

- `vohive-plus-desktop_1.0.0_windows_x64.zip`：Windows x64 桌面便携包，内含桌面壳、Linux amd64 后端运行时、默认配置和 WSL USB 准备脚本。
- `vohive-plus-firmware_1.0.0_linux_amd64`：Linux amd64 后端运行时，适用于 WSL2 或 Linux x86_64 主机。
- `vohive-plus-firmware_1.0.0_linux_arm64`：Linux arm64 后端运行时。
- `vohive-plus-firmware_1.0.0_linux_armv7`：Linux armv7 后端运行时。
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
  -ldflags "-s -w -X 'github.com/swordstudiox/vohive-plus/internal/global.Version=1.0.0'" \
  -o dist/vohive-plus-firmware_1.0.0_linux_amd64 ./cmd/vohive
```

### 本地构建桌面软件

桌面壳需要内置 Linux amd64 后端运行时。先确认下面文件存在：

```text
desktop/src-tauri/resources/vohive/vohive-open_linux_amd64
desktop/src-tauri/resources/vohive/config.example.yaml
desktop/src-tauri/resources/vohive/vohive-usb-prepare.sh
```

然后在 Windows 环境执行：

```powershell
cd desktop
pnpm install
pnpm tauri build
```

当前项目不生成 NSIS 安装包。正式发布通过 GitHub Actions 把 `vohive-plus-desktop.exe` 和 `resources/vohive/*` 打成便携 zip。

### 测试命令

```bash
go test ./internal/api ./internal/db ./internal/device -count=1
npm run test --prefix web
npm run typecheck --prefix web
npm run build --prefix web
```

```powershell
cd desktop
node --test tests/*.test.mjs
pnpm run build
cd src-tauri
cargo test
```

### 发布流程

1. 更新版本号和 `.github/release-notes/vX.Y.Z.md`。
2. 提交变更。
3. 创建 tag 并推送：

```bash
git tag -a v1.0.0 -m "发布 v1.0.0"
git push origin main
git push origin v1.0.0
```

GitHub Actions 会构建后端多架构运行时、Windows 桌面便携包，并读取 `.github/release-notes/v1.0.0.md` 发布到 [vohive-plus/releases](https://github.com/swordstudiox/vohive-plus/releases)。

后续版本遵循语义化版本规则：

- 修复兼容性问题：递增 patch，例如 `1.0.1`。
- 增加向后兼容功能：递增 minor，例如 `1.1.0`。
- 发生破坏性变更：递增 major，例如 `2.0.0`。

## 项目状态与限制

- WSL2 是当前可运行主路线。
- VirtualBox Headless + 最小 Debian 是后续方向，当前 Release 不包含 VM 镜像。
- 桌面壳不会自动安装 WSL2 或 usbipd-win。
- 默认账号 `admin/admin` 和默认监听所有地址是当前版本保留行为。
- 请自行确认所在地区法律法规、运营商服务条款和硬件使用风险。

## 许可证

根项目基于 [PolyForm Noncommercial License 1.0.0](LICENSE)。本源码整合树包含多个第三方组件，详情见 [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md)。
