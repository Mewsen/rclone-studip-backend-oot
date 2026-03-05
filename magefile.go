//go:build mage
// +build mage

package main

import (
	"encoding/json"
	"errors"
	"fmt"
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

	pluginModFileName   = "studip.plugin.mod"
	pluginSumFileName   = "studip.plugin.sum"
	pluginBuildTreeDir  = "rclone"
	pluginEntrypointDir = "cmd/studipplugin"
	pluginEntrypointSrc = "package main\n\nimport _ \"github.com/mewsen/rclone-studip-backend-oot/backend/studip\"\n"
)

var Default = BuildPluginAndStandaloneBinary

func BuildPluginAndStandaloneBinary() error {
	if err := Plugin(); err != nil {
		return err
	}
	return Standalone()
}

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
		"./"+pluginEntrypointDir,
	)
}

func InstallPlugin() error {
	if err := Plugin(); err != nil {
		return err
	}

	dst, err := installedPluginPath()
	if err != nil {
		return err
	}

	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return fmt.Errorf("create plugin dir: %w", err)
	}

	data, err := os.ReadFile(pluginOutputPath())
	if err != nil {
		return err
	}

	err = os.WriteFile(dst, data, 0o644)
	if err != nil {
		return err
	}

	fmt.Printf(">> installed plugin to %s\n", dst)
	return nil
}

func UninstallPlugin() error {
	dst, err := installedPluginPath()
	if err != nil {
		return err
	}

	if err := os.Remove(dst); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove installed plugin %q: %w", dst, err)
	}

	fmt.Printf(">> removed plugin from %s\n", dst)

	return nil
}

func Standalone() error {
	if err := ensureOutputDir(); err != nil {
		return err
	}

	return runCommandWithEnv(
		[]string{"GOEXPERIMENT=" + goExperiment},
		"go", "build", "-o", binaryOutputPath(), ".",
	)
}

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

func installedPluginPath() (string, error) {
	if p := os.Getenv("RCLONE_PLUGIN_PATH"); p != "" {
		return filepath.Join(p, pluginName), nil
	}

	return "", errors.New("RCLONE_PLUGIN_PATH not set")
}

func runInDirWithEnv(dir string, extraEnv []string, name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), extraEnv...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("command failed in %s: %s %s: %w", dir, name, strings.Join(args, " "), err)
	}

	return nil
}

func runCommandWithEnv(extraEnv []string, name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Env = append(os.Environ(), extraEnv...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("command failed: %s %s: %w", name, strings.Join(args, " "), err)
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

		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}

		return os.WriteFile(dstPath, data, 0o644)
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

	data, err := os.ReadFile(filepath.Join(buildTree, "go.mod"))
	if err != nil {
		return err
	}

	err = os.WriteFile(modFile, data, 0o644)
	if err != nil {
		return err
	}

	data, err = os.ReadFile(filepath.Join(buildTree, "go.sum"))
	if err != nil {
		return err
	}

	err = os.WriteFile(sumFile, data, 0o644)
	if err != nil {
		return err
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

func pluginOutputPath() string { return filepath.Join(outputDir, pluginName) }

func binaryOutputPath() string { return filepath.Join(outputDir, binaryName) }

func pluginBuildTreePath() string { return filepath.Join(outputDir, pluginBuildTreeDir) }
