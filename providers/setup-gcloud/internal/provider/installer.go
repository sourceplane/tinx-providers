package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"

	"github.com/sourceplane/kiox-providers/internal/installutil"
)

const (
	defaultRequestedVersion = "latest"
	defaultStableVersion    = "565.0.0"
	defaultToolName         = "gcloud"
	defaultDownloadBaseURL  = "https://storage.googleapis.com/cloud-sdk-release"
	defaultMetadataBaseURL  = "https://storage.googleapis.com/storage/v1/b/cloud-sdk-release/o"
	defaultComponentsURL    = "https://dl.google.com/dl/cloudsdk/channels/rapid/components-2.json"
	installedSDKDirName     = "google-cloud-sdk"
	installedVersionFile    = ".kiox-gcloud-version"
)

var versionPattern = regexp.MustCompile(`^v?(\d+)\.(\d+)\.(\d+)$`)

type Config struct {
	RequestedVersion string
	InstallDir       string
	TargetBin        string
	CacheDir         string
	KioxHome         string
	ToolName         string
	DownloadMirrors  []string
	MetadataBaseURLs []string
	ComponentsURLs   []string
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

type componentsPayload struct {
	Version string `json:"version"`
}

type objectMetadataPayload struct {
	Name    string `json:"name"`
	MD5Hash string `json:"md5Hash"`
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
	metadataBaseURLs, err := installutil.SanitizeURLs(cfg.MetadataBaseURLs, []string{defaultMetadataBaseURL}, true)
	if err != nil {
		return Result{}, err
	}
	componentsURLs, err := installutil.SanitizeURLs(cfg.ComponentsURLs, []string{defaultComponentsURL}, true)
	if err != nil {
		return Result{}, err
	}

	cacheDir, err := installutil.ResolveCacheDir(cfg.CacheDir, cfg.KioxHome, "setup-gcloud")
	if err != nil {
		return Result{}, err
	}

	archiveSuffix, archiveExt, wrapperName, err := gcloudPlatform(runtime.GOOS, runtime.GOARCH)
	if err != nil {
		return Result{}, err
	}

	installDir, targetPath, err := resolveInstallPaths(cfg.InstallDir, cfg.TargetBin, wrapperName)
	if err != nil {
		return Result{}, err
	}

	requested := strings.TrimSpace(cfg.RequestedVersion)
	if requested == "" {
		requested = defaultRequestedVersion
	}

	resolvedVersion, err := resolveVersion(ctx, client, cacheDir, componentsURLs, requested)
	if err != nil {
		return Result{}, err
	}

	archiveName := archiveFileName(resolvedVersion, archiveSuffix, archiveExt)
	archivePath := archiveCachePath(cacheDir, resolvedVersion, archiveName)
	archiveMD5, err := loadOrFetchArchiveMD5(ctx, client, cacheDir, metadataBaseURLs, resolvedVersion, archiveName)
	if err != nil {
		return Result{}, err
	}

	usedCache := true
	if ok, err := installutil.FileMatchesMD5Base64(archivePath, archiveMD5); err != nil {
		return Result{}, err
	} else if !ok {
		usedCache = false
		if _, err := installutil.DownloadToFile(ctx, client, archiveURLs(downloadBaseURLs, archiveName), archivePath, "", 0o644); err != nil {
			return Result{}, err
		}
		if ok, err := installutil.FileMatchesMD5Base64(archivePath, archiveMD5); err != nil {
			return Result{}, err
		} else if !ok {
			return Result{}, fmt.Errorf("downloaded gcloud archive MD5 mismatch for %s", archiveName)
		}
	}

	versionFilePath := filepath.Join(installDir, installedVersionFile)
	if installedVersion, err := installutil.ReadTrimmedFile(versionFilePath); err == nil && installedVersion == resolvedVersion {
		if _, err := os.Stat(targetPath); err == nil {
			sha256, err := installutil.FileSHA256(targetPath)
			if err != nil {
				return Result{}, err
			}
			return Result{
				RequestedVersion: requested,
				ResolvedVersion:  resolvedVersion,
				BinaryPath:       targetPath,
				SHA256:           sha256,
				SourceURL:        archiveURLs(downloadBaseURLs, archiveName)[0],
				UsedCache:        true,
			}, nil
		}
	}

	if err := installFromArchive(archivePath, installDir, archiveExt); err != nil {
		return Result{}, err
	}
	if err := writeWrapper(targetPath, runtime.GOOS); err != nil {
		return Result{}, err
	}
	if err := installutil.WriteAtomic(versionFilePath, []byte(resolvedVersion+"\n"), 0o644); err != nil {
		return Result{}, err
	}

	sha256, err := installutil.FileSHA256(targetPath)
	if err != nil {
		return Result{}, err
	}

	return Result{
		RequestedVersion: requested,
		ResolvedVersion:  resolvedVersion,
		BinaryPath:       targetPath,
		SHA256:           sha256,
		SourceURL:        archiveURLs(downloadBaseURLs, archiveName)[0],
		UsedCache:        usedCache,
	}, nil
}

func resolveVersion(ctx context.Context, client *http.Client, cacheDir string, componentsURLs []string, requested string) (string, error) {
	trimmed := strings.TrimSpace(requested)
	if trimmed == "" || strings.EqualFold(trimmed, "latest") || strings.EqualFold(trimmed, "stable") {
		version, err := loadOrFetchLatestVersion(ctx, client, cacheDir, componentsURLs)
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

func loadOrFetchLatestVersion(ctx context.Context, client *http.Client, cacheDir string, componentsURLs []string) (string, error) {
	cachePath := filepath.Join(cacheDir, "versions", "latest.txt")
	if value, err := installutil.ReadTrimmedFile(cachePath); err == nil && value != "" {
		return value, nil
	}

	data, _, err := installutil.FetchFirst(ctx, client, componentsURLs)
	if err != nil {
		if value, cacheErr := installutil.ReadTrimmedFile(cachePath); cacheErr == nil && value != "" {
			return value, nil
		}
		return "", err
	}

	var payload componentsPayload
	if err := json.Unmarshal(data, &payload); err != nil {
		return "", fmt.Errorf("decode gcloud components manifest: %w", err)
	}
	version := normalizeExactVersion(payload.Version)
	if version == "" {
		return "", fmt.Errorf("gcloud components manifest did not include a version")
	}
	if err := installutil.WriteAtomic(cachePath, []byte(version+"\n"), 0o644); err != nil {
		return "", err
	}
	return version, nil
}

func loadOrFetchArchiveMD5(ctx context.Context, client *http.Client, cacheDir string, metadataBaseURLs []string, version, archiveName string) (string, error) {
	cachePath := filepath.Join(cacheDir, "checksums", version, archiveName+".md5")
	if value, err := installutil.ReadTrimmedFile(cachePath); err == nil && value != "" {
		return value, nil
	}

	data, _, err := installutil.FetchFirst(ctx, client, metadataURLs(metadataBaseURLs, archiveName))
	if err != nil {
		return "", err
	}
	var payload objectMetadataPayload
	if err := json.Unmarshal(data, &payload); err != nil {
		return "", fmt.Errorf("decode gcloud object metadata: %w", err)
	}
	if payload.MD5Hash == "" {
		return "", fmt.Errorf("gcloud object metadata for %s did not include md5Hash", archiveName)
	}
	if err := installutil.WriteAtomic(cachePath, []byte(payload.MD5Hash+"\n"), 0o644); err != nil {
		return "", err
	}
	return payload.MD5Hash, nil
}

func resolveInstallPaths(installDir, targetBin, wrapperName string) (string, string, error) {
	trimmedInstallDir := strings.TrimSpace(installDir)
	trimmedTargetBin := strings.TrimSpace(targetBin)
	if trimmedInstallDir == "" && trimmedTargetBin == "" {
		return "", "", fmt.Errorf("KIOX_TARGET_TOOL_INSTALL_DIR or KIOX_TARGET_TOOL_BIN must be set")
	}

	if trimmedTargetBin != "" {
		absoluteTargetPath, err := filepath.Abs(trimmedTargetBin)
		if err != nil {
			return "", "", err
		}
		if trimmedInstallDir == "" {
			return filepath.Dir(filepath.Dir(absoluteTargetPath)), absoluteTargetPath, nil
		}
		absoluteInstallDir, err := filepath.Abs(trimmedInstallDir)
		if err != nil {
			return "", "", err
		}
		return absoluteInstallDir, absoluteTargetPath, nil
	}

	absoluteInstallDir, err := filepath.Abs(trimmedInstallDir)
	if err != nil {
		return "", "", err
	}
	return absoluteInstallDir, filepath.Join(absoluteInstallDir, "bin", wrapperName), nil
}

func installFromArchive(archivePath, installDir, archiveExt string) error {
	if err := os.MkdirAll(installDir, 0o755); err != nil {
		return err
	}
	tempRoot, err := os.MkdirTemp(installDir, ".extract-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tempRoot)

	if archiveExt == "zip" {
		if err := installutil.ExtractZip(archivePath, tempRoot); err != nil {
			return err
		}
	} else {
		if err := installutil.ExtractTarGz(archivePath, tempRoot); err != nil {
			return err
		}
	}

	sdkSource := filepath.Join(tempRoot, installedSDKDirName)
	if stat, err := os.Stat(sdkSource); err != nil {
		return err
	} else if !stat.IsDir() {
		return fmt.Errorf("expected %s to be a directory", sdkSource)
	}

	sdkDestination := filepath.Join(installDir, installedSDKDirName)
	if err := os.RemoveAll(sdkDestination); err != nil {
		return err
	}
	return os.Rename(sdkSource, sdkDestination)
}

func writeWrapper(targetPath, goos string) error {
	var content string
	perm := os.FileMode(0o755)
	if goos == "windows" {
		content = "@echo off\r\nset SCRIPT_DIR=%~dp0\r\ncall \"%SCRIPT_DIR%..\\google-cloud-sdk\\bin\\gcloud.cmd\" %*\r\n"
		perm = 0o644
	} else {
		content = "#!/bin/sh\nset -eu\nSCRIPT_DIR=$(CDPATH= cd -- \"$(dirname -- \"$0\")\" && pwd)\nexec \"$SCRIPT_DIR/../google-cloud-sdk/bin/gcloud\" \"$@\"\n"
	}
	return installutil.WriteAtomic(targetPath, []byte(content), perm)
}

func archiveURLs(downloadBaseURLs []string, archiveName string) []string {
	return installutil.JoinURLPaths(downloadBaseURLs, archiveName)
}

func metadataURLs(metadataBaseURLs []string, archiveName string) []string {
	urls := make([]string, 0, len(metadataBaseURLs))
	escapedName := url.PathEscape(archiveName)
	for _, baseURL := range metadataBaseURLs {
		urls = append(urls, strings.TrimRight(baseURL, "/")+"/"+escapedName+"?fields=name,md5Hash")
	}
	return urls
}

func archiveFileName(version, archiveSuffix, archiveExt string) string {
	return fmt.Sprintf("google-cloud-cli-%s-%s.%s", version, archiveSuffix, archiveExt)
}

func archiveCachePath(cacheDir, version, archiveName string) string {
	return filepath.Join(cacheDir, "archives", version, archiveName)
}

func gcloudPlatform(goos, goarch string) (string, string, string, error) {
	switch goos {
	case "darwin":
		switch goarch {
		case "amd64":
			return "darwin-x86_64", "tar.gz", defaultToolName, nil
		case "arm64":
			return "darwin-arm", "tar.gz", defaultToolName, nil
		}
	case "linux":
		switch goarch {
		case "amd64":
			return "linux-x86_64", "tar.gz", defaultToolName, nil
		case "arm64":
			return "linux-arm", "tar.gz", defaultToolName, nil
		}
	case "windows":
		switch goarch {
		case "amd64":
			return "windows-x86_64", "zip", defaultToolName + ".cmd", nil
		case "arm64":
			return "windows-arm", "zip", defaultToolName + ".cmd", nil
		}
	}
	return "", "", "", fmt.Errorf("unsupported platform %q/%q", goos, goarch)
}

func normalizeExactVersion(version string) string {
	return strings.TrimPrefix(strings.TrimSpace(version), "v")
}
