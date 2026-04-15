package installutil

import (
	"context"
	"crypto/md5"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

func NewHTTPClient() *http.Client {
	return &http.Client{Timeout: 5 * time.Minute}
}

func ExecutableName(name, goos string) string {
	if goos == "windows" && !strings.HasSuffix(strings.ToLower(name), ".exe") {
		return name + ".exe"
	}
	return name
}

func ResolveTargetPath(targetBin, installDir, toolName, goos string) (string, error) {
	if trimmed := strings.TrimSpace(targetBin); trimmed != "" {
		return filepath.Abs(trimmed)
	}

	if strings.TrimSpace(installDir) == "" {
		return "", errors.New("TINX_TARGET_TOOL_INSTALL_DIR or TINX_TARGET_TOOL_BIN must be set")
	}

	targetPath := filepath.Join(strings.TrimSpace(installDir), "bin", ExecutableName(toolName, goos))
	return filepath.Abs(targetPath)
}

func ResolveCacheDir(explicitCacheDir, tinxHome string, segments ...string) (string, error) {
	if trimmed := strings.TrimSpace(explicitCacheDir); trimmed != "" {
		return filepath.Abs(trimmed)
	}

	base := strings.TrimSpace(tinxHome)
	if base == "" {
		homeDir, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolve home directory: %w", err)
		}
		base = filepath.Join(homeDir, ".tinx")
	}

	parts := append([]string{base, "cache", "providers"}, segments...)
	return filepath.Join(parts...), nil
}

func SanitizeURLs(values []string, defaults []string, requireHTTPS bool) ([]string, error) {
	urls := values
	if len(urls) == 0 {
		urls = defaults
	}

	clean := make([]string, 0, len(urls))
	seen := map[string]struct{}{}
	for _, value := range urls {
		trimmed := strings.TrimRight(strings.TrimSpace(value), "/")
		if trimmed == "" {
			continue
		}
		parsed, err := url.Parse(trimmed)
		if err != nil {
			return nil, fmt.Errorf("invalid URL %q: %w", value, err)
		}
		if requireHTTPS && parsed.Scheme != "https" {
			return nil, fmt.Errorf("URL must use HTTPS: %s", trimmed)
		}
		if _, ok := seen[trimmed]; ok {
			continue
		}
		seen[trimmed] = struct{}{}
		clean = append(clean, trimmed)
	}

	if len(clean) == 0 {
		return nil, errors.New("at least one URL is required")
	}

	return clean, nil
}

func JoinURLPath(baseURL, relativePath string) string {
	return strings.TrimRight(baseURL, "/") + "/" + strings.TrimLeft(relativePath, "/")
}

func JoinURLPaths(baseURLs []string, relativePath string) []string {
	joined := make([]string, 0, len(baseURLs))
	for _, baseURL := range baseURLs {
		joined = append(joined, JoinURLPath(baseURL, relativePath))
	}
	return joined
}

func FetchFirst(ctx context.Context, client *http.Client, sourceURLs []string) ([]byte, string, error) {
	var failures []string
	for _, sourceURL := range sourceURLs {
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

	return nil, "", fmt.Errorf("all URLs failed: %s", strings.Join(failures, "; "))
}

func DownloadToFile(ctx context.Context, client *http.Client, sourceURLs []string, targetPath string, expectedSHA256 string, perm os.FileMode) (string, error) {
	if err := os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
		return "", err
	}

	var failures []string
	for _, sourceURL := range sourceURLs {
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

		if response.StatusCode != http.StatusOK {
			response.Body.Close()
			failures = append(failures, fmt.Sprintf("%s: unexpected status %s", sourceURL, response.Status))
			continue
		}

		tempFile, err := os.CreateTemp(filepath.Dir(targetPath), ".download-*")
		if err != nil {
			response.Body.Close()
			return "", err
		}
		tempPath := tempFile.Name()
		defer os.Remove(tempPath)

		hasher := sha256.New()
		writer := io.MultiWriter(tempFile, hasher)
		_, copyErr := io.Copy(writer, response.Body)
		closeErr := response.Body.Close()
		if syncErr := tempFile.Close(); copyErr == nil {
			copyErr = syncErr
		}
		if copyErr == nil {
			copyErr = closeErr
		}
		if copyErr != nil {
			failures = append(failures, fmt.Sprintf("%s: %v", sourceURL, copyErr))
			continue
		}

		if expectedSHA256 != "" {
			actual := hex.EncodeToString(hasher.Sum(nil))
			if !strings.EqualFold(expectedSHA256, actual) {
				failures = append(failures, fmt.Sprintf("%s: checksum mismatch", sourceURL))
				continue
			}
		}

		if err := os.Chmod(tempPath, perm); err != nil {
			return "", err
		}
		if err := ReplaceFile(tempPath, targetPath); err != nil {
			return "", err
		}
		return sourceURL, nil
	}

	return "", fmt.Errorf("all URLs failed: %s", strings.Join(failures, "; "))
}

func FileSHA256(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()

	hasher := sha256.New()
	if _, err := io.Copy(hasher, file); err != nil {
		return "", err
	}

	return hex.EncodeToString(hasher.Sum(nil)), nil
}

func FileMD5Base64(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()

	hasher := md5.New()
	if _, err := io.Copy(hasher, file); err != nil {
		return "", err
	}

	return base64.StdEncoding.EncodeToString(hasher.Sum(nil)), nil
}

func FileMatchesChecksum(path, expected string) (bool, error) {
	actual, err := FileSHA256(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, err
	}

	return strings.EqualFold(expected, actual), nil
}

func FileMatchesMD5Base64(path, expected string) (bool, error) {
	actual, err := FileMD5Base64(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, err
	}

	return strings.EqualFold(expected, actual), nil
}

func LinkOrCopyFile(sourcePath, targetPath string, perm os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
		return err
	}
	if err := os.Remove(targetPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.Symlink(sourcePath, targetPath); err == nil {
		return nil
	}
	return CopyFileAtomic(sourcePath, targetPath, perm)
}

func CopyFileAtomic(sourcePath, targetPath string, perm os.FileMode) error {
	input, err := os.Open(sourcePath)
	if err != nil {
		return err
	}
	defer input.Close()

	if err := os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
		return err
	}

	tempFile, err := os.CreateTemp(filepath.Dir(targetPath), ".copy-*")
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

	return ReplaceFile(tempPath, targetPath)
}

func WriteAtomic(path string, data []byte, perm os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}

	tempFile, err := os.CreateTemp(filepath.Dir(path), ".write-*")
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

	return ReplaceFile(tempPath, path)
}

func ReplaceFile(tempPath, targetPath string) error {
	if err := os.Rename(tempPath, targetPath); err == nil {
		return nil
	}
	if removeErr := os.Remove(targetPath); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
		return removeErr
	}
	return os.Rename(tempPath, targetPath)
}

func ReadTrimmedFile(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(data)), nil
}

func CurrentPlatform() (string, string) {
	return runtime.GOOS, runtime.GOARCH
}
