package services

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type remoteRuntimeProfile struct {
	OS   string
	Arch string
	Libc string
}

func (p remoteRuntimeProfile) BundleKey() string {
	parts := []string{}
	if p.OS != "" {
		parts = append(parts, p.OS)
	}
	if p.Arch != "" {
		parts = append(parts, p.Arch)
	}
	if p.Libc != "" && p.Libc != "unknown" {
		parts = append(parts, p.Libc)
	}
	return strings.Join(parts, "-")
}

func (p remoteRuntimeProfile) BundleKeyNoLibc() string {
	parts := []string{}
	if p.OS != "" {
		parts = append(parts, p.OS)
	}
	if p.Arch != "" {
		parts = append(parts, p.Arch)
	}
	return strings.Join(parts, "-")
}

type postgresToolBundleManifest struct {
	Version    string   `json:"version"`
	TargetOS   string   `json:"target_os"`
	TargetArch string   `json:"target_arch"`
	Libc       string   `json:"libc"`
	Tools      []string `json:"tools"`
}

type postgresToolBundle struct {
	Version      string
	RootDir      string
	ManifestPath string
	Manifest     postgresToolBundleManifest
	Source       string
}

func detectRemoteRuntimeProfile(runner *sshRunner) (remoteRuntimeProfile, error) {
	if runner == nil {
		return remoteRuntimeProfile{}, fmt.Errorf("SSH runner 不能为空")
	}
	output, err := runner.Run(strings.Join([]string{
		"set -e",
		"OS=$(uname -s 2>/dev/null | tr '[:upper:]' '[:lower:]')",
		"ARCH=$(uname -m 2>/dev/null | tr '[:upper:]' '[:lower:]')",
		"LIBC=unknown",
		"if command -v getconf >/dev/null 2>&1 && getconf GNU_LIBC_VERSION >/dev/null 2>&1; then LIBC=glibc; fi",
		"if [ \"$LIBC\" = \"unknown\" ] && command -v ldd >/dev/null 2>&1; then",
		"  LDD_HEAD=$(ldd --version 2>&1 | head -n1 | tr '[:upper:]' '[:lower:]')",
		"  case \"$LDD_HEAD\" in",
		"    *musl*) LIBC=musl ;;",
		"    *glibc*|*gnu*|*libc*) LIBC=glibc ;;",
		"  esac",
		"fi",
		"echo OS=$OS",
		"echo ARCH=$ARCH",
		"echo LIBC=$LIBC",
	}, "\n"))
	if err != nil {
		return remoteRuntimeProfile{}, fmt.Errorf("识别远端运行时画像失败: %w", err)
	}
	parsed := parseKeyValueOutput(output)
	return remoteRuntimeProfile{
		OS:   normalizeRuntimeOS(parsed["OS"]),
		Arch: normalizeRuntimeArch(parsed["ARCH"]),
		Libc: normalizeRuntimeLibc(parsed["LIBC"]),
	}, nil
}

func normalizeRuntimeOS(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "darwin", "macos", "mac":
		return "darwin"
	case "linux":
		return "linux"
	case "windows":
		return "windows"
	default:
		return strings.ToLower(strings.TrimSpace(value))
	}
}

func normalizeRuntimeArch(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "x86_64", "amd64":
		return "x86_64"
	case "aarch64", "arm64":
		return "arm64"
	case "armv7l", "armv7":
		return "armv7"
	default:
		return strings.ToLower(strings.TrimSpace(value))
	}
}

func normalizeRuntimeLibc(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "unknown":
		return "unknown"
	case "musl":
		return "musl"
	case "glibc", "gnu", "libc":
		return "glibc"
	default:
		return strings.ToLower(strings.TrimSpace(value))
	}
}

func selectBundledPostgresToolBundle(serverVersion string, profile remoteRuntimeProfile, requiredTools []string) (*postgresToolBundle, error) {
	versions := postgresClientVersionCandidates(serverVersion)
	if exact := postgresExactClientVersion(serverVersion); exact != "" {
		versions = append([]string{exact}, versions...)
	}
	versions = uniqueStrings(versions)
	bases := backupToolBaseDirs()
	searched := []string{}
	for _, base := range bases {
		for _, version := range versions {
			versionRoot := filepath.Join(base, "postgres", version)
			candidateDirs := bundledPostgresToolCandidateDirs(versionRoot, profile)
			for _, candidateDir := range candidateDirs {
				searched = append(searched, candidateDir)
				bundle, ok := loadBundledPostgresToolBundle(candidateDir, versionRoot, version, profile, requiredTools)
				if ok {
					return bundle, nil
				}
			}
		}
	}
	return nil, fmt.Errorf(
		"未找到 PostgreSQL 工具包：profile=%s required_tools=%s searched=%s",
		displayOrDefault(profile.BundleKey(), "generic"),
		strings.Join(uniqueStrings(requiredTools), ","),
		strings.Join(uniqueStrings(searched), ","),
	)
}

