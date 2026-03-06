//go:build mage
// +build mage

package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

const (
	outputDir    = "build"
	pluginName   = "librcloneplugin_backend_studip.so"
	binaryName   = "rclone-studip"
	goExperiment = "nodwarf5"

	rcloneModulePath = "github.com/rclone/rclone"
	studipModulePath = "github.com/mewsen/rclone-studip-backend-oot"

	pluginBuildTreeDir  = "rclone"
	pluginModFileName   = "studip.plugin.mod"
	pluginSumFileName   = "studip.plugin.sum"
	pluginEntrypointDir = "cmd/studipplugin"
	pluginEntrypointPkg = "./cmd/studipplugin"
	pluginEntrypointSrc = "package main\n\nimport _ \"github.com/mewsen/rclone-studip-backend-oot/backend/studip\"\n"
)

// Default target when running mage without arguments.
var Default = All

// All builds both plugin and standalone binaries.
func All() error {
	if err := Plugin(); err != nil {
		return err
	}
	return Standalone()
}

// Plugin builds the Stud.IP backend as an rclone plugin by building from
// inside a copied rclone main module tree. This keeps package hashes compatible
// with plugin loading requirements.
func Plugin() error {
	if runtime.GOOS == "windows" {
		return fmt.Errorf("go plugin buildmode is not supported on windows")
	}

	if err := ensureOutputDir(); err != nil {
		return err
	}

	repoRoot, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("resolve repo root: %w", err)
	}
	repoRoot, err = filepath.Abs(repoRoot)
	if err != nil {
		return fmt.Errorf("resolve absolute repo root: %w", err)
	}

	rcloneModuleDir, err := goModuleDir(rcloneModulePath)
	if err != nil {
		return err
	}

	buildTree := pluginBuildTreePath()
	if err := os.RemoveAll(buildTree); err != nil {
		return fmt.Errorf("remove old plugin build tree: %w", err)
	}
	if err := copyDirWritable(rcloneModuleDir, buildTree); err != nil {
		return fmt.Errorf("copy rclone module tree: %w", err)
	}

	if err := writePluginEntrypoint(buildTree); err != nil {
		return err
	}
	if err := preparePluginModfile(buildTree, repoRoot); err != nil {
		return err
	}

	return runInDirWithEnv(
		buildTree,
		[]string{"GOEXPERIMENT=" + goExperiment},
		"go", "build",
		"-modfile="+pluginModFileName,
		"-trimpath",
		"-buildmode=plugin",
		"-o", filepath.Join(repoRoot, pluginOutputPath()),
		pluginEntrypointPkg,
	)
}

// InstallPlugin copies the built plugin into rclone's plugin directory.
func InstallPlugin() error {
	if err := Plugin(); err != nil {
		return err
	}

	dst := installedPluginPath()
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return fmt.Errorf("create plugin dir: %w", err)
	}

	if err := copyFile(pluginOutputPath(), dst); err != nil {
		return fmt.Errorf("install plugin: %w", err)
	}

	fmt.Printf(">> installed plugin to %s\n", dst)
	return nil
}

// UninstallPlugin removes the plugin from rclone's plugin directory.
func UninstallPlugin() error {
	dst := installedPluginPath()
	if err := os.Remove(dst); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove installed plugin %q: %w", dst, err)
	}
	fmt.Printf(">> removed plugin from %s\n", dst)
	return nil
}

// Standalone builds the custom rclone binary with the backend included.
func Standalone() error {
	if err := ensureOutputDir(); err != nil {
		return err
	}

	return runWithEnv(
		[]string{"GOEXPERIMENT=" + goExperiment},
		"go", "build", "-o", binaryOutputPath(), ".",
	)
}

// RunStandalone runs the standalone binary with plugin loading disabled.
// This avoids auto-loading ~/.local/share/rclone/plugins/*.so.
func RunStandalone(args ...string) error {
	if err := Standalone(); err != nil {
		return err
	}

	emptyPluginDir, err := os.MkdirTemp("", "rclone-empty-plugins-*")
	if err != nil {
		return fmt.Errorf("create empty plugin dir: %w", err)
	}
	defer os.RemoveAll(emptyPluginDir)

	return runCommandWithEnv(
		[]string{"RCLONE_PLUGIN_PATH=" + emptyPluginDir},
		binaryOutputPath(),
		args...,
	)
}

// Clean removes build artifacts produced by this magefile.
func Clean() error {
	for _, artifact := range []string{pluginOutputPath(), binaryOutputPath()} {
		if err := os.Remove(artifact); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("remove %q: %w", artifact, err)
		}
	}
	if err := os.RemoveAll(pluginBuildTreePath()); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove %q: %w", pluginBuildTreePath(), err)
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

func pluginBuildTreePath() string { return filepath.Join(outputDir, pluginBuildTreeDir) }

func installedPluginPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(".local", "share", "rclone", "plugins", pluginName)
	}
	return filepath.Join(home, ".local", "share", "rclone", "plugins", pluginName)
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer func() {
		_ = out.Close()
	}()

	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Sync()
}

