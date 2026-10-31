package update

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const (
	exeInZip       = "barncat-sparks.exe"
	checksumsAsset = "checksums.txt"

	maxChecksums = 1 << 20
	maxDownload  = 256 << 20

	renameTries = 5
	renameDelay = 200 * time.Millisecond // antivirus may hold a new exe briefly
)

// OldPath is where the previous version is kept for rollback. Its extension
// stops it being double-clicked by accident.
func OldPath(exePath string) string { return exePath + ".old" }

func (r Release) asset(name string) (Asset, bool) {
	for _, a := range r.Assets {
		if a.Name == name {
			return a, true
		}
	}
	return Asset{}, false
}

// Stage downloads the release's Windows zip beside exePath, verifies it
// against checksums.txt and extracts the new exe to exePath+".new", which it
// returns. It leaves exePath alone, and leaves nothing behind on failure.
func (c *Client) Stage(ctx context.Context, rel Release, exePath string) (string, error) {
	zipName := fmt.Sprintf("barncat-sparks_%s_windows_amd64.zip", rel.Version)
	zipAsset, ok := rel.asset(zipName)
	if !ok {
		return "", fmt.Errorf("release v%s has no Windows download", rel.Version)
	}
	sumsAsset, ok := rel.asset(checksumsAsset)
	if !ok {
		return "", fmt.Errorf("release v%s has no %s", rel.Version, checksumsAsset)
	}

	sums, err := c.fetchText(ctx, sumsAsset.URL)
	if err != nil {
		return "", err
	}
	want, ok := findChecksum(sums, zipName)
	if !ok {
		return "", fmt.Errorf("no checksum listed for %s", zipName)
	}

	tmp, err := os.CreateTemp(filepath.Dir(exePath), ".sparks-update-*.zip")
	if err != nil {
		return "", fmt.Errorf("can't write to the Sparks folder: %w", err)
	}
	defer os.Remove(tmp.Name())
	defer tmp.Close()

	got, err := c.download(ctx, zipAsset.URL, tmp)
	if err != nil {
		return "", err
	}
	if got != want {
		return "", fmt.Errorf("checksum mismatch for %s", zipName)
	}

	staged := exePath + ".new"
	if err := extractExe(tmp, staged); err != nil {
		os.Remove(staged)
		return "", err
	}
	return staged, nil
}

func (c *Client) fetchText(ctx context.Context, url string) (string, error) {
	resp, err := c.get(ctx, url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, maxChecksums))
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// download writes url to w and returns the SHA-256 of what it wrote.
func (c *Client) download(ctx context.Context, url string, w io.Writer) (string, error) {
	resp, err := c.get(ctx, url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	h := sha256.New()
	if _, err := io.Copy(io.MultiWriter(w, h), io.LimitReader(resp.Body, maxDownload)); err != nil {
		return "", fmt.Errorf("download failed: %w", err)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// findChecksum returns name's hash from sha256sum-style text, in lower case.
func findChecksum(text, name string) (string, bool) {
	for line := range strings.Lines(text) {
		f := strings.Fields(line)
		if len(f) == 2 && strings.TrimPrefix(f[1], "*") == name {
			return strings.ToLower(f[0]), true
		}
	}
	return "", false
}

func extractExe(zipFile *os.File, dst string) error {
	info, err := zipFile.Stat()
	if err != nil {
		return err
	}
	zr, err := zip.NewReader(zipFile, info.Size())
	if err != nil {
		return fmt.Errorf("reading download: %w", err)
	}
	for _, f := range zr.File {
		if f.Name == exeInZip {
			return writeExe(f, dst)
		}
	}
	return fmt.Errorf("download has no %s", exeInZip)
}

func writeExe(f *zip.File, dst string) error {
	src, err := f.Open()
	if err != nil {
		return err
	}
	defer src.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		return fmt.Errorf("can't write to the Sparks folder: %w", err)
	}
	// Reading to the end also checks the zip's CRC.
	if _, err := io.Copy(out, io.LimitReader(src, maxDownload)); err != nil {
		out.Close()
		return fmt.Errorf("unpacking %s: %w", exeInZip, err)
	}
	if err := out.Close(); err != nil {
		return err
	}
	return checkIsProgram(dst)
}

func checkIsProgram(path string) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	magic := make([]byte, 2)
	if _, err := io.ReadFull(file, magic); err != nil || string(magic) != "MZ" {
		return fmt.Errorf("%s in the download is not a Windows program", exeInZip)
	}
	return nil
}

// rename retries a failure other than a missing file, which won't clear.
func rename(from, to string) error {
	var err error
	for range renameTries {
		if err = os.Rename(from, to); err == nil || errors.Is(err, fs.ErrNotExist) {
			return err
		}
		time.Sleep(renameDelay)
	}
	return err
}

// Swap moves exePath aside to OldPath and staged into its place. Windows can't
// overwrite a running exe, but it can rename one. On failure exePath is back
// as it was and no rollback file is left. An earlier rollback file is replaced.
func Swap(exePath, staged string) error {
	old := OldPath(exePath)
	if err := os.Remove(old); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("can't replace %s; close any other copy of Sparks: %w", filepath.Base(old), err)
	}
	if err := rename(exePath, old); err != nil {
		return fmt.Errorf("can't move the running program aside: %w", err)
	}
	if err := rename(staged, exePath); err != nil {
		if backErr := rename(old, exePath); backErr != nil {
			return fmt.Errorf("can't install the update (%w), and your previous version is left as %s: %v", err, old, backErr)
		}
		return fmt.Errorf("can't install the update: %w", err)
	}
	return nil
}

// RollBack puts the previous version back after a Swap.
func RollBack(exePath string) error {
	if err := os.Remove(exePath); err != nil {
		return err
	}
	return rename(OldPath(exePath), exePath)
}

// restartArgs returns the new copy's arguments: this copy's, with -wait-pid
// naming this copy.
func restartArgs(args []string, pid int) []string {
	out := make([]string, 0, len(args)+1)
	for _, a := range args {
		if !strings.HasPrefix(strings.TrimLeft(a, "-"), "wait-pid=") {
			out = append(out, a)
		}
	}
	return append(out, fmt.Sprintf("-wait-pid=%d", pid))
}

// StartNewCopy launches exePath, which waits for process pid to exit before it
// takes the single-instance lock.
func StartNewCopy(exePath string, args []string, pid int) error {
	cmd := exec.Command(exePath, restartArgs(args, pid)...)
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}