func bundledPostgresToolCandidateDirs(versionRoot string, profile remoteRuntimeProfile) []string {
	out := []string{}
	seen := map[string]struct{}{}
	add := func(path string) {
		v := strings.TrimSpace(path)
		if v == "" {
			return
		}
		cleaned := filepath.Clean(v)
		if _, ok := seen[cleaned]; ok {
			return
		}
		seen[cleaned] = struct{}{}
		out = append(out, cleaned)
	}
	if key := profile.BundleKey(); key != "" {
		add(filepath.Join(versionRoot, key))
	}
	if key := profile.BundleKeyNoLibc(); key != "" && key != profile.BundleKey() {
		add(filepath.Join(versionRoot, key))
	}
	if entries, err := os.ReadDir(versionRoot); err == nil {
		names := make([]string, 0, len(entries))
		for _, entry := range entries {
			if entry.IsDir() {
				names = append(names, entry.Name())
			}
		}
		sort.Strings(names)
		for _, name := range names {
			add(filepath.Join(versionRoot, name))
		}
	}
	add(versionRoot)
	return out
}

func loadBundledPostgresToolBundle(candidateDir string, versionRoot string, version string, profile remoteRuntimeProfile, requiredTools []string) (*postgresToolBundle, bool) {
	info, err := os.Stat(candidateDir)
	if err != nil || !info.IsDir() {
		return nil, false
	}
	manifestPath := filepath.Join(candidateDir, "manifest.json")
	manifest, hasManifest, err := readPostgresToolBundleManifest(manifestPath)
	if err != nil {
		return nil, false
	}
	if hasManifest && !profileMatchesBundleManifest(profile, manifest) {
		return nil, false
	}
	if !hasManifest && candidateDir != versionRoot {
		base := filepath.Base(candidateDir)
		if base != profile.BundleKey() && base != profile.BundleKeyNoLibc() {
			return nil, false
		}
	}
	if !bundleContainsTools(candidateDir, requiredTools) {
		return nil, false
	}
	source := fmt.Sprintf("bundle:%s", candidateDir)
	if hasManifest {
		source = fmt.Sprintf("bundle:%s (%s/%s/%s)", candidateDir, displayOrDefault(manifest.TargetOS, "*"), displayOrDefault(manifest.TargetArch, "*"), displayOrDefault(manifest.Libc, "*"))
	}
	return &postgresToolBundle{
		Version:      version,
		RootDir:      candidateDir,
		ManifestPath: manifestPath,
		Manifest:     manifest,
		Source:       source,
	}, true
}

func readPostgresToolBundleManifest(path string) (postgresToolBundleManifest, bool, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return postgresToolBundleManifest{}, false, nil
		}
		return postgresToolBundleManifest{}, false, err
	}
	var manifest postgresToolBundleManifest
	if err := json.Unmarshal(raw, &manifest); err != nil {
		return postgresToolBundleManifest{}, false, err
	}
	manifest.TargetOS = normalizeRuntimeOS(manifest.TargetOS)
	manifest.TargetArch = normalizeRuntimeArch(manifest.TargetArch)
	manifest.Libc = normalizeRuntimeLibc(manifest.Libc)
	return manifest, true, nil
}

func profileMatchesBundleManifest(profile remoteRuntimeProfile, manifest postgresToolBundleManifest) bool {
	match := func(expected string, actual string) bool {
		expected = strings.TrimSpace(strings.ToLower(expected))
		actual = strings.TrimSpace(strings.ToLower(actual))
		return expected == "" || expected == "*" || expected == actual
	}
	if !match(manifest.TargetOS, profile.OS) {
		return false
	}
	if !match(manifest.TargetArch, profile.Arch) {
		return false
	}
	if !match(manifest.Libc, profile.Libc) {
		return false
	}
	return true
}

