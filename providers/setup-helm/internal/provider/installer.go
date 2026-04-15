package provider

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"

	"github.com/sourceplane/tinx-providers/internal/installutil"
)

const (
	defaultRequestedVersion = "latest"
	defaultStableVersion    = "v4.1.4"
	defaultToolName         = "helm"
	defaultBaseURL          = "https://get.helm.sh"
)

var versionPattern = regexp.MustCompile(`^v?(\d+)\.(\d+)\.(\d+)$`)

type Config struct {
	RequestedVersion string
	InstallDir       string
	TargetBin        string
	CacheDir         string
	TinxHome         string
	ToolName         string
	Mirrors          []string
	HTTPClient       *http.Client
	LogOutput        io.Writer
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

func NewInstaller() *Installer {
	return &Installer{client: installutil.NewHTTPClient()}
}

func MirrorsFromEnv(raw string) []string {
	parts := strings.FieldsFunc(raw, func(r rune) bool {
		return r == ',' || r == ';' || r == '\n'
	})
	urls := make([]string, 0, len(parts))
	for _, part := range parts {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			urls = append(urls, trimmed)
		}
	}
	return urls
}

func (i *Installer) Install(ctx context.Context, cfg Config) (Result, error) {
	client := i.client
	if cfg.HTTPClient != nil {
		client = cfg.HTTPClient
	}

	baseURLs, err := installutil.SanitizeURLs(cfg.Mirrors, []string{defaultBaseURL}, true)
	if err != nil {
		return Result{}, err
	}

	cacheDir, err := installutil.ResolveCacheDir(cfg.CacheDir, cfg.TinxHome, "setup-helm")
	if err != nil {
		return Result{}, err
	}

	toolName := strings.TrimSpace(cfg.ToolName)
	if toolName == "" {
		toolName = defaultToolName
	}

	targetPath, err := installutil.ResolveTargetPath(cfg.TargetBin, cfg.InstallDir, toolName, runtime.GOOS)
	if err != nil {
		return Result{}, err
	}

	goos, goarch, archiveExt, err := helmPlatform(runtime.GOOS, runtime.GOARCH)
	if err != nil {
		return Result{}, err
	}

	requested := strings.TrimSpace(cfg.RequestedVersion)
	if requested == "" {
		requested = defaultRequestedVersion
	}

	resolvedVersion, err := resolveVersion(ctx, client, cacheDir, baseURLs, requested)
	if err != nil {
		return Result{}, err
	}

	cacheBinaryPath := binaryCachePath(cacheDir, resolvedVersion, goos, goarch)
	binaryDigest := ""
	usedCache := false
	if _, err := osStat(cacheBinaryPath); err == nil {
		binaryDigest, err = installutil.FileSHA256(cacheBinaryPath)
		if err != nil {
			return Result{}, err
		}
		usedCache = true
	} else {
		archiveChecksum, err := loadOrFetchArchiveChecksum(ctx, client, cacheDir, baseURLs, resolvedVersion, goos, goarch, archiveExt)
		if err != nil {
			return Result{}, err
		}
		archivePath := archiveCachePath(cacheDir, resolvedVersion, goos, goarch, archiveExt)
		if ok, err := installutil.FileMatchesChecksum(archivePath, archiveChecksum); err != nil {
			return Result{}, err
		} else if !ok {
			if _, err := installutil.DownloadToFile(ctx, client, archiveURLs(baseURLs, resolvedVersion, goos, goarch, archiveExt), archivePath, archiveChecksum, 0o644); err != nil {
				return Result{}, err
			}
		}
		if err := extractBinaryToCache(archivePath, cacheBinaryPath, goos); err != nil {
			return Result{}, err
		}
		binaryDigest, err = installutil.FileSHA256(cacheBinaryPath)
		if err != nil {
			return Result{}, err
		}
	}

	if ok, err := installutil.FileMatchesChecksum(targetPath, binaryDigest); err == nil && ok {
		return Result{
			RequestedVersion: requested,
			ResolvedVersion:  resolvedVersion,
			BinaryPath:       targetPath,
			SHA256:           binaryDigest,
			SourceURL:        archiveURLs(baseURLs, resolvedVersion, goos, goarch, archiveExt)[0],
			UsedCache:        true,
		}, nil
	}

	if err := installutil.CopyFileAtomic(cacheBinaryPath, targetPath, 0o755); err != nil {
		return Result{}, err
	}
	if ok, err := installutil.FileMatchesChecksum(targetPath, binaryDigest); err != nil || !ok {
		if err != nil {
			return Result{}, err
		}
		return Result{}, fmt.Errorf("installed binary checksum mismatch at %s", targetPath)
	}

	return Result{
		RequestedVersion: requested,
		ResolvedVersion:  resolvedVersion,
		BinaryPath:       targetPath,
		SHA256:           binaryDigest,
		SourceURL:        archiveURLs(baseURLs, resolvedVersion, goos, goarch, archiveExt)[0],
		UsedCache:        usedCache,
	}, nil
}

func resolveVersion(ctx context.Context, client *http.Client, cacheDir string, baseURLs []string, requested string) (string, error) {
	trimmed := strings.TrimSpace(requested)
	if trimmed == "" || strings.EqualFold(trimmed, "latest") || strings.EqualFold(trimmed, "stable") {
		version, err := loadOrFetchLatestVersion(ctx, client, cacheDir, baseURLs)
		if err != nil || version == "" {
			return defaultStableVersion, nil
		}
		return normalizeExactVersion(version), nil
	}

	if !versionPattern.MatchString(trimmed) {
		return "", fmt.Errorf("invalid version format %q; expected latest or major.minor.patch", requested)
	}
	return normalizeExactVersion(trimmed), nil
}

