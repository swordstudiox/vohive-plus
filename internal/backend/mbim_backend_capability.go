package backend

import "github.com/swordstudiox/vohive-plus/pkg/mbim"

func (b *MBIMBackend) Capability() *mbim.Capabilities {
	return b.source.Capability()
}
