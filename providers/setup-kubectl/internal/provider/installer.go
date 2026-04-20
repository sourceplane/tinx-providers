package provider

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"
)

const (
	defaultRequestedVersion = "latest"
	defaultStableVersion    = "v1.15.0"
	defaultToolName         = "kubectl"
)

var (
	versionPattern  = regexp.MustCompile(`^v?(\d+)\.(\d+)(?:\.(\d+))?$`)
	checksumPattern = regexp.MustCompile(`^[a-fA-F0-9]{64}$`)

	defaultMirrorBases = []string{
		"https://dl.k8s.io/release",
		"https://cdn.dl.k8s.io/release",
		"https://storage.googleapis.com/kubernetes-release/release",
	}
)

type Config struct {
	RequestedVersion string
	InstallDir       string
	TargetBin        string
	CacheDir         string
	KioxHome         string
	ToolName         string
	Mirrors          []string
	Debug            bool
	LogOutput        io.Writer
	HTTPClient       *http.Client
}

type Result struct {
	RequestedVersion string `json:"requested_version"`
	ResolvedVersion  string `json:"resolved_version"`
	BinaryPath       string `json:"binary_path"`
	SHA256           string `json:"sha256"`
	SourceURL        string `json:"source_url"`
	UsedCache        bool   `json:"used_cache"`
}

type Installer struct {
	client *http.Client
}

type versionSpec struct {
	raw     string
	major   string
	minor   string
	exact   string
	stable  bool
	current string
}

func NewInstaller() *Installer {
	return &Installer{
		client: &http.Client{Timeout: 2 * time.Minute},
	}
}

func MirrorsFromEnv(raw string) []string {
	parts := strings.FieldsFunc(raw, func(r rune) bool {
		return r == ',' || r == ';' || r == '\n'
	})
	mirrors := make([]string, 0, len(parts))
	for _, part := range parts {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			mirrors = append(mirrors, trimmed)
		}
	}
	return mirrors
}

func (i *Installer) Install(ctx context.Context, cfg Config) (Result, error) {
	client := i.client
	if cfg.HTTPClient != nil {
		client = cfg.HTTPClient
	}

	mirrors, err := sanitizeMirrors(cfg.Mirrors)
	if err != nil {
		return Result{}, err
	}

	cacheDir, err := resolveCacheDir(cfg.CacheDir, cfg.KioxHome)
	if err != nil {
		return Result{}, err
	}

	targetPath, err := resolveTargetPath(cfg.TargetBin, cfg.InstallDir, cfg.ToolName, runtime.GOOS)
	if err != nil {
		return Result{}, err
	}
	targetPath, err = filepath.Abs(targetPath)
	if err != nil {
		return Result{}, err
	}

	goos, goarch, err := kubectlPlatform(runtime.GOOS, runtime.GOARCH)
	if err != nil {
		return Result{}, err
	}

	requested := cfg.RequestedVersion
	if strings.TrimSpace(requested) == "" {
		requested = defaultRequestedVersion
	}

	resolvedVersion, err := resolveVersion(ctx, client, cacheDir, mirrors, requested, cfg)
	if err != nil {
		return Result{}, err
	}

	checksum, _, err := loadOrFetchChecksum(ctx, client, cacheDir, mirrors, resolvedVersion, goos, goarch, cfg)
	if err != nil {
		return Result{}, err
	}

	binaryURL := downloadURLForMirror(mirrors[0], resolvedVersion, goos, goarch)
	if ok, err := fileMatchesChecksum(targetPath, checksum); err == nil && ok {
		return Result{
			RequestedVersion: strings.TrimSpace(requested),
			ResolvedVersion:  resolvedVersion,
			BinaryPath:       targetPath,
			SHA256:           checksum,
			SourceURL:        binaryURL,
			UsedCache:        true,
		}, nil
	}

	cacheBinaryPath := binaryCachePath(cacheDir, resolvedVersion, goos, goarch)
	usedCache := false
	if ok, err := fileMatchesChecksum(cacheBinaryPath, checksum); err == nil && ok {
		usedCache = true
	} else {
		payload, usedSourceURL, err := fetchFirst(ctx, client, binaryURLs(mirrors, resolvedVersion, goos, goarch), cfg)
		if err != nil {
			return Result{}, err
		}
		if actual := sha256.Sum256(payload); strings.ToLower(checksum) != hex.EncodeToString(actual[:]) {
			return Result{}, fmt.Errorf("checksum mismatch for %s", usedSourceURL)
		}
		if err := writeAtomic(cacheBinaryPath, payload, 0o755); err != nil {
			return Result{}, err
		}
		binaryURL = usedSourceURL
	}

	if err := copyFileAtomic(cacheBinaryPath, targetPath, 0o755); err != nil {
		return Result{}, err
	}
	if ok, err := fileMatchesChecksum(targetPath, checksum); err != nil || !ok {
		if err != nil {
			return Result{}, err
		}
		return Result{}, fmt.Errorf("installed binary checksum mismatch at %s", targetPath)
	}

	return Result{
		RequestedVersion: strings.TrimSpace(requested),
		ResolvedVersion:  resolvedVersion,
		BinaryPath:       targetPath,
		SHA256:           checksum,
		SourceURL:        binaryURL,
		UsedCache:        usedCache,
	}, nil
}