func loadOrFetchLatestVersion(ctx context.Context, client *http.Client, cacheDir string, baseURLs []string) (string, error) {
	cachePath := filepath.Join(cacheDir, "versions", "latest.txt")
	if value, err := installutil.ReadTrimmedFile(cachePath); err == nil && value != "" {
		return value, nil
	}

	data, _, err := installutil.FetchFirst(ctx, client, latestVersionURLs(baseURLs))
	if err != nil {
		if value, cacheErr := installutil.ReadTrimmedFile(cachePath); cacheErr == nil && value != "" {
			return value, nil
		}
		return "", err
	}

	version := strings.TrimSpace(string(data))
	if version == "" {
		return "", fmt.Errorf("empty latest Helm version marker")
	}
	if err := installutil.WriteAtomic(cachePath, []byte(version+"\n"), 0o644); err != nil {
		return "", err
	}
	return version, nil
}

func loadOrFetchArchiveChecksum(ctx context.Context, client *http.Client, cacheDir string, baseURLs []string, version, goos, goarch, archiveExt string) (string, error) {
	cachePath := filepath.Join(cacheDir, "checksums", version, goos, goarch, checksumFileName(version, goos, goarch, archiveExt))
	if value, err := installutil.ReadTrimmedFile(cachePath); err == nil && value != "" {
		return value, nil
	}

	data, _, err := installutil.FetchFirst(ctx, client, checksumURLs(baseURLs, version, goos, goarch, archiveExt))
	if err != nil {
		return "", err
	}
	fields := strings.Fields(string(data))
	if len(fields) == 0 {
		return "", fmt.Errorf("empty checksum payload for Helm %s", version)
	}
	checksum := strings.TrimSpace(fields[0])
	if err := installutil.WriteAtomic(cachePath, []byte(checksum+"\n"), 0o644); err != nil {
		return "", err
	}
	return checksum, nil
}

func extractBinaryToCache(archivePath, cacheBinaryPath, goos string) error {
	workDir, err := filepath.Abs(filepath.Join(filepath.Dir(cacheBinaryPath), "extract"))
	if err != nil {
		return err
	}
	if err := filepathRemoveAll(workDir); err != nil {
		return err
	}
	if err := filepathMkdirAll(workDir, 0o755); err != nil {
		return err
	}
	defer filepathRemoveAll(workDir)

	if strings.HasSuffix(strings.ToLower(archivePath), ".zip") {
		if err := installutil.ExtractZip(archivePath, workDir); err != nil {
			return err
		}
	} else {
		if err := installutil.ExtractTarGz(archivePath, workDir); err != nil {
			return err
		}
	}

	binaryName := installutil.ExecutableName(defaultToolName, goos)
	binaryPath, err := installutil.FindFirstFile(workDir, binaryName)
	if err != nil {
		return err
	}
	return installutil.CopyFileAtomic(binaryPath, cacheBinaryPath, 0o755)
}

func latestVersionURLs(baseURLs []string) []string {
	urls := make([]string, 0, len(baseURLs))
	for _, baseURL := range baseURLs {
		urls = append(urls, installutil.JoinURLPath(baseURL, "helm-latest-version"))
	}
	return urls
}

func archiveURLs(baseURLs []string, version, goos, goarch, archiveExt string) []string {
	archiveName := archiveFileName(version, goos, goarch, archiveExt)
	return installutil.JoinURLPaths(baseURLs, archiveName)
}

func checksumURLs(baseURLs []string, version, goos, goarch, archiveExt string) []string {
	return installutil.JoinURLPaths(baseURLs, checksumFileName(version, goos, goarch, archiveExt))
}

func archiveFileName(version, goos, goarch, archiveExt string) string {
	return fmt.Sprintf("helm-%s-%s-%s.%s", version, goos, goarch, archiveExt)
}

func checksumFileName(version, goos, goarch, archiveExt string) string {
	return archiveFileName(version, goos, goarch, archiveExt) + ".sha256sum"
}

func archiveCachePath(cacheDir, version, goos, goarch, archiveExt string) string {
	return filepath.Join(cacheDir, "archives", version, goos, goarch, archiveFileName(version, goos, goarch, archiveExt))
}

func binaryCachePath(cacheDir, version, goos, goarch string) string {
	return filepath.Join(cacheDir, "binaries", version, goos, goarch, installutil.ExecutableName(defaultToolName, goos))
}

func helmPlatform(goos, goarch string) (string, string, string, error) {
	platform := goos
	switch goos {
	case "linux", "darwin":
	case "windows":
		platform = "windows"
	default:
		return "", "", "", fmt.Errorf("unsupported operating system %q", goos)
	}

	arch := goarch
	switch goarch {
	case "amd64", "arm64", "arm", "386":
	default:
		return "", "", "", fmt.Errorf("unsupported architecture %q", goarch)
	}

	archiveExt := "tar.gz"
	if platform == "windows" {
		archiveExt = "zip"
	}
	return platform, arch, archiveExt, nil
}

func normalizeExactVersion(version string) string {
	trimmed := strings.TrimSpace(version)
	if strings.HasPrefix(trimmed, "v") {
		return trimmed
	}
	return "v" + trimmed
}

func osStat(path string) (string, error) {
	info, err := runtimeStat(path)
	if err != nil {
		return "", err
	}
	if info == nil {
		return "", fmt.Errorf("missing file info for %s", path)
	}
	return path, nil
}
