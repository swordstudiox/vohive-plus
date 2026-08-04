package device

import (
	"context"
	"strings"
	"time"

	"github.com/swordstudiox/vohive-plus/internal/modem"
)

// ATRadioQuerier 定义了通过 AT 通道查询射频及网络信息的接口
type ATRadioQuerier interface {
	QueryOperator() (string, error)                             // 查询当前驻留的运营商名称
	QueryRegistration() (int, string, string, string, error)    // 查询网络注册状态及相关网络标号
	QueryCSQ() (int, int, error)                                // 查询信号强度指标 (CSQ/dBm)
	QueryServingCellLTEInfo() (modem.ServingCellLTEInfo, error) // 查询当前 LTE 主小区的详细频段与测量指标
	QueryNetworkRadio() (string, string, string, uint32, error) // 查询网络制式、双工模式、频段及频点
}

// ATRadioReadOptions 包含读取 AT 射频状态时的重试控制选项
type ATRadioReadOptions struct {
	Attempts int           // 失败或读取时的最大尝试次数
	Delay    time.Duration // 两次尝试之间的等待间隔时间
}

// ATRadioSnapshot 存储单次或多次查询后获取的设备射频状态快照数据（字段均使用指针以区分未获取和零值）
type ATRadioSnapshot struct {
	Operator      *string // 运营商名称
	SignalDBM     *int    // 信号强度指示 (dBm)
	SignalRSRP    *int    // 4G/5G 接收信号参考功率 (RSRP)
	SignalRSRQ    *int    // 4G/5G 接收信号参考质量 (RSRQ)
	SignalSINR    *int    // 4G/5G 信号与干扰加噪声比 (SINR)
	RegStatus     *int    // 网络注册状态码
	RegStatusText *string // 网络注册状态描述文本
	NetworkMode   *string // 驻网网络技术模式 (如 LTE)
	NetworkDuplex *string // 网络双工模式 (如 FDD/TDD)
	RadioBand     *string // 当前射频频段 (Band)
	RadioChannel  *uint32 // 工作频点
}

// ptrString 将非空字符串包装为指针返回；若字符串为空，则返回 nil
func ptrString(v string) *string {
	v = strings.TrimSpace(v)
	if v == "" {
		return nil
	}
	return &v
}

// ptrInt 将整数包装为指针返回
func ptrInt(v int) *int {
	return &v
}

// ptrUint32 将非零 uint32 包装为指针返回；若为 0，则返回 nil
func ptrUint32(v uint32) *uint32 {
	if v == 0 {
		return nil
	}
	return &v
}

// normalizeATRadioReadOptions 标准化射频读取配置，补充默认值
func normalizeATRadioReadOptions(opts ATRadioReadOptions) ATRadioReadOptions {
	if opts.Attempts <= 0 {
		opts.Attempts = 1
	}
	if opts.Delay < 0 {
		opts.Delay = 0
	}
	return opts
}

// ReadATRadioSnapshot 根据配置的重试策略和上下文，安全地通过 Querier 读取当前射频指标的完整快照
func ReadATRadioSnapshot(ctx context.Context, q ATRadioQuerier, opts ATRadioReadOptions) ATRadioSnapshot {
	opts = normalizeATRadioReadOptions(opts)
	if ctx == nil {
		ctx = context.Background()
	}
	var out ATRadioSnapshot
	if q == nil {
		return out
	}
	for attempt := 0; attempt < opts.Attempts; attempt++ {
		if attempt > 0 && opts.Delay > 0 {
			timer := time.NewTimer(opts.Delay)
			select {
			case <-ctx.Done():
				timer.Stop()
				return out
			case <-timer.C:
			}
		}
		readATRadioSnapshotOnce(q, &out)
	}
	return out
}

// readATRadioSnapshotOnce 执行单次 AT 查询操作，并将成功获取的射频属性写入快照中
func readATRadioSnapshotOnce(q ATRadioQuerier, out *ATRadioSnapshot) {
	if _, dbm, err := q.QueryCSQ(); err == nil && dbm != -999 {
		out.SignalDBM = ptrInt(dbm)
	}
	if reg, text, _, _, err := q.QueryRegistration(); err == nil {
		out.RegStatus = ptrInt(reg)
		if v := ptrString(text); v != nil {
			out.RegStatusText = v
		}
	}
	if cell, err := q.QueryServingCellLTEInfo(); err == nil {
		if cell.RSRP != 0 {
			out.SignalRSRP = ptrInt(cell.RSRP)
		}
		if cell.RSRQ != 0 {
			out.SignalRSRQ = ptrInt(cell.RSRQ)
		}
		if cell.SINR != 0 {
			out.SignalSINR = ptrInt(cell.SINR)
		}
		if v := ptrString(cell.Duplex); v != nil {
			out.NetworkDuplex = v
		}
		if v := ptrString(cell.Band); v != nil {
			out.RadioBand = v
		}
		if v := ptrUint32(cell.Channel); v != nil {
			out.RadioChannel = v
		}
		if cell.Channel != 0 && out.NetworkMode == nil {
			mode := "LTE"
			out.NetworkMode = &mode
		}
	}
	if mode, duplex, band, channel, err := q.QueryNetworkRadio(); err == nil {
		if v := ptrString(mode); v != nil {
			out.NetworkMode = v
		}
		if v := ptrString(duplex); v != nil {
			out.NetworkDuplex = v
		}
		if v := ptrString(band); v != nil {
			out.RadioBand = v
		}
		if v := ptrUint32(channel); v != nil {
			out.RadioChannel = v
		}
	}
	if operator, err := q.QueryOperator(); err == nil {
		if v := ptrString(operator); v != nil {
			out.Operator = v
		}
	}
}

// ApplyToStatus 将当前射频快照中所有非 nil 的字段覆盖应用到目标 `modem.DeviceStatus` 状态结构中
func (s ATRadioSnapshot) ApplyToStatus(status modem.DeviceStatus) modem.DeviceStatus {
	if s.Operator != nil {
		status.Operator = *s.Operator
	}
	if s.SignalDBM != nil {
		status.SignalDBM = *s.SignalDBM
	}
	if s.SignalRSRP != nil {
		status.SignalRSRP = *s.SignalRSRP
	}
	if s.SignalRSRQ != nil {
		status.SignalRSRQ = *s.SignalRSRQ
	}
	if s.SignalSINR != nil {
		status.SignalSINR = *s.SignalSINR
	}
	if s.RegStatus != nil {
		status.RegStatus = *s.RegStatus
	}
	if s.RegStatusText != nil {
		status.RegStatusText = *s.RegStatusText
	}
	if s.NetworkMode != nil {
		status.NetworkMode = *s.NetworkMode
	}
	if s.NetworkDuplex != nil {
		status.NetworkDuplex = *s.NetworkDuplex
	}
	if s.RadioBand != nil {
		status.RadioBand = *s.RadioBand
	}
	if s.RadioChannel != nil {
		status.RadioChannel = *s.RadioChannel
	}
	return status
}