func runWithEnv(extraEnv []string, name string, args ...string) error {
	return runCommandWithEnv(extraEnv, name, args...)
}

func runInDirWithEnv(dir string, extraEnv []string, name string, args ...string) error {
	fmt.Printf(">> (cd %s && %s %s)\n", dir, name, shellJoin(args))
	if len(extraEnv) > 0 {
		fmt.Printf(">> env %s\n", shellJoin(extraEnv))
	}

	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), extraEnv...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("command failed in %s: %s %s: %w", dir, name, shellJoin(args), err)
	}

	return nil
}

func runCommandWithEnv(extraEnv []string, name string, args ...string) error {
	fmt.Printf(">> %s %s\n", name, shellJoin(args))
	if len(extraEnv) > 0 {
		fmt.Printf(">> env %s\n", shellJoin(extraEnv))
	}

	cmd := exec.Command(name, args...)
	cmd.Env = append(os.Environ(), extraEnv...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("command failed: %s %s: %w", name, shellJoin(args), err)
	}

	return nil
}

func goModuleDir(module string) (string, error) {
	cmd := exec.Command("go", "list", "-m", "-f", "{{.Dir}}", module)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("go list module dir for %q failed: %w\n%s", module, err, strings.TrimSpace(string(out)))
	}
	dir := strings.TrimSpace(string(out))
	if dir != "" {
		return dir, nil
	}

	// In fresh CI environments, "go list -m" can return an empty Dir for
	// dependencies until they are downloaded.
	cmd = exec.Command("go", "mod", "download", "-json", module)
	out, err = cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("go mod download for %q failed: %w\n%s", module, err, strings.TrimSpace(string(out)))
	}

	var meta struct {
		Dir string `json:"Dir"`
	}
	if err := json.Unmarshal(out, &meta); err != nil {
		return "", fmt.Errorf("parse go mod download output for %q: %w\n%s", module, err, strings.TrimSpace(string(out)))
	}
	if strings.TrimSpace(meta.Dir) == "" {
		return "", fmt.Errorf("empty module dir for %q", module)
	}
	return strings.TrimSpace(meta.Dir), nil
}

func copyDirWritable(src, dst string) error {
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return err
	}

	return filepath.WalkDir(src, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}

		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}

		dstPath := filepath.Join(dst, rel)
		info, err := d.Info()
		if err != nil {
			return err
		}

		if d.IsDir() {
			return os.MkdirAll(dstPath, 0o755)
		}

		if info.Mode()&os.ModeSymlink != 0 {
			target, err := os.Readlink(path)
			if err != nil {
				return err
			}
			return os.Symlink(target, dstPath)
		}

		if !info.Mode().IsRegular() {
			return nil
		}

		if err := copyFile(path, dstPath); err != nil {
			return err
		}

		perm := os.FileMode(0o644)
		if info.Mode()&0o111 != 0 {
			perm = 0o755
		}
		return os.Chmod(dstPath, perm)
	})
}

func writePluginEntrypoint(buildTree string) error {
	entryDir := filepath.Join(buildTree, pluginEntrypointDir)
	if err := os.MkdirAll(entryDir, 0o755); err != nil {
		return fmt.Errorf("create plugin entrypoint dir: %w", err)
	}
	entryFile := filepath.Join(entryDir, "main.go")
	if err := os.WriteFile(entryFile, []byte(pluginEntrypointSrc), 0o644); err != nil {
		return fmt.Errorf("write plugin entrypoint: %w", err)
	}
	return nil
}

func preparePluginModfile(buildTree, repoRoot string) error {
	modFile := filepath.Join(buildTree, pluginModFileName)
	sumFile := filepath.Join(buildTree, pluginSumFileName)

	if err := copyFile(filepath.Join(buildTree, "go.mod"), modFile); err != nil {
		return fmt.Errorf("copy plugin modfile: %w", err)
	}
	if err := copyFile(filepath.Join(buildTree, "go.sum"), sumFile); err != nil {
		return fmt.Errorf("copy plugin sumfile: %w", err)
	}

	if err := runInDirWithEnv(
		buildTree,
		nil,
		"go", "mod", "edit",
		"-modfile="+pluginModFileName,
		"-require="+studipModulePath+"@v0.0.0",
	); err != nil {
		return fmt.Errorf("configure plugin modfile require: %w", err)
	}

	if err := runInDirWithEnv(
		buildTree,
		nil,
		"go", "mod", "edit",
		"-modfile="+pluginModFileName,
		"-replace="+studipModulePath+"="+repoRoot,
	); err != nil {
		return fmt.Errorf("configure plugin modfile replace: %w", err)
	}

	if err := runInDirWithEnv(
		buildTree,
		[]string{"GOEXPERIMENT=" + goExperiment},
		"go", "mod", "tidy",
		"-modfile="+pluginModFileName,
	); err != nil {
		return fmt.Errorf("tidy plugin modfile: %w", err)
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
