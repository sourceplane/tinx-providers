package provider

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sourceplane/tinx-providers/internal/installutil"
)

func TestResolveVersion(t *testing.T) {
	t.Parallel()

	if got := normalizeExactVersion("v1.14.8"); got != "1.14.8" {
		t.Fatalf("normalizeExactVersion() = %q", got)
	}
	if got, err := latestVersionFromIndex([]byte(`{"versions":{"1.14.7":{},"1.14.8":{},"1.15.0-beta1":{}}}`)); err != nil || got != "1.14.8" {
		t.Fatalf("latestVersionFromIndex() = %q, %v", got, err)
	}
	if _, err := resolveVersion(context.Background(), http.DefaultClient, t.TempDir(), []string{defaultIndexURL}, "1.14"); err == nil {
		t.Fatal("expected invalid version error")
	}
}

func TestInstallUsesCachedBinary(t *testing.T) {
	goos, goarch, archiveExt, err := terraformPlatform(runtime.GOOS, runtime.GOARCH)
	if err != nil {
		t.Skipf("unsupported runtime for test: %v", err)
	}

	version := "1.14.8"
	archive, err := zipArchive(installutil.ExecutableName(defaultToolName, goos), []byte("terraform-test-binary"))
	if err != nil {
		t.Fatalf("zipArchive() error = %v", err)
	}
	archiveDigest := sha256.Sum256(archive)
	archiveChecksum := hex.EncodeToString(archiveDigest[:])

	var requests atomic.Int64
	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests.Add(1)
		switch request.URL.Path {
		case "/" + version + "/" + archiveFileName(version, goos, goarch, archiveExt):
			_, _ = writer.Write(archive)
		case "/" + version + "/" + checksumFileName(version):
			_, _ = writer.Write([]byte(archiveChecksum + "  " + archiveFileName(version, goos, goarch, archiveExt) + "\n"))
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()

	cacheDir := t.TempDir()
	installer := NewInstaller()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	firstInstallDir := t.TempDir()
	firstResult, err := installer.Install(ctx, Config{
		RequestedVersion: version,
		InstallDir:       firstInstallDir,
		CacheDir:         cacheDir,
		DownloadMirrors:  []string{server.URL},
		HTTPClient:       server.Client(),
	})
	if err != nil {
		t.Fatalf("first Install() error = %v", err)
	}
	if firstResult.ResolvedVersion != version {
		t.Fatalf("resolved version = %q, want %q", firstResult.ResolvedVersion, version)
	}

	secondInstallDir := t.TempDir()
	secondResult, err := installer.Install(ctx, Config{
		RequestedVersion: version,
		InstallDir:       secondInstallDir,
		CacheDir:         cacheDir,
		DownloadMirrors:  []string{"https://127.0.0.1:1"},
	})
	if err != nil {
		t.Fatalf("second Install() error = %v", err)
	}
	if !secondResult.UsedCache {
		t.Fatal("expected second install to use cache")
	}
	if requests.Load() != 2 {
		t.Fatalf("network requests = %d, want 2", requests.Load())
	}
	if ok, err := installutil.FileMatchesChecksum(secondResult.BinaryPath, secondResult.SHA256); err != nil || !ok {
		t.Fatalf("installed binary checksum mismatch, ok=%t err=%v", ok, err)
	}
}
