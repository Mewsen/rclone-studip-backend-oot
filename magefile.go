//go:build mage
// +build mage

package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/rclone/rclone/fs/config/obscure"
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

	studIPDemoBaseURL  = "http://localhost:8034/jsonapi.php/v1/"
	studIPDemoUsername = "test_dozent"
	studIPDemoPassword = "testing"
	studIPDemoLicense  = "SELFMADE_NONPUB"
	studIPConfigName   = "test-rclone.conf"
)

var Default = BuildPluginAndStandaloneBinary

type studIPTestConfig struct {
	BaseURL    string
	Username   string
	Password   string
	CourseID   string
	License    string
	ConfigPath string
}

type studIPCoursesResponse struct {
	Data []studIPCourse `json:"data"`
}

type studIPCourse struct {
	ID         string `json:"id"`
	Attributes struct {
		Title      string `json:"title"`
		CourseType int    `json:"course-type"`
	} `json:"attributes"`
	Relationships struct {
		Folders struct {
			Links struct {
				Related string `json:"related"`
			} `json:"links"`
		} `json:"folders"`
	} `json:"relationships"`
}

type studIPFoldersResponse struct {
	Data []struct {
		Attributes struct {
			FolderType string `json:"folder-type"`
			IsReadable bool   `json:"is-readable"`
			IsWritable bool   `json:"is-writable"`
		} `json:"attributes"`
	} `json:"data"`
}

func TestAgainstContainer() error {
	err := runCommandWithEnv(nil, "git", "submodule", "update", "--init", "--recursive")
	if err != nil {
		return err
	}

	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		return fmt.Errorf("create output dir %q: %w", outputDir, err)
	}

	config, course, err := prepareStudIPTestEnvironment()
	if err != nil {
		return err
	}

	fmt.Printf(">> using Stud.IP demo account %q\n", config.Username)
	fmt.Printf(">> using demo course %q (%s)\n", course.Attributes.Title, course.ID)
	fmt.Printf(">> wrote rclone config to %s\n", config.ConfigPath)
	fmt.Printf(">> running backend tests: RCLONE_CONFIG=%s go test -parallel=1 -v -count=1 ./backend/studip/studip_test.go\n", config.ConfigPath)

	defer func() {
		fmt.Println(">> stopping Stud.IP demo stack")
		if downErr := runCommandWithEnv(nil, "docker", "compose", "down", "--volumes", "--remove-orphans"); downErr != nil {
			fmt.Fprintf(os.Stderr, "failed to stop Stud.IP demo stack: %v\n", downErr)
		}
	}()

	err = runCommandWithEnv(
		[]string{"RCLONE_CONFIG=" + config.ConfigPath},
		"go", "test", "-parallel=16", "-v", "-count=1", "./backend/studip/studip_test.go",
	)
	if err != nil {
		return err
	}
	err = runCommandWithEnv(
		[]string{"RCLONE_CONFIG=" + config.ConfigPath},
		"go", "-C", "fs/sync", "test", "-parallel=16", "-remote", "TestStudIP:fs/sync", "-v", "-count=1",
	)
	if err != nil {
		return err
	}

	err = runCommandWithEnv(
		[]string{"RCLONE_CONFIG=" + config.ConfigPath},
		"go", "-C", "fs/sync", "test", "-parallel=16", "-remote", "TestStudIP:fs/operations", "-v", "-count=1",
	)
	if err != nil {
		return err
	}

	return nil
}

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

	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		return fmt.Errorf("create output dir %q: %w", outputDir, err)
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
	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		return fmt.Errorf("create output dir %q: %w", outputDir, err)
	}

	type target struct {
		goos   string
		goarch string
	}

	targets := []target{
		{goos: "linux", goarch: "amd64"},
		{goos: "linux", goarch: "arm64"},
		{goos: "windows", goarch: "amd64"},
		{goos: "windows", goarch: "arm64"},
		{goos: "darwin", goarch: "amd64"},
		{goos: "darwin", goarch: "arm64"},
	}

	for _, t := range targets {
		out := binaryOutputPathFor(t.goos, t.goarch)

		env := []string{
			"GOEXPERIMENT=" + goExperiment,
			"GOOS=" + t.goos,
			"GOARCH=" + t.goarch,
			"CGO_ENABLED=0",
		}

		if err := runCommandWithEnv(
			env,
			"go", "build", "-o", out, ".",
		); err != nil {
			return fmt.Errorf("build %s/%s: %w", t.goos, t.goarch, err)
		}
	}

	return nil
}

