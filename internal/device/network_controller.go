package device

import (
	mbimcore "github.com/swordstudiox/vohive-plus/internal/mbim"
	qmicore "github.com/swordstudiox/vohive-plus/internal/qmi"
)

type NetworkController interface {
	Connect() error
	Disconnect() error
	IsConnected() bool
	RotateIP() error
	GetPrivateIP() string
	GetPrivateIPv6() string
	GetPublicIPv4AndV6NoCache() (publicV4 string, publicV6 string)
}

var (
	_ NetworkController = (*qmicore.Manager)(nil)
	_ NetworkController = (*mbimcore.Manager)(nil)
)

func (w *Worker) NetworkController() NetworkController {
	if w == nil {
		return nil
	}
	if w.netOverride != nil {
		return w.netOverride
	}
	if w.QMICore != nil {
		return w.QMICore
	}
	if w.MBIMCore != nil {
		return w.MBIMCore
	}
	return nil
}
