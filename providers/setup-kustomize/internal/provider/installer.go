package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"

	"github.com/sourceplane/tinx-providers/internal/installutil"
)

const (
	defaultRequestedVersion   = "latest"
	defaultStableVersion      = "v5.8.1"
	defaultToolName           = "kustomize"
	defaultDownloadBaseURL    = "https://github.com/kubernetes-sigs/kustomize/releases/download"
	defaultLatestReleaseAPI   = "https://api.github.com/repos/kubernetes-sigs/kustomize/releases/latest"
	defaultDownloadPathPrefix = "kustomize"
)

var versionPattern = regexp.MustCompile(`^v?(\d+)\.(\d+)\.(\d+)$`)

type Config struct {
	RequestedVersion string
	InstallDir       string
	TargetBin        string
	CacheDir         string
	TinxHome         string
	ToolName         string
	DownloadMirrors  []string
	ReleaseAPIURLs   []string
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

type latestReleasePayload struct {
	TagName string `json:"tag_name"`
}

func NewInstaller() *Installer {
	return &Installer{client: installutil.NewHTTPClient()}
}

func URLsFromEnv(raw string) []string {
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

	downloadBaseURLs, err := installutil.SanitizeURLs(cfg.DownloadMirrors, []string{defaultDownloadBaseURL}, true)
	if err != nil {
		return Result{}, err
	}
	releaseAPIURLs, err := installutil.SanitizeURLs(cfg.ReleaseAPIURLs, []string{defaultLatestReleaseAPI}, true)
	if err != nil {
		return Result{}, err
	}

	cacheDir, err := installutil.ResolveCacheDir(cfg.CacheDir, cfg.TinxHome, "setup-kustomize")
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

	goos, goarch, archiveExt, err := kustomizePlatform(runtime.GOOS, runtime.GOARCH)
	if err != nil {
		return Result{}, err
	}

	requested := strings.TrimSpace(cfg.RequestedVersion)
	if requested == "" {
		requested = defaultRequestedVersion
	}

	resolvedVersion, err := resolveVersion(ctx, client, cacheDir, releaseAPIURLs, requested)
	if err != nil {
		return Result{}, err
	}

	cacheBinaryPath := binaryCachePath(cacheDir, resolvedVersion, goos, goarch)
	binaryDigest := ""
	usedCache := false
	if _, err := os.Stat(cacheBinaryPath); err == nil {
		binaryDigest, err = installutil.FileSHA256(cacheBinaryPath)
		if err != nil {
			return Result{}, err
		}
		usedCache = true
	} else {
		archiveChecksum, err := loadOrFetchArchiveChecksum(ctx, client, cacheDir, downloadBaseURLs, resolvedVersion, goos, goarch, archiveExt)
		if err != nil {
			return Result{}, err
		}
		archivePath := archiveCachePath(cacheDir, resolvedVersion, goos, goarch, archiveExt)
		if ok, err := installutil.FileMatchesChecksum(archivePath, archiveChecksum); err != nil {
			return Result{}, err
		} else if !ok {
			if _, err := installutil.DownloadToFile(ctx, client, archiveURLs(downloadBaseURLs, resolvedVersion, goos, goarch, archiveExt), archivePath, archiveChecksum, 0o644); err != nil {
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

	primarySourceURL := archiveURLs(downloadBaseURLs, resolvedVersion, goos, goarch, archiveExt)[0]
	if ok, err := installutil.FileMatchesChecksum(targetPath, binaryDigest); err == nil && ok {
		return Result{
			RequestedVersion: requested,
			ResolvedVersion:  resolvedVersion,
			BinaryPath:       targetPath,
			SHA256:           binaryDigest,
			SourceURL:        primarySourceURL,
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
		SourceURL:        primarySourceURL,
		UsedCache:        usedCache,
	}, nil
}

func resolveVersion(ctx context.Context, client *http.Client, cacheDir string, releaseAPIURLs []string, requested string) (string, error) {
	trimmed := strings.TrimSpace(requested)
	if trimmed == "" || strings.EqualFold(trimmed, "latest") || strings.EqualFold(trimmed, "stable") {
		version, err := loadOrFetchLatestVersion(ctx, client, cacheDir, releaseAPIURLs)
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

func loadOrFetchLatestVersion(ctx context.Context, client *http.Client, cacheDir string, releaseAPIURLs []string) (string, error) {
	cachePath := filepath.Join(cacheDir, "versions", "latest.txt")
	if value, err := installutil.ReadTrimmedFile(cachePath); err == nil && value != "" {
		return value, nil
	}

	data, _, err := installutil.FetchFirst(ctx, client, releaseAPIURLs)
	if err != nil {
		if value, cacheErr := installutil.ReadTrimmedFile(cachePath); cacheErr == nil && value != "" {
			return value, nil
		}
		return "", err
	}

	var payload latestReleasePayload
	if err := json.Unmarshal(data, &payload); err != nil {
		return "", fmt.Errorf("decode latest Kustomize release metadata: %w", err)
	}
	version := releaseVersionFromTag(payload.TagName)
	if version == "" {
		return "", fmt.Errorf("latest Kustomize release tag %q did not contain a version", payload.TagName)
	}
	version = normalizeExactVersion(version)
	if err := installutil.WriteAtomic(cachePath, []byte(version+"\n"), 0o644); err != nil {
		return "", err
	}
	return version, nil
}

func loadOrFetchArchiveChecksum(ctx context.Context, client *http.Client, cacheDir string, downloadBaseURLs []string, version, goos, goarch, archiveExt string) (string, error) {
	cachePath := filepath.Join(cacheDir, "checksums", version, goos, goarch, checksumFileName())
	if value, err := installutil.ReadTrimmedFile(cachePath); err == nil && value != "" {
		return value, nil
	}

	data, _, err := installutil.FetchFirst(ctx, client, checksumURLs(downloadBaseURLs, version))
	if err != nil {
		return "", err
	}
	checksum, err := checksumForArchive(string(data), archiveFileName(version, goos, goarch, archiveExt))
	if err != nil {
		return "", err
	}
	if err := installutil.WriteAtomic(cachePath, []byte(checksum+"\n"), 0o644); err != nil {
		return "", err
	}
	return checksum, nil
}

func extractBinaryToCache(archivePath, cacheBinaryPath, goos string) error {
	workDir := filepath.Join(filepath.Dir(cacheBinaryPath), "extract")
	if err := os.RemoveAll(workDir); err != nil {
		return err
	}
	if err := os.MkdirAll(workDir, 0o755); err != nil {
		return err
	}
	defer os.RemoveAll(workDir)

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

func archiveURLs(downloadBaseURLs []string, version, goos, goarch, archiveExt string) []string {
	return installutil.JoinURLPaths(downloadBaseURLs, downloadRelativePath(version, archiveFileName(version, goos, goarch, archiveExt)))
}

func checksumURLs(downloadBaseURLs []string, version string) []string {
	return installutil.JoinURLPaths(downloadBaseURLs, downloadRelativePath(version, checksumFileName()))
}

func downloadRelativePath(version, fileName string) string {
	return filepath.ToSlash(filepath.Join(defaultDownloadPathPrefix, version, fileName))
}

func archiveFileName(version, goos, goarch, archiveExt string) string {
	return fmt.Sprintf("kustomize_%s_%s_%s.%s", version, goos, goarch, archiveExt)
}

func checksumFileName() string {
	return "checksums.txt"
}

func archiveCachePath(cacheDir, version, goos, goarch, archiveExt string) string {
	return filepath.Join(cacheDir, "archives", version, goos, goarch, archiveFileName(version, goos, goarch, archiveExt))
}

func binaryCachePath(cacheDir, version, goos, goarch string) string {
	return filepath.Join(cacheDir, "binaries", version, goos, goarch, installutil.ExecutableName(defaultToolName, goos))
}

func checksumForArchive(data, archiveName string) (string, error) {
	for _, line := range strings.Split(data, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		if fields[1] == archiveName {
			return strings.TrimSpace(fields[0]), nil
		}
	}
	return "", fmt.Errorf("checksum for %s not found", archiveName)
}

func kustomizePlatform(goos, goarch string) (string, string, string, error) {
	switch goos {
	case "linux", "darwin", "windows":
	default:
		return "", "", "", fmt.Errorf("unsupported operating system %q", goos)
	}

	switch goarch {
	case "amd64", "arm64":
	default:
		return "", "", "", fmt.Errorf("unsupported architecture %q", goarch)
	}

	archiveExt := "tar.gz"
	if goos == "windows" {
		archiveExt = "zip"
	}
	return goos, goarch, archiveExt, nil
}

func normalizeExactVersion(version string) string {
	trimmed := strings.TrimSpace(version)
	if strings.HasPrefix(trimmed, "v") {
		return trimmed
	}
	return "v" + trimmed
}

func releaseVersionFromTag(tag string) string {
	trimmed := strings.TrimSpace(tag)
	if trimmed == "" {
		return ""
	}
	if idx := strings.LastIndex(trimmed, "/"); idx >= 0 {
		trimmed = trimmed[idx+1:]
	}
	if !versionPattern.MatchString(trimmed) {
		return ""
	}
	return trimmed
}