func binaryOutputPathFor(goos, goarch string) string {
	name := fmt.Sprintf("rclone-studip-%s-%s", goos, goarch)
	if goos == "windows" {
		name += ".exe"
	}
	return filepath.Join(outputDir, name)
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

func prepareStudIPTestEnvironment() (studIPTestConfig, studIPCourse, error) {
	config := studIPTestConfig{
		BaseURL:    envOrDefault("STUDIP_TEST_BASE_URL", studIPDemoBaseURL),
		Username:   envOrDefault("STUDIP_TEST_USERNAME", studIPDemoUsername),
		Password:   envOrDefault("STUDIP_TEST_PASSWORD", studIPDemoPassword),
		License:    envOrDefault("STUDIP_TEST_LICENSE", studIPDemoLicense),
		ConfigPath: envOrDefault("RCLONE_CONFIG", filepath.Join(outputDir, studIPConfigName)),
	}

	configPath, err := filepath.Abs(config.ConfigPath)
	if err != nil {
		return config, studIPCourse{}, fmt.Errorf("resolve rclone config path: %w", err)
	}
	config.ConfigPath = configPath

	if err := recreateStudIPDemoStack(); err != nil {
		return config, studIPCourse{}, err
	}

	var course studIPCourse
	course, err = waitForStudIPDemoCourse(config.BaseURL, config.Username, config.Password, 2*time.Minute)
	if err != nil {
		return config, studIPCourse{}, err
	}
	config.CourseID = course.ID

	if err := writeStudIPTestConfig(config); err != nil {
		return config, studIPCourse{}, err
	}

	return config, course, nil
}

func recreateStudIPDemoStack() error {
	fmt.Println(">> recreating fresh Stud.IP demo stack")

	if err := runCommandWithEnv(nil, "docker", "compose", "down", "--volumes", "--remove-orphans"); err != nil {
		return err
	}

	return runCommandWithEnv(nil, "docker", "compose", "up", "-d", "--build")
}

func waitForStudIPDemoCourse(baseURL, username, password string, timeout time.Duration) (studIPCourse, error) {
	var (
		course  studIPCourse
		lastErr error
	)

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		course, lastErr = discoverStudIPDemoCourse(baseURL, username, password)
		if lastErr == nil {
			return course, nil
		}
		time.Sleep(2 * time.Second)
	}

	if lastErr == nil {
		lastErr = errors.New("no writable demo course became available")
	}

	return studIPCourse{}, fmt.Errorf("wait for Stud.IP demo course: %w", lastErr)
}

func waitForStudIPCourse(baseURL, username, password, courseID string, timeout time.Duration) (studIPCourse, error) {
	var (
		course  studIPCourse
		lastErr error
	)

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		course, lastErr = fetchStudIPCourse(baseURL, username, password, courseID)
		if lastErr == nil {
			lastErr = verifyStudIPCourseFolders(baseURL, username, password, courseID)
		}
		if lastErr == nil {
			return course, nil
		}
		time.Sleep(2 * time.Second)
	}

	if lastErr == nil {
		lastErr = fmt.Errorf("course %q never became readable", courseID)
	}

	return studIPCourse{}, fmt.Errorf("wait for Stud.IP course %q: %w", courseID, lastErr)
}

