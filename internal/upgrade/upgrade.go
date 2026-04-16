// Package upgrade implements the "tmux-sidebar upgrade" subcommand, which
// downloads the latest release from GitHub and replaces the current binary.
package upgrade

import (
	"archive/tar"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

const (
	repoOwner = "ishii1648"
	repoName  = "tmux-sidebar"
)

var (
	styleOK   = lipgloss.NewStyle().Foreground(lipgloss.Color("2")).Bold(true)
	styleInfo = lipgloss.NewStyle().Foreground(lipgloss.Color("4")).Bold(true)
	styleErr  = lipgloss.NewStyle().Foreground(lipgloss.Color("1")).Bold(true)
)

// githubRelease represents a subset of the GitHub release API response.
type githubRelease struct {
	TagName string        `json:"tag_name"`
	Assets  []githubAsset `json:"assets"`
}

// githubAsset represents a single release asset.
type githubAsset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
}

// Run executes the upgrade subcommand.
// currentVersion is the compiled-in version string (e.g. "0.5.0" or "dev").
func Run(currentVersion string) error {
	fmt.Println("tmux-sidebar upgrade")
	fmt.Println(strings.Repeat("─", 40))
	fmt.Println()

	// 1. Fetch latest release metadata.
	fmt.Println(styleInfo.Render("Checking latest release..."))
	release, err := fetchLatestRelease()
	if err != nil {
		return fmt.Errorf("fetch latest release: %w", err)
	}

	latestVersion := strings.TrimPrefix(release.TagName, "v")
	fmt.Printf("  Current version : %s\n", currentVersion)
	fmt.Printf("  Latest version  : %s\n", latestVersion)
	fmt.Println()

	if currentVersion == latestVersion {
		fmt.Println(styleOK.Render("Already up to date."))
		return nil
	}

	// 2. Find the matching asset for OS/arch.
	assetName := fmt.Sprintf("%s_%s_%s.tar.gz", repoName, runtime.GOOS, runtime.GOARCH)
	var downloadURL string
	for _, a := range release.Assets {
		if a.Name == assetName {
			downloadURL = a.BrowserDownloadURL
			break
		}
	}
	if downloadURL == "" {
		return fmt.Errorf("no release asset found for %s/%s (expected %s)", runtime.GOOS, runtime.GOARCH, assetName)
	}

	// 3. Download the archive.
	fmt.Printf(styleInfo.Render("Downloading %s...")+"\n", assetName)
	archivePath, err := downloadToTemp(downloadURL)
	if err != nil {
		return fmt.Errorf("download: %w", err)
	}
	defer os.Remove(archivePath)

	// 4. Extract the binary from the tar.gz.
	binaryPath, err := extractBinary(archivePath, repoName)
	if err != nil {
		return fmt.Errorf("extract: %w", err)
	}
	defer os.Remove(binaryPath)

	// 5. Determine install destinations.
	destinations := installDestinations()
	if len(destinations) == 0 {
		return fmt.Errorf("could not determine install location")
	}

	// 6. Install to each destination.
	fmt.Println(styleInfo.Render("Installing..."))
	for _, dest := range destinations {
		if err := installBinary(binaryPath, dest); err != nil {
			fmt.Fprintf(os.Stderr, "  warning: %s: %v\n", dest, err)
			continue
		}
		// Ad-hoc codesign on macOS.
		if runtime.GOOS == "darwin" {
			_ = exec.Command("codesign", "--sign", "-", dest).Run()
		}
		fmt.Printf("  %s %s\n", styleOK.Render("✓"), dest)
	}
	fmt.Println()

	fmt.Println(styleOK.Render(fmt.Sprintf("Upgraded to %s.", latestVersion)))
	fmt.Println("  Run " + styleInfo.Render("tmux-sidebar restart") + " to apply to running sidebars.")
	return nil
}

// fetchLatestRelease queries the GitHub API for the latest release.
func fetchLatestRelease() (*githubRelease, error) {
	url := fmt.Sprintf("https://api.github.com/repos/%s/%s/releases/latest", repoOwner, repoName)
	resp, err := http.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GitHub API returned %s", resp.Status)
	}

	var release githubRelease
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	return &release, nil
}

// downloadToTemp downloads the given URL into a temporary file and returns its path.
func downloadToTemp(url string) (string, error) {
	resp, err := http.Get(url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("HTTP %s", resp.Status)
	}

	tmp, err := os.CreateTemp("", "tmux-sidebar-upgrade-*.tar.gz")
	if err != nil {
		return "", err
	}
	defer tmp.Close()

	if _, err := io.Copy(tmp, resp.Body); err != nil {
		os.Remove(tmp.Name())
		return "", err
	}
	return tmp.Name(), nil
}

// extractBinary opens a tar.gz archive and extracts the named binary to a temp file.
func extractBinary(archivePath, binaryName string) (string, error) {
	f, err := os.Open(archivePath)
	if err != nil {
		return "", err
	}
	defer f.Close()

	gz, err := gzip.NewReader(f)
	if err != nil {
		return "", err
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", err
		}
		if filepath.Base(hdr.Name) == binaryName && hdr.Typeflag == tar.TypeReg {
			tmp, err := os.CreateTemp("", "tmux-sidebar-bin-*")
			if err != nil {
				return "", err
			}
			if _, err := io.Copy(tmp, tr); err != nil {
				tmp.Close()
				os.Remove(tmp.Name())
				return "", err
			}
			tmp.Close()
			if err := os.Chmod(tmp.Name(), 0o755); err != nil {
				os.Remove(tmp.Name())
				return "", err
			}
			return tmp.Name(), nil
		}
	}
	return "", fmt.Errorf("binary %q not found in archive", binaryName)
}

// installDestinations returns the list of paths where the binary should be installed.
func installDestinations() []string {
	var paths []string

	// Primary: ~/.local/bin (matches Makefile install target).
	home, err := os.UserHomeDir()
	if err == nil {
		localBin := filepath.Join(home, ".local", "bin", repoName)
		if _, err := os.Stat(localBin); err == nil {
			paths = append(paths, localBin)
		}
	}

	// Secondary: GOPATH/bin (matches Makefile install target).
	if gopath := os.Getenv("GOPATH"); gopath != "" {
		gopathBin := filepath.Join(gopath, "bin", repoName)
		if _, err := os.Stat(gopathBin); err == nil {
			paths = append(paths, gopathBin)
		}
	} else if home != "" {
		gopathBin := filepath.Join(home, "go", "bin", repoName)
		if _, err := os.Stat(gopathBin); err == nil {
			paths = append(paths, gopathBin)
		}
	}

	// Fallback: if no known paths found, try the executable's own path.
	if len(paths) == 0 {
		if exe, err := os.Executable(); err == nil {
			if real, err := filepath.EvalSymlinks(exe); err == nil {
				paths = append(paths, real)
			} else {
				paths = append(paths, exe)
			}
		}
	}

	return paths
}

// installBinary copies src to dst using atomic rename (write to temp in same dir, then rename).
func installBinary(src, dst string) error {
	srcFile, err := os.Open(src)
	if err != nil {
		return err
	}
	defer srcFile.Close()

	dir := filepath.Dir(dst)
	tmp, err := os.CreateTemp(dir, ".tmux-sidebar-upgrade-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()

	if _, err := io.Copy(tmp, srcFile); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return err
	}
	tmp.Close()

	if err := os.Chmod(tmpPath, 0o755); err != nil {
		os.Remove(tmpPath)
		return err
	}

	if err := os.Rename(tmpPath, dst); err != nil {
		os.Remove(tmpPath)
		return err
	}
	return nil
}
