package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/sourceplane/kiox-providers/providers/setup-gcloud/internal/provider"
)

func main() {
	var config provider.Config
	var outputFormat string
	var downloadMirrors urlFlag
	var metadataBaseURLs urlFlag
	var componentsURLs urlFlag

	fs := flag.NewFlagSet(os.Args[0], flag.ExitOnError)
	fs.StringVar(&config.RequestedVersion, "version", firstNonEmpty(os.Getenv("INPUT_VERSION"), os.Getenv("GCLOUD_VERSION"), os.Getenv("CLOUDSDK_VERSION")), "gcloud version to install")
	fs.StringVar(&config.InstallDir, "install-dir", os.Getenv("KIOX_TARGET_TOOL_INSTALL_DIR"), "override the target installation directory")
	fs.StringVar(&config.TargetBin, "bin", os.Getenv("KIOX_TARGET_TOOL_BIN"), "override the target binary path")
	fs.StringVar(&config.CacheDir, "cache-dir", os.Getenv("GCLOUD_CACHE_DIR"), "override the provider cache directory")
	fs.StringVar(&config.KioxHome, "kiox-home", os.Getenv("KIOX_HOME"), "override the kiox home used to derive the cache")
	fs.StringVar(&config.ToolName, "tool-name", firstNonEmpty(os.Getenv("KIOX_TARGET_TOOL_NAME"), "gcloud"), "tool name to materialize")
	fs.StringVar(&outputFormat, "output", firstNonEmpty(os.Getenv("KIOX_PROVIDER_OUTPUT"), "text"), "output format: text or json")
	fs.Var(&downloadMirrors, "mirror", "additional HTTPS gcloud archive base URL")
	fs.Var(&metadataBaseURLs, "metadata-url", "additional HTTPS Cloud Storage metadata base URL")
	fs.Var(&componentsURLs, "components-url", "additional HTTPS gcloud components manifest URL")
	_ = fs.Parse(os.Args[1:])

	if len(downloadMirrors) > 0 {
		config.DownloadMirrors = append(config.DownloadMirrors[:0], downloadMirrors...)
	} else {
		config.DownloadMirrors = provider.URLsFromEnv(os.Getenv("GCLOUD_DOWNLOAD_BASE_URLS"))
	}
	if len(metadataBaseURLs) > 0 {
		config.MetadataBaseURLs = append(config.MetadataBaseURLs[:0], metadataBaseURLs...)
	} else {
		config.MetadataBaseURLs = provider.URLsFromEnv(os.Getenv("GCLOUD_METADATA_BASE_URLS"))
	}
	if len(componentsURLs) > 0 {
		config.ComponentsURLs = append(config.ComponentsURLs[:0], componentsURLs...)
	} else {
		config.ComponentsURLs = provider.URLsFromEnv(os.Getenv("GCLOUD_COMPONENTS_URLS"))
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	result, err := provider.NewInstaller().Install(ctx, config)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	switch strings.ToLower(strings.TrimSpace(outputFormat)) {
	case "", "text":
		fmt.Printf("binary_path=%s\n", result.BinaryPath)
		fmt.Printf("resolved_version=%s\n", result.ResolvedVersion)
		fmt.Printf("sha256=%s\n", result.SHA256)
		fmt.Printf("used_cache=%t\n", result.UsedCache)
	case "json":
		encoder := json.NewEncoder(os.Stdout)
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(result); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	default:
		fmt.Fprintf(os.Stderr, "unsupported output format %q\n", outputFormat)
		os.Exit(1)
	}
}

type urlFlag []string

func (u *urlFlag) String() string {
	return strings.Join(*u, ",")
}

func (u *urlFlag) Set(value string) error {
	trimmed := strings.TrimSpace(value)
	if trimmed != "" {
		*u = append(*u, trimmed)
	}
	return nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}