func discoverStudIPDemoCourse(baseURL, username, password string) (studIPCourse, error) {
	courses, err := fetchStudIPCourses(baseURL, username, password)
	if err != nil {
		return studIPCourse{}, err
	}

	var lastErr error
	for _, course := range courses.Data {
		if strings.TrimSpace(course.Relationships.Folders.Links.Related) == "" {
			continue
		}
		if err := verifyStudIPCourseFolders(baseURL, username, password, course.ID); err != nil {
			lastErr = err
			continue
		}
		return course, nil
	}

	if lastErr != nil {
		return studIPCourse{}, lastErr
	}

	return studIPCourse{}, errors.New("courses endpoint returned no course with folder access")
}

func fetchStudIPCourses(baseURL, username, password string) (*studIPCoursesResponse, error) {
	response := new(studIPCoursesResponse)
	if err := studIPGetJSON(baseURL, username, password, "courses", response); err != nil {
		return nil, err
	}
	return response, nil
}

func fetchStudIPCourse(baseURL, username, password, courseID string) (studIPCourse, error) {
	var response struct {
		Data studIPCourse `json:"data"`
	}

	if err := studIPGetJSON(baseURL, username, password, fmt.Sprintf("courses/", courseID), &response); err != nil {
		return studIPCourse{}, err
	}

	return response.Data, nil
}

func verifyStudIPCourseFolders(baseURL, username, password, courseID string) error {
	response := new(studIPFoldersResponse)
	if err := studIPGetJSON(baseURL, username, password, fmt.Sprintf("courses/%s/folders", courseID), response); err != nil {
		return err
	}

	for _, folder := range response.Data {
		if folder.Attributes.FolderType != "RootFolder" {
			continue
		}
		if !folder.Attributes.IsReadable {
			return fmt.Errorf("course %q root folder is not readable", courseID)
		}
		if !folder.Attributes.IsWritable {
			return fmt.Errorf("course %q root folder is not writable", courseID)
		}
		return nil
	}

	return fmt.Errorf("course %q has no root folder in the folders response", courseID)
}

func studIPGetJSON(baseURL, username, password, relativePath string, out any) error {
	requestURL, err := studIPAPIURL(baseURL, relativePath)
	if err != nil {
		return err
	}

	req, err := http.NewRequest(http.MethodGet, requestURL, nil)
	if err != nil {
		return fmt.Errorf("create request %q: %w", requestURL, err)
	}
	req.SetBasicAuth(username, password)
	req.Header.Set("Accept", "application/vnd.api+json")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("GET %s failed: %w", requestURL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("GET %s returned %s: %s", requestURL, resp.Status, strings.TrimSpace(string(body)))
	}

	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("decode %s response: %w", requestURL, err)
	}

	return nil
}

func studIPAPIURL(baseURL, relativePath string) (string, error) {
	base, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil {
		return "", fmt.Errorf("parse Stud.IP base URL %q: %w", baseURL, err)
	}

	relativePath = strings.TrimLeft(strings.TrimSpace(relativePath), "/")
	ref, err := url.Parse(relativePath)
	if err != nil {
		return "", fmt.Errorf("parse Stud.IP relative path %q: %w", relativePath, err)
	}

	return base.ResolveReference(ref).String(), nil
}

func writeStudIPTestConfig(config studIPTestConfig) error {
	if err := os.MkdirAll(filepath.Dir(config.ConfigPath), 0o755); err != nil {
		return fmt.Errorf("create rclone config dir: %w", err)
	}

	obscuredPassword, err := obscure.Obscure(config.Password)
	if err != nil {
		return fmt.Errorf("obscure Stud.IP password: %w", err)
	}

	data := strings.Join([]string{
		"[TestStudIP]",
		"type = studip",
		"base_url = " + config.BaseURL,
		"username = " + config.Username,
		"password = " + obscuredPassword,
		"course_id = " + config.CourseID,
		"license = " + config.License,
		"",
	}, "\n")

	if err := os.WriteFile(config.ConfigPath, []byte(data), 0o600); err != nil {
		return fmt.Errorf("write rclone config %q: %w", config.ConfigPath, err)
	}

	return nil
}

func envOrDefault(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
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
