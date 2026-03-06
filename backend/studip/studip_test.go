package studip_test

import (
	"testing"

	"github.com/mewsen/rclone-studip-backend-oot/backend/studip"
	"github.com/mewsen/rclone-studip-backend-oot/fstest/fstests"
)

// TestIntegration runs integration tests against the remote
func TestIntegration(t *testing.T) {
	fstests.Run(t, &fstests.Opt{
		RemoteName: "TestStudIP:rclone",
		NilObject:  (*studip.Object)(nil),
	})
}