func resolveVersion(ctx context.Context, client *http.Client, cacheDir string, mirrors []string, requested string, cfg Config) (string, error) {
	spec, err := parseVersionSpec(requested)
	if err != nil {
		return "", err
	}

	switch {
	case spec.stable:
		version, err := loadOrFetchVersionFile(ctx, client, cacheDir, mirrors, "stable.txt", cfg)
		if err != nil {
			return defaultStableVersion, nil
		}
		if version == "" {
			return defaultStableVersion, nil
		}
		return normalizeExactVersion(version), nil
	case spec.exact != "":
		return spec.exact, nil
	default:
		relativePath := fmt.Sprintf("stable-%s.%s.txt", spec.major, spec.minor)
		version, err := loadOrFetchVersionFile(ctx, client, cacheDir, mirrors, relativePath, cfg)
		if err != nil {
			return "", fmt.Errorf("failed to get latest patch version for %s.%s", spec.major, spec.minor)
		}
		if strings.TrimSpace(version) == "" {
			return "", fmt.Errorf("failed to get latest patch version for %s.%s", spec.major, spec.minor)
		}
		return normalizeExactVersion(version), nil
	}
}

func parseVersionSpec(requested string) (versionSpec, error) {
	trimmed := strings.TrimSpace(requested)
	if trimmed == "" || strings.EqualFold(trimmed, "latest") || strings.EqualFold(trimmed, "stable") {
		return versionSpec{raw: defaultRequestedVersion, stable: true}, nil
	}

	match := versionPattern.FindStringSubmatch(trimmed)
	if len(match) == 0 {
		return versionSpec{}, fmt.Errorf("invalid version format %q; expected latest, stable, major.minor, or major.minor.patch", requested)
	}

	spec := versionSpec{raw: trimmed, major: match[1], minor: match[2]}
	if patch := match[3]; patch != "" {
		spec.exact = normalizeExactVersion(trimmed)
		return spec, nil
	}

	return spec, nil
}

func resolveTargetPath(targetBin, installDir, toolName, goos string) (string, error) {
	if trimmed := strings.TrimSpace(targetBin); trimmed != "" {
		return trimmed, nil
	}

	if strings.TrimSpace(installDir) == "" {
		return "", errors.New("KIOX_TARGET_TOOL_INSTALL_DIR or KIOX_TARGET_TOOL_BIN must be set")
	}

	name := strings.TrimSpace(toolName)
	if name == "" {
		name = defaultToolName
	}
	if goos == "windows" && !strings.HasSuffix(strings.ToLower(name), ".exe") {
		name += ".exe"
	}

	return filepath.Join(installDir, "bin", name), nil
}

func resolveCacheDir(cacheDir, kioxHome string) (string, error) {
	if trimmed := strings.TrimSpace(cacheDir); trimmed != "" {
		return trimmed, nil
	}

	base := strings.TrimSpace(kioxHome)
	if base == "" {
		homeDir, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolve home directory: %w", err)
		}
		base = filepath.Join(homeDir, ".kiox")
	}

	return filepath.Join(base, "cache", "providers", "setup-kubectl"), nil
}

