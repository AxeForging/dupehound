package integration

import (
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestInstallScript_InstallsVerifiedReleaseArchive(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("install.sh targets POSIX systems")
	}

	repoRoot := filepath.Clean("..")
	tmp := t.TempDir()
	payload := filepath.Join(tmp, "payload")
	if err := os.Mkdir(payload, 0o755); err != nil {
		t.Fatal(err)
	}

	osName, archName := installerPlatform(t)
	binaryName := "dupehound-" + osName + "-" + archName
	binaryPath := filepath.Join(payload, binaryName)
	if err := os.WriteFile(binaryPath, []byte("#!/bin/sh\nprintf 'installed dupehound\\n'\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	asset := binaryName + ".tar.gz"
	archive := filepath.Join(tmp, asset)
	tarCmd := exec.Command("tar", "-czf", archive, "-C", payload, binaryName)
	if output, err := tarCmd.CombinedOutput(); err != nil {
		t.Fatalf("create fixture archive: %v\n%s", err, output)
	}
	archiveBytes, err := os.ReadFile(archive)
	if err != nil {
		t.Fatal(err)
	}
	checksum := fmt.Sprintf("%x  %s\n", sha256.Sum256(archiveBytes), asset)
	checksums := filepath.Join(tmp, "checksums.txt")
	if err := os.WriteFile(checksums, []byte(checksum), 0o644); err != nil {
		t.Fatal(err)
	}

	fakeBin := filepath.Join(tmp, "fake-bin")
	if err := os.Mkdir(fakeBin, 0o755); err != nil {
		t.Fatal(err)
	}
	curlLog := filepath.Join(tmp, "curl.log")
	fakeCurl := `#!/bin/sh
set -eu
url=""
out=""
while [ "$#" -gt 0 ]; do
  case "$1" in
    -o) out="$2"; shift 2 ;;
    -*) shift ;;
    *) url="$1"; shift ;;
  esac
done
printf '%s\n' "$url" >> "$FAKE_CURL_LOG"
case "$url" in
  */checksums.txt) cp "$FIXTURE_CHECKSUMS" "$out" ;;
  *) cp "$FIXTURE_ARCHIVE" "$out" ;;
esac
`
	if err := os.WriteFile(filepath.Join(fakeBin, "curl"), []byte(fakeCurl), 0o755); err != nil {
		t.Fatal(err)
	}

	installDir := filepath.Join(tmp, "install")
	cmd := exec.Command("sh", filepath.Join(repoRoot, "install.sh"))
	cmd.Env = append(os.Environ(),
		"PATH="+fakeBin+string(os.PathListSeparator)+os.Getenv("PATH"),
		"DUPEHOUND_VERSION=v0.1.0",
		"DUPEHOUND_INSTALL_DIR="+installDir,
		"FIXTURE_ARCHIVE="+archive,
		"FIXTURE_CHECKSUMS="+checksums,
		"FAKE_CURL_LOG="+curlLog,
	)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("install script failed: %v\n%s", err, output)
	}

	installed := filepath.Join(installDir, "dupehound")
	run := exec.Command(installed)
	runOutput, err := run.CombinedOutput()
	if err != nil {
		t.Fatalf("installed binary failed: %v\n%s", err, runOutput)
	}
	if got := strings.TrimSpace(string(runOutput)); got != "installed dupehound" {
		t.Fatalf("installed binary output = %q", got)
	}

	logBytes, err := os.ReadFile(curlLog)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(logBytes), "/releases/download/v0.1.0/"+asset) {
		t.Fatalf("versioned release URL not requested:\n%s", logBytes)
	}
}

func installerPlatform(t *testing.T) (string, string) {
	t.Helper()
	osName := runtime.GOOS
	archName := runtime.GOARCH
	if osName != "linux" && osName != "darwin" {
		t.Skipf("unsupported test OS %s", osName)
	}
	switch archName {
	case "amd64", "arm64", "386", "arm":
	default:
		t.Skipf("unsupported test architecture %s", archName)
	}
	return osName, archName
}
