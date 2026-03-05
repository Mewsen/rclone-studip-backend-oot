//go:build mage
// +build mage

package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
)

const (
	modulePath = "github.com/mewsen/rclone-studip-backend-oot"
	outputDir  = "build"
	pluginName = "librcloneplugin_backend_studip.so"
	binaryName = "rclone-studip"
)

// Default target when running mage without arguments.
var Default = Build

// Build compiles both plugin and standalone binaries.
func Build() error {
	if err := Plugin(); err != nil {
		return err
	}

	return Standalone()
}

// Plugin builds the Stud.IP backend as an rclone plugin.
func Plugin() error {
	if runtime.GOOS == "windows" {
		return fmt.Errorf("go plugin buildmode is not supported on windows")
	}

	tmpDir, err := os.MkdirTemp("", "rclone-studip-plugin-*")
	if err != nil {
		return fmt.Errorf("create temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	wrapper := filepath.Join(tmpDir, "main.go")
	src := fmt.Sprintf(
		"package main\n\nimport _ %q\n\nfunc main() {}\n",
		modulePath+"/backend/studip",
	)
	if err := os.WriteFile(wrapper, []byte(src), 0o600); err != nil {
		return fmt.Errorf("write plugin wrapper: %w", err)
	}

	if err := ensureOutputDir(); err != nil {
		return err
	}

	return run("go", "build", "--buildmode=plugin", "-o", pluginOutputPath(), wrapper)
}

// Standalone builds the custom rclone binary with the backend included.
func Standalone() error {
	if err := ensureOutputDir(); err != nil {
		return err
	}

	return run("go", "build", "-o", binaryOutputPath(), ".")
}

// Clean removes build artifacts produced by this magefile.
func Clean() error {
	for _, artifact := range []string{pluginOutputPath(), binaryOutputPath()} {
		if err := os.Remove(artifact); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("remove %q: %w", artifact, err)
		}
	}

	return nil
}

func ensureOutputDir() error {
	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		return fmt.Errorf("create output dir %q: %w", outputDir, err)
	}
	return nil
}

func pluginOutputPath() string { return filepath.Join(outputDir, pluginName) }

func binaryOutputPath() string { return filepath.Join(outputDir, binaryName) }

func run(name string, args ...string) error {
	fmt.Printf(">> %s %s\n", name, shellJoin(args))

	cmd := exec.Command(name, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("command failed: %s %s: %w", name, shellJoin(args), err)
	}

	return nil
}

func shellJoin(parts []string) string {
	if len(parts) == 0 {
		return ""
	}

	out := make([]byte, 0, 128)
	for i, p := range parts {
		if i > 0 {
			out = append(out, ' ')
		}
		if needsQuoting(p) {
			out = append(out, '"')
			for _, r := range p {
				if r == '"' || r == '\\' {
					out = append(out, '\\')
				}
				out = append(out, byte(r))
			}
			out = append(out, '"')
			continue
		}
		out = append(out, p...)
	}
	return string(out)
}

func needsQuoting(s string) bool {
	if s == "" {
		return true
	}
	for _, r := range s {
		switch r {
		case ' ', '\t', '\n', '"', '\'':
			return true
		}
	}
	return false
}
