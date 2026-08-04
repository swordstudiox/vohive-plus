package updater

import (
	"errors"
	"os"

	"github.com/swordstudiox/vohive-plus/internal/global"
)

var ErrDisabled = errors.New("in-app binary updates are disabled for this source-integrated build")

type UpdateInfo struct {
	HasUpdate   bool   `json:"has_update"`
	CurrentVer  string `json:"current_version"`
	LatestVer   string `json:"latest_version"`
	ReleaseNote string `json:"release_note"`
	IsDocker    bool   `json:"is_docker"`
}

// CheckUpdate reports no in-app updates for this source-integrated build.
// Release binaries and Docker images should be updated through repository
// releases or container image rollout, not by hot-replacing the running binary.
func CheckUpdate() (*UpdateInfo, error) {
	currentVersion := global.Version
	if currentVersion == "" {
		currentVersion = "unknown"
	}

	isDocker := false
	if _, err := os.Stat("/.dockerenv"); err == nil {
		isDocker = true
	}

	return &UpdateInfo{
		HasUpdate:   false,
		CurrentVer:  currentVersion,
		LatestVer:   currentVersion,
		ReleaseNote: "In-app binary updates are disabled for this source-integrated build.",
		IsDocker:    isDocker,
	}, nil
}

// ApplyUpdate is disabled for source-integrated builds.
func ApplyUpdate() error {
	return ErrDisabled
}