func kubectlPlatform(goos, goarch string) (string, string, error) {
	switch goos {
	case "linux", "darwin", "windows":
	default:
		return "", "", fmt.Errorf("unsupported operating system %q", goos)
	}

	switch goarch {
	case "amd64", "arm64", "arm":
		return goos, goarch, nil
	default:
		return "", "", fmt.Errorf("unsupported architecture %q", goarch)
	}
}

func normalizeExactVersion(version string) string {
	trimmed := strings.TrimSpace(version)
	if trimmed == "" {
		return ""
	}
	if strings.HasPrefix(trimmed, "v") {
		return trimmed
	}
	return "v" + trimmed
}

func sanitizeMirrors(mirrors []string) ([]string, error) {
	if len(mirrors) == 0 {
		mirrors = append([]string(nil), defaultMirrorBases...)
	}

	clean := make([]string, 0, len(mirrors))
	seen := map[string]struct{}{}
	for _, mirror := range mirrors {
		trimmed := strings.TrimRight(strings.TrimSpace(mirror), "/")
		if trimmed == "" {
			continue
		}
		parsed, err := url.Parse(trimmed)
		if err != nil {
			return nil, fmt.Errorf("invalid mirror URL %q: %w", mirror, err)
		}
		if parsed.Scheme != "https" {
			return nil, fmt.Errorf("mirror URL must use HTTPS: %s", trimmed)
		}
		if _, ok := seen[trimmed]; ok {
			continue
		}
		seen[trimmed] = struct{}{}
		clean = append(clean, trimmed)
	}

	if len(clean) == 0 {
		return nil, errors.New("at least one HTTPS mirror URL is required")
	}

	return clean, nil
}

func loadOrFetchVersionFile(ctx context.Context, client *http.Client, cacheDir string, mirrors []string, relativePath string, cfg Config) (string, error) {
	cachePath := filepath.Join(cacheDir, "versions", relativePath)
	if value, err := readTrimmedFile(cachePath); err == nil && value != "" {
		debugf(cfg, "using cached version marker %s", cachePath)
		return value, nil
	}

	data, usedURL, err := fetchFirst(ctx, client, joinMirrorPaths(mirrors, relativePath), cfg)
	if err != nil {
		if value, cacheErr := readTrimmedFile(cachePath); cacheErr == nil && value != "" {
			debugf(cfg, "network resolution failed, reusing cached version marker %s", cachePath)
			return value, nil
		}
		return "", err
	}

	version := strings.TrimSpace(string(data))
	if version == "" {
		return "", fmt.Errorf("empty version marker from %s", usedURL)
	}
	if err := writeAtomic(cachePath, []byte(version+"\n"), 0o644); err != nil {
		return "", err
	}
	return version, nil
}

func loadOrFetchChecksum(ctx context.Context, client *http.Client, cacheDir string, mirrors []string, version, goos, goarch string, cfg Config) (string, string, error) {
	cachePath := filepath.Join(cacheDir, "checksums", version, goos, goarch, executableName(goos)+".sha256")
	if value, err := readTrimmedFile(cachePath); err == nil && checksumPattern.MatchString(value) {
		debugf(cfg, "using cached checksum %s", cachePath)
		return strings.ToLower(value), "", nil
	}

	data, usedURL, err := fetchFirst(ctx, client, checksumURLs(mirrors, version, goos, goarch), cfg)
	if err != nil {
		return "", "", err
	}

	fields := strings.Fields(string(data))
	if len(fields) == 0 || !checksumPattern.MatchString(fields[0]) {
		return "", "", fmt.Errorf("invalid checksum payload from %s", usedURL)
	}
	checksum := strings.ToLower(fields[0])
	if err := writeAtomic(cachePath, []byte(checksum+"\n"), 0o644); err != nil {
		return "", "", err
	}
	return checksum, usedURL, nil
}