func bundleContainsTools(rootDir string, requiredTools []string) bool {
	for _, toolName := range uniqueStrings(requiredTools) {
		if strings.TrimSpace(toolName) == "" {
			continue
		}
		found := false
		for _, candidate := range bundledToolCandidatePaths(rootDir, toolName) {
			if isExecutableFile(candidate) {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

func bundledToolCandidatePaths(rootDir string, toolName string) []string {
	name := strings.TrimSpace(toolName)
	if rootDir == "" || name == "" {
		return nil
	}
	return []string{
		filepath.Join(rootDir, name),
		filepath.Join(rootDir, "bin", name),
		filepath.Join(rootDir, "client", name),
		filepath.Join(rootDir, "client", "bin", name),
	}
}

func ensureRemoteBundledPostgresTools(runner *sshRunner, toolsDir string, serverVersion string, requiredTools []string) (string, error) {
	profile, err := detectRemoteRuntimeProfile(runner)
	if err != nil {
		return "", err
	}
	bundle, err := selectBundledPostgresToolBundle(serverVersion, profile, requiredTools)
	if err != nil {
		return "", err
	}
	if err := syncBundledPostgresToolBundle(runner, defaultBackupNodeToolsDir(toolsDir), bundle); err != nil {
		return "", err
	}
	return fmt.Sprintf("bundle=%s profile=%s target_version=%s required_tools=%s", bundle.Source, displayOrDefault(profile.BundleKey(), "generic"), bundle.Version, strings.Join(uniqueStrings(requiredTools), ",")), nil
}

func syncBundledPostgresToolBundle(runner *sshRunner, toolsDir string, bundle *postgresToolBundle) error {
	if runner == nil {
		return fmt.Errorf("SSH runner 不能为空")
	}
	if bundle == nil {
		return fmt.Errorf("工具包不能为空")
	}
	archiveBytes, err := archiveDirectoryToTarGz(bundle.RootDir)
	if err != nil {
		return fmt.Errorf("打包 PostgreSQL 工具包失败: %w", err)
	}
	toolsBase := defaultBackupNodeToolsDir(toolsDir)
	stamp := fmt.Sprintf("%d", time.Now().UnixNano())
	stagingParent := filepath.ToSlash(filepath.Join(toolsBase, ".staging", "postgres"))
	stagingDir := filepath.ToSlash(filepath.Join(stagingParent, bundle.Version+"-"+stamp))
	targetDir := filepath.ToSlash(filepath.Join(toolsBase, "postgres", bundle.Version))
	targetTmpDir := targetDir + ".tmp-" + stamp
	archivePath := filepath.ToSlash(filepath.Join(stagingParent, "bundle-"+bundle.Version+"-"+stamp+".tar.gz"))
	prepareScript := strings.Join([]string{
		"set -e",
		"mkdir -p " + shellQuote(stagingParent) + " " + shellQuote(filepath.ToSlash(filepath.Join(toolsBase, "postgres"))),
		"rm -rf " + shellQuote(stagingDir) + " " + shellQuote(targetTmpDir),
	}, "\n")
	if _, err := runner.Run(prepareScript); err != nil {
		return fmt.Errorf("准备远端工具目录失败: %w", err)
	}
	if err := runner.Upload(archivePath, archiveBytes); err != nil {
		return fmt.Errorf("上传 PostgreSQL 工具包失败: %w", err)
	}
	installScript := strings.Join([]string{
		"set -e",
		"command -v tar >/dev/null 2>&1",
		"mkdir -p " + shellQuote(stagingDir),
		"tar -xzf " + shellQuote(archivePath) + " -C " + shellQuote(stagingDir),
		"[ -d " + shellQuote(stagingDir) + " ]",
		"rm -rf " + shellQuote(targetTmpDir),
		"mv " + shellQuote(stagingDir) + " " + shellQuote(targetTmpDir),
		"rm -rf " + shellQuote(targetDir),
		"mv " + shellQuote(targetTmpDir) + " " + shellQuote(targetDir),
		"chmod -R a+rX " + shellQuote(targetDir),
		"rm -f " + shellQuote(archivePath),
		"echo INSTALLED=" + shellQuote(targetDir),
	}, "\n")
	if output, err := runner.Run(installScript); err != nil {
		return fmt.Errorf("安装 PostgreSQL 工具包失败: %w output=%s", err, strings.TrimSpace(output))
	}
	return nil
}

func archiveDirectoryToTarGz(rootDir string) ([]byte, error) {
	root := strings.TrimSpace(rootDir)
	if root == "" {
		return nil, fmt.Errorf("目录不能为空")
	}
	info, err := os.Stat(root)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("不是目录: %s", root)
	}
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == root {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		info, err := d.Info()
		if err != nil {
			return err
		}
		linkTarget := ""
		if info.Mode()&os.ModeSymlink != 0 {
			linkTarget, err = os.Readlink(path)
			if err != nil {
				return err
			}
		}
		header, err := tar.FileInfoHeader(info, linkTarget)
		if err != nil {
			return err
		}
		header.Name = rel
		if info.IsDir() && !strings.HasSuffix(header.Name, "/") {
			header.Name += "/"
		}
		if err := tw.WriteHeader(header); err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		file, err := os.Open(path)
		if err != nil {
			return err
		}
		defer file.Close()
		if _, err := io.Copy(tw, file); err != nil {
			return err
		}
		return nil
	})
	closeErr := tw.Close()
	if err == nil {
		err = closeErr
	}
	gzipErr := gz.Close()
	if err == nil {
		err = gzipErr
	}
	if err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
