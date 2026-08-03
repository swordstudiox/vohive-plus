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
- Windows Node 不能可靠复用 WSL Linux 的 `node_modules`；Web 测试和构建应明确使用 WSL 内 Linux Node，并把 `.toolchains/node/bin` 放到 `PATH` 最前。
- 在 WSL `/mnt/f` 上跑主 Web `npm run build` 可能超过 3 分钟，超时不等于构建失败；遇到无输出超时要先检查残留 `node/vite` 进程和 `dist` 更新时间，再决定是否重跑或清理。
- Tauri 桌面命令返回 `ActionResult { ok:false }` 后，前端必须显式展示 `message` 和 `suggested_admin_command`；只等待 Promise resolve 会把失败路径吞掉。
- 桌面壳持有 `Child` 句柄不代表后端仍在运行；状态刷新应使用 `try_wait()` 更新退出状态并释放句柄，否则后端异常退出后 UI 会误报运行中。
- Tauri Windows 构建要求存在 `.ico` 图标资源；即使是 MVP，也要先准备 `src-tauri/icons/icon.ico`，否则安装包构建会卡在资源校验。
- Tauri/Rust 在 Windows debug 运行时可能返回 `\\?\F:\...` 这种 verbatim path；转换给 WSL 使用前必须剥掉 `\\?\` 前缀，否则 Linux 会看到不存在的 `//?/F:/...`。
- 桌面启动按钮必须先看健康检查，`/ping` 已正常时应复用既有后端；否则用户从外部启动过 WSL 后端时，桌面壳会误以为“进程未运行”并尝试启动第二个实例。
- Windows GUI 程序如果直接打开出现黑框，检查 PE subsystem；Rust/Tauri 入口可用 `#![cfg_attr(windows, windows_subsystem = "windows")]` 隐藏 console。

## 2026-08-03 Git 提交规范

- 每次本地提交都要编写详细中文提交说明，至少包含本次变更的背景、核心改动、验证命令和已知剩余风险；不要只写一句泛泛的 `update` 或 `fix`。
