package main

import (
	_ "github.com/mewsen/rclone-studip-backend-oot/backend/studip"
	_ "github.com/rclone/rclone/backend/all"
	"github.com/rclone/rclone/cmd"
	_ "github.com/rclone/rclone/cmd/all"
	_ "github.com/rclone/rclone/lib/plugin"
)

func main() {
	cmd.Main()
}
