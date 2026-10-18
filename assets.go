package main

import (
	"embed"
	"encoding/json"

	"github.com/spotbye/SpotiFLAC/backend"
)

//go:embed all:frontend/dist
var assets embed.FS

//go:embed wails.json
var wailsJSON []byte

func applyAppVersion() {
	type wailsInfo struct {
		Info struct {
			ProductVersion string `json:"productVersion"`
		} `json:"info"`
	}
	var config wailsInfo
	if err := json.Unmarshal(wailsJSON, &config); err == nil && config.Info.ProductVersion != "" {
		backend.AppVersion = config.Info.ProductVersion
	}
}
