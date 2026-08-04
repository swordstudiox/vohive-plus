package api

import (
	"errors"
	"net/http"
	"os"
	"os/exec"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/swordstudiox/vohive-plus/internal/config"
	"github.com/swordstudiox/vohive-plus/internal/updater"
	"github.com/swordstudiox/vohive-plus/pkg/logger"
)

var errNotFound = errors.New("not found")

// resolveUninstallTargets 计算自毁流程需要清理的数据目录和配置文件路径。
// 配置文件路径必须来自运行时实际加载的路径（config.GetConfigPath()），
// 不能假定其固定位于进程工作目录下的 "config" 子目录——
// OpenWrt 部署通过 -c 显式传入 /etc/vohive/config.yaml，与工作目录无关，
// 用硬编码相对路径删除会删错地方（实际等于什么都没删）。
// configPath 为空（配置管理器未初始化）时不返回任何配置文件路径，避免误删。
func resolveUninstallTargets(configPath string) (dataDir string, configFile string) {
	return "data", configPath
}

// detectServiceStopCommands 根据当前部署形态返回应执行的"停止 + 禁用自启"命令。
// systemd 的 Restart=always 和 OpenWrt procd 的 respawn 都只在进程
// "非主动" 退出时才会重新拉起；只要在自毁前显式请求服务管理器停止/禁用，
// 即使后续删除可执行文件失败（例如只读 squashfs），也不会被重新拉起。
// 仅靠"删掉自己导致 exec 失败"这种副作用来阻止重启是不可靠的。
func detectServiceStopCommands(lookPath func(string) (string, error), statFile func(string) bool) [][]string {
	var cmds [][]string
	if statFile("/etc/init.d/vohive") {
		cmds = append(cmds, []string{"/etc/init.d/vohive", "disable"})
		cmds = append(cmds, []string{"/etc/init.d/vohive", "stop"})
		return cmds
	}
	if _, err := lookPath("systemctl"); err == nil {
		cmds = append(cmds, []string{"systemctl", "disable", "--now", "vohive"})
	}
	return cmds
}

// handleCheckUpdate 检查系统更新
func (s *Server) handleCheckUpdate(c *gin.Context) {
	info, err := updater.CheckUpdate()
	if err != nil {
		logger.Error("检查系统更新失败", "err", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, info)
}

// handleApplyUpdate 应用系统更新
func (s *Server) handleApplyUpdate(c *gin.Context) {
	if err := updater.ApplyUpdate(); err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, updater.ErrDisabled) {
			status = http.StatusConflict
		} else {
			logger.Error("应用更新失败", "err", err)
		}
		c.JSON(status, gin.H{
			"status":  "error",
			"message": err.Error(),
		})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "正在后台下载更新，系统稍后将自动重启..."})
}

// handleUninstall 自毁/卸载接口，用于用户拒绝免责声明时
// 必须登录后才能调用——这是真正会删数据、删配置、删自身可执行文件并
// os.Exit(0) 的破坏性操作，绝不能允许未鉴权请求触发。
func (s *Server) handleUninstall(c *gin.Context) {
	if !s.isAuthenticatedRequest(c, time.Now()) {
		c.JSON(http.StatusUnauthorized, gin.H{
			"status":     "error",
			"code":       "unauthorized",
			"message":    "未授权",
			"request_id": requestID(c),
		})
		return
	}

	logger.Warn("用户拒绝了免责声明，正在触发自毁/卸载逻辑")
	c.JSON(http.StatusOK, gin.H{"message": "正在卸载软件..."})

	// 在后台异步执行自毁，以免请求无法返回
	go func() {
		time.Sleep(1 * time.Second)

		// 先主动通知服务管理器停止 + 禁用自启，确保即使后面的文件删除
		// 失败（只读文件系统等），systemd Restart=always / procd respawn
		// 也不会把进程重新拉起来。命令异步触发(Start 不 Wait)，
		// 避免对"停止自己"这条命令的等待造成死锁。
		for _, args := range detectServiceStopCommands(exec.LookPath, fileExists) {
			cmd := exec.Command(args[0], args[1:]...)
			if err := cmd.Start(); err != nil {
				logger.Warn("通知服务管理器停止失败", "cmd", args, "err", err)
				continue
			}
			go cmd.Wait()
		}

		dataDir, configFile := resolveUninstallTargets(config.GetConfigPath())

		if err := os.RemoveAll(dataDir); err != nil {
			logger.Warn("清理数据目录失败", "dir", dataDir, "err", err)
		}
		if configFile != "" {
			if err := os.Remove(configFile); err != nil && !os.IsNotExist(err) {
				logger.Warn("清理配置文件失败", "file", configFile, "err", err)
			}
		}
		if executable, err := os.Executable(); err == nil {
			if err := os.Remove(executable); err != nil {
				logger.Warn("删除可执行文件失败", "file", executable, "err", err)
			}
		}

		logger.Warn("自毁流程结束，退出进程")
		os.Exit(0)
	}()
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