func fetchFirst(ctx context.Context, client *http.Client, urls []string, cfg Config) ([]byte, string, error) {
	var failures []string
	for _, sourceURL := range urls {
		debugf(cfg, "fetching %s", sourceURL)
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, sourceURL, nil)
		if err != nil {
			failures = append(failures, fmt.Sprintf("%s: %v", sourceURL, err))
			continue
		}

		response, err := client.Do(request)
		if err != nil {
			failures = append(failures, fmt.Sprintf("%s: %v", sourceURL, err))
			continue
		}

		data, readErr := io.ReadAll(response.Body)
		response.Body.Close()
		if readErr != nil {
			failures = append(failures, fmt.Sprintf("%s: %v", sourceURL, readErr))
			continue
		}
		if response.StatusCode != http.StatusOK {
			failures = append(failures, fmt.Sprintf("%s: unexpected status %s", sourceURL, response.Status))
			continue
		}

		return data, sourceURL, nil
	}

	return nil, "", fmt.Errorf("all mirrors failed: %s", strings.Join(failures, "; "))
}

func fileMatchesChecksum(path, expected string) (bool, error) {
	file, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	defer file.Close()

	hasher := sha256.New()
	if _, err := io.Copy(hasher, file); err != nil {
		return false, err
	}

	return strings.EqualFold(expected, hex.EncodeToString(hasher.Sum(nil))), nil
}

func copyFileAtomic(sourcePath, targetPath string, perm os.FileMode) error {
	input, err := os.Open(sourcePath)
	if err != nil {
		return err
	}
	defer input.Close()

	if err := os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
		return err
	}

	tempFile, err := os.CreateTemp(filepath.Dir(targetPath), "."+filepath.Base(targetPath)+".tmp-*")
	if err != nil {
		return err
	}
	tempPath := tempFile.Name()
	defer os.Remove(tempPath)

	if _, err := io.Copy(tempFile, input); err != nil {
		tempFile.Close()
		return err
	}
	if err := tempFile.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tempPath, perm); err != nil {
		return err
	}

	return replaceFile(tempPath, targetPath)
}

func writeAtomic(path string, data []byte, perm os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}

	tempFile, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tempPath := tempFile.Name()
	defer os.Remove(tempPath)

	if _, err := tempFile.Write(data); err != nil {
		tempFile.Close()
		return err
	}
	if err := tempFile.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tempPath, perm); err != nil {
		return err
	}

	return replaceFile(tempPath, path)
}

func replaceFile(tempPath, targetPath string) error {
	if err := os.Rename(tempPath, targetPath); err == nil {
		return nil
	}
	if removeErr := os.Remove(targetPath); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
		return removeErr
	}
	return os.Rename(tempPath, targetPath)
}

func readTrimmedFile(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(data)), nil
}

func joinMirrorPaths(mirrors []string, relativePath string) []string {
	urls := make([]string, 0, len(mirrors))
	for _, mirror := range mirrors {
		urls = append(urls, strings.TrimRight(mirror, "/")+"/"+strings.TrimLeft(relativePath, "/"))
	}
	return urls
}

func binaryURLs(mirrors []string, version, goos, goarch string) []string {
	urls := make([]string, 0, len(mirrors))
	for _, mirror := range mirrors {
		urls = append(urls, downloadURLForMirror(mirror, version, goos, goarch))
	}
	return urls
}

func checksumURLs(mirrors []string, version, goos, goarch string) []string {
	urls := make([]string, 0, len(mirrors))
	for _, mirror := range mirrors {
		urls = append(urls, downloadURLForMirror(mirror, version, goos, goarch)+".sha256")
	}
	return urls
}

func downloadURLForMirror(mirror, version, goos, goarch string) string {
	return fmt.Sprintf("%s/%s/bin/%s/%s/%s", strings.TrimRight(mirror, "/"), version, goos, goarch, executableName(goos))
}

func binaryCachePath(cacheDir, version, goos, goarch string) string {
	return filepath.Join(cacheDir, "downloads", version, goos, goarch, executableName(goos))
}

func executableName(goos string) string {
	if goos == "windows" {
		return "kubectl.exe"
	}
	return "kubectl"
}

func debugf(cfg Config, format string, args ...any) {
	if !cfg.Debug || cfg.LogOutput == nil {
		return
	}
	_, _ = fmt.Fprintf(cfg.LogOutput, "debug: "+format+"\n", args...)
}
