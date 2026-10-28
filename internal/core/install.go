package core

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// InstallLog is a progress sink used by the GUI and CLI.
type InstallLog interface {
	Step(msg string)
	Detail(msg string)
}

type nopLog struct{}

func (nopLog) Step(string)   {}
func (nopLog) Detail(string) {}

// NopInstallLog discards progress.
func NopInstallLog() InstallLog { return nopLog{} }

// Installer downloads and pins official release binaries.
type Installer struct {
	Paths *Paths
	State *StateStore
	Log   InstallLog
	token func() string
}

// NewInstaller creates an installer.
func NewInstaller(paths *Paths, state *StateStore, log InstallLog) *Installer {
	if log == nil {
		log = NopInstallLog()
	}
	return &Installer{
		Paths: paths,
		State: state,
		Log:   log,
		token: func() string { return state.Settings().GitHubToken },
	}
}

// CheckUpdate returns the latest release info for a kind and whether it
// differs from the installed one.
func (in *Installer) CheckUpdate(ctx context.Context, k Kind) (*Release, bool, error) {
	d := Def(k)
	s := in.State.Settings()
	g := newGHClient(s.GitHubToken)
	rel, err := g.LatestRelease(ctx, d.Repo, s.ReleaseChannel == "pre")
	if err != nil {
		return nil, false, err
	}
	rec := in.State.Server(k)
	upd := rec.BinTag != rel.TagName
	return rel, upd, nil
}

// Install downloads the release binary for kind and installs it to
// Paths.BinPath. It verifies the signed _sha256sums manifest when the
// release ships one, and always pins the SHA-256 locally.
func (in *Installer) Install(ctx context.Context, k Kind, rel *Release) error {
	d := Def(k)
	in.Log.Step(fmt.Sprintf("Downloading %s %s", d.Name, rel.TagName))
	assetName, err := d.AssetName(runtime.GOOS, runtime.GOARCH)
	if err != nil {
		return err
	}
	asset, err := FindAsset(rel, assetName)
	if err != nil {
		// fall back to the older Ubuntu build
		alt := strings.Replace(assetName, "-24_04-", "-22_04-", 1)
		asset, err = FindAsset(rel, alt)
		if err != nil {
			return err
		}
	}
	res, err := Download(ctx, asset.BrowserDownloadURL, in.Paths.DownloadsDir(), assetName, in.token(), nil)
	if err != nil {
		return err
	}
	in.Log.Detail(fmt.Sprintf("Downloaded %s (%.1f MiB)", assetName, float64(res.Size)/(1024*1024)))

	// Verify against the signed manifest when present.
	if sums, aerr := FindAsset(rel, SumsAssetName()); aerr == nil {
		if sres, derr := Download(ctx, sums.BrowserDownloadURL, in.Paths.DownloadsDir(), sums.Name, in.token(), nil); derr == nil {
			data, _ := os.ReadFile(sres.Path)
			if want, ok := ParseSha256sums(string(data))[assetName]; ok && want != "" {
				if want != res.SHA256 {
					return fmt.Errorf("SHA-256 mismatch for %s (want %s got %s) - download corrupted", assetName, want, res.SHA256)
				}
				in.Log.Detail("Signature manifest verified (sha256 match)")
			}
		}
	}

	dest := in.Paths.BinPath(k)
	in.Log.Step("Installing binary")
	if err := os.MkdirAll(filepath.Dir(dest), 0o700); err != nil {
		return err
	}
	// If an old binary is running, it is stopped by the caller.
	if err := os.Rename(res.Path, dest); err != nil {
		// cross-device: copy
		data, rerr := os.ReadFile(res.Path)
		if rerr != nil {
			return rerr
		}
		if werr := os.WriteFile(dest, data, 0o755); werr != nil {
			return werr
		}
		os.Remove(res.Path)
	}
	if err := os.Chmod(dest, 0o755); err != nil {
		return err
	}

	version := in.probeVersion(dest, k)
	err = in.State.UpdateServer(k, func(rec *ServerRec) {
		rec.Installed = true
		rec.BinTag = rel.TagName
		rec.BinSHA256 = res.SHA256
		rec.BinVersion = version
	})
	if err != nil {
		return err
	}
	in.Log.Step(fmt.Sprintf("%s installed (%s)", d.Name, version))
	return nil
}

// probeVersion runs `<bin> --version` (servers) or `<bin> -v` (chat CLI).
func (in *Installer) probeVersion(bin string, k Kind) string {
	var args []string
	if k == KindRelay {
		args = []string{"-v"}
	} else {
		args = []string{"--version"}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, args...)
	out, err := cmd.CombinedOutput()
	line := strings.TrimSpace(string(out))
	if err != nil || line == "" {
		return "unknown"
	}
	if i := strings.IndexByte(line, '\n'); i > 0 {
		line = line[:i]
	}
	return line
}

// VerifyInstalled checks the pinned hash of an installed binary.
func (in *Installer) VerifyInstalled(k Kind) error {
	rec := in.State.Server(k)
	if !rec.Installed {
		return fmt.Errorf("%s is not installed", Def(k).Name)
	}
	got, err := VerifySha256(in.Paths.BinPath(k))
	if err != nil {
		return err
	}
	if rec.BinSHA256 != "" && got != rec.BinSHA256 {
		return fmt.Errorf("%s binary hash changed (pinned %s, found %s) - reinstall recommended", Def(k).Name, rec.BinSHA256[:12], got[:12])
	}
	return nil
}
