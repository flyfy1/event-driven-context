// Package updater checks the selected Event-driven Context server for CLI
// release policy and performs explicit, checksum-verified self-updates.
package updater

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"event-driven-context/internal/buildinfo"
)

const (
	cacheVersion      = 1
	maxPolicyBytes    = 32 << 10
	maxReleaseBytes   = 1 << 20
	maxChecksumsBytes = 1 << 20
	maxBinaryBytes    = 100 << 20
)

type Status struct {
	CLIVersion       string `json:"cli_version"`
	CLICommit        string `json:"cli_commit"`
	LatestVersion    string `json:"latest_version,omitempty"`
	MinimumVersion   string `json:"minimum_version,omitempty"`
	UpdateAvailable  bool   `json:"update_available"`
	Compatible       bool   `json:"compatible"`
	PolicyAvailable  bool   `json:"policy_available"`
	ReleasePageURL   string `json:"release_page_url,omitempty"`
	CheckedAt        string `json:"checked_at,omitempty"`
	UpdateCheckError string `json:"update_check_error,omitempty"`
}

type Snapshot struct {
	Status Status              `json:"status"`
	Policy buildinfo.CLIPolicy `json:"policy"`
}

type Result struct {
	PreviousVersion  string `json:"previous_version"`
	InstalledVersion string `json:"installed_version"`
	Executable       string `json:"executable"`
	Backup           string `json:"backup"`
	Updated          bool   `json:"updated"`
}

type Client struct {
	HTTP *http.Client
	Now  func() time.Time
	OS   string
	Arch string
}

func New() *Client {
	return &Client{
		HTTP: &http.Client{Timeout: 5 * time.Second, CheckRedirect: secureRedirect},
		Now:  time.Now,
		OS:   runtime.GOOS,
		Arch: runtime.GOARCH,
	}
}

func secureRedirect(req *http.Request, _ []*http.Request) error {
	if err := validateRemoteURL(req.URL.String()); err != nil {
		return err
	}
	return nil
}

func (c *Client) Check(ctx context.Context, server string) (Snapshot, error) {
	status := Status{
		CLIVersion: buildinfo.Version,
		CLICommit:  buildinfo.Commit,
		Compatible: true,
		CheckedAt:  c.now().UTC().Format(time.RFC3339Nano),
	}
	endpoint := strings.TrimRight(server, "/") + "/.well-known/edc-cli"
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return Snapshot{Status: status}, err
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("User-Agent", "edc/"+buildinfo.Version)
	response, err := c.httpClient().Do(request)
	if err != nil {
		return Snapshot{Status: status}, err
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusNotFound {
		return Snapshot{Status: status}, nil
	}
	if response.StatusCode != http.StatusOK {
		return Snapshot{Status: status}, fmt.Errorf("CLI update policy returned HTTP %d", response.StatusCode)
	}
	var policy buildinfo.CLIPolicy
	if err = decodeLimitedJSON(response.Body, maxPolicyBytes, &policy); err != nil {
		return Snapshot{Status: status}, fmt.Errorf("read CLI update policy: %w", err)
	}
	if !validSemver(policy.LatestVersion) || !validSemver(policy.MinimumVersion) {
		return Snapshot{Status: status}, fmt.Errorf("server returned invalid CLI versions")
	}
	if compareSemver(policy.MinimumVersion, policy.LatestVersion) > 0 {
		return Snapshot{Status: status}, fmt.Errorf("server minimum CLI version exceeds latest version")
	}
	if err = validatePolicyURL(policy.ReleaseAPIURL, buildinfo.ReleaseAPIURL); err != nil {
		return Snapshot{Status: status}, fmt.Errorf("invalid CLI release API URL: %w", err)
	}
	if err = validatePolicyURL(policy.ReleasePageURL, buildinfo.ReleasePageURL); err != nil {
		return Snapshot{Status: status}, fmt.Errorf("invalid CLI release page URL: %w", err)
	}
	status.LatestVersion = policy.LatestVersion
	status.MinimumVersion = policy.MinimumVersion
	status.UpdateAvailable = compareSemver(buildinfo.Version, policy.LatestVersion) < 0
	status.Compatible = compareSemver(buildinfo.Version, policy.MinimumVersion) >= 0
	status.PolicyAvailable = true
	status.ReleasePageURL = policy.ReleasePageURL
	return Snapshot{Status: status, Policy: policy}, nil
}

func (c *Client) CheckCached(ctx context.Context, server, cachePath string, ttl time.Duration) (Snapshot, error) {
	now := c.now()
	cache, err := readCache(cachePath)
	if err == nil && cache.Server == strings.TrimRight(server, "/") {
		checked, parseErr := time.Parse(time.RFC3339Nano, cache.Snapshot.Status.CheckedAt)
		if parseErr == nil && !now.Before(checked) && now.Sub(checked) < ttl {
			return cache.Snapshot, nil
		}
	}
	snapshot, err := c.Check(ctx, server)
	if err != nil {
		return snapshot, err
	}
	notifiedAt := ""
	if cache.Snapshot.Status.LatestVersion == snapshot.Status.LatestVersion {
		notifiedAt = cache.NotifiedAt
	}
	if err = writeCache(cachePath, cacheFile{Version: cacheVersion, Server: strings.TrimRight(server, "/"), Snapshot: snapshot, NotifiedAt: notifiedAt}); err != nil {
		return snapshot, err
	}
	return snapshot, nil
}

func (c *Client) ShouldNotify(ctx context.Context, server, cachePath string, ttl time.Duration) (Status, bool, error) {
	snapshot, err := c.CheckCached(ctx, server, cachePath, ttl)
	if err != nil || (!snapshot.Status.UpdateAvailable && snapshot.Status.Compatible) {
		return snapshot.Status, false, err
	}
	cache, err := readCache(cachePath)
	if err != nil {
		return snapshot.Status, false, err
	}
	if cache.NotifiedAt != "" {
		notified, parseErr := time.Parse(time.RFC3339Nano, cache.NotifiedAt)
		if parseErr == nil && !c.now().Before(notified) && c.now().Sub(notified) < ttl {
			return snapshot.Status, false, nil
		}
	}
	cache.NotifiedAt = c.now().UTC().Format(time.RFC3339Nano)
	if err = writeCache(cachePath, cache); err != nil {
		return snapshot.Status, false, err
	}
	return snapshot.Status, true, nil
}

func (c *Client) Update(ctx context.Context, server, executable string) (Result, Status, error) {
	snapshot, err := c.Check(ctx, server)
	if err != nil {
		return Result{}, snapshot.Status, err
	}
	if !snapshot.Status.PolicyAvailable {
		return Result{}, snapshot.Status, fmt.Errorf("server does not publish CLI update information")
	}
	if !snapshot.Status.UpdateAvailable {
		return Result{PreviousVersion: buildinfo.Version, InstalledVersion: buildinfo.Version, Executable: executable}, snapshot.Status, nil
	}
	release, err := c.fetchRelease(ctx, snapshot.Policy.ReleaseAPIURL)
	if err != nil {
		return Result{}, snapshot.Status, err
	}
	if release.Draft || release.Prerelease || release.TagName != snapshot.Policy.LatestVersion {
		return Result{}, snapshot.Status, fmt.Errorf("published release does not match server recommendation %s", snapshot.Policy.LatestVersion)
	}
	assetName := "edc-" + c.operatingSystem() + "-" + c.architecture()
	binaryAsset, ok := findAsset(release.Assets, assetName)
	if !ok {
		return Result{}, snapshot.Status, fmt.Errorf("release %s has no asset for %s/%s", release.TagName, c.operatingSystem(), c.architecture())
	}
	expected, err := c.assetDigest(ctx, release.Assets, binaryAsset)
	if err != nil {
		return Result{}, snapshot.Status, err
	}
	data, err := c.download(ctx, binaryAsset.BrowserDownloadURL, maxBinaryBytes)
	if err != nil {
		return Result{}, snapshot.Status, fmt.Errorf("download %s: %w", assetName, err)
	}
	if binaryAsset.Size > 0 && int64(len(data)) != binaryAsset.Size {
		return Result{}, snapshot.Status, fmt.Errorf("downloaded CLI size does not match release metadata")
	}
	sum := sha256.Sum256(data)
	if !strings.EqualFold(hex.EncodeToString(sum[:]), expected) {
		return Result{}, snapshot.Status, fmt.Errorf("downloaded CLI sha256 does not match release metadata")
	}
	result, err := install(executable, data, buildinfo.Version, release.TagName, c.now())
	return result, snapshot.Status, err
}

type releaseAsset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
	Digest             string `json:"digest"`
	Size               int64  `json:"size"`
}

type releaseResponse struct {
	TagName    string         `json:"tag_name"`
	HTMLURL    string         `json:"html_url"`
	Draft      bool           `json:"draft"`
	Prerelease bool           `json:"prerelease"`
	Assets     []releaseAsset `json:"assets"`
}

func (c *Client) fetchRelease(ctx context.Context, endpoint string) (releaseResponse, error) {
	data, err := c.download(ctx, endpoint, maxReleaseBytes)
	if err != nil {
		return releaseResponse{}, fmt.Errorf("read CLI release: %w", err)
	}
	var out releaseResponse
	if err = decodeLimitedJSON(bytes.NewReader(data), maxReleaseBytes, &out); err != nil {
		return out, fmt.Errorf("decode CLI release: %w", err)
	}
	if !validSemver(out.TagName) {
		return out, fmt.Errorf("CLI release has invalid version")
	}
	return out, nil
}

func (c *Client) assetDigest(ctx context.Context, assets []releaseAsset, binary releaseAsset) (string, error) {
	if strings.HasPrefix(binary.Digest, "sha256:") {
		digest := strings.TrimPrefix(binary.Digest, "sha256:")
		if validSHA256(digest) {
			return strings.ToLower(digest), nil
		}
	}
	checksums, ok := findAsset(assets, "checksums.txt")
	if !ok {
		return "", fmt.Errorf("release is missing a sha256 digest and checksums.txt")
	}
	data, err := c.download(ctx, checksums.BrowserDownloadURL, maxChecksumsBytes)
	if err != nil {
		return "", fmt.Errorf("download checksums.txt: %w", err)
	}
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && strings.TrimPrefix(fields[1], "*") == binary.Name && validSHA256(fields[0]) {
			return strings.ToLower(fields[0]), nil
		}
	}
	return "", fmt.Errorf("checksums.txt has no valid entry for %s", binary.Name)
}

func (c *Client) download(ctx context.Context, rawURL string, limit int64) ([]byte, error) {
	if err := validateRemoteURL(rawURL); err != nil {
		return nil, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Accept", "application/octet-stream")
	request.Header.Set("User-Agent", "edc/"+buildinfo.Version)
	response, err := c.httpClient().Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", response.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("response exceeds %d bytes", limit)
	}
	return data, nil
}

func install(target string, data []byte, previous, latest string, now time.Time) (Result, error) {
	if runtime.GOOS == "windows" {
		return Result{}, fmt.Errorf("self-update is not supported on Windows")
	}
	target, err := filepath.Abs(target)
	if err != nil {
		return Result{}, err
	}
	info, err := os.Lstat(target)
	if err != nil {
		return Result{}, fmt.Errorf("inspect current executable: %w", err)
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return Result{}, fmt.Errorf("current executable must be a regular file, not a symlink")
	}
	dir := filepath.Dir(target)
	temp, err := os.CreateTemp(dir, ".edc-update-*")
	if err != nil {
		return Result{}, fmt.Errorf("create update file: %w", err)
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)
	mode := info.Mode().Perm()
	if mode&0111 == 0 {
		mode = 0755
	}
	if err = temp.Chmod(mode); err == nil {
		_, err = temp.Write(data)
	}
	if err == nil {
		err = temp.Sync()
	}
	if closeErr := temp.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return Result{}, fmt.Errorf("write update file: %w", err)
	}
	backup := target + ".backup-" + safeVersion(previous)
	if _, statErr := os.Lstat(backup); statErr == nil {
		backup += "-" + now.UTC().Format("20060102T150405Z")
	} else if !errors.Is(statErr, os.ErrNotExist) {
		return Result{}, fmt.Errorf("inspect update backup: %w", statErr)
	}
	if err = os.Link(target, backup); err != nil {
		return Result{}, fmt.Errorf("back up current executable: %w", err)
	}
	if err = os.Rename(tempPath, target); err != nil {
		return Result{}, fmt.Errorf("replace current executable; backup retained at %s: %w", backup, err)
	}
	if err = syncDirectory(dir); err != nil {
		return Result{}, fmt.Errorf("sync updated executable directory; backup retained at %s: %w", backup, err)
	}
	return Result{PreviousVersion: previous, InstalledVersion: latest, Executable: target, Backup: backup, Updated: true}, nil
}

type cacheFile struct {
	Version    int      `json:"version"`
	Server     string   `json:"server"`
	Snapshot   Snapshot `json:"snapshot"`
	NotifiedAt string   `json:"notified_at,omitempty"`
}

func readCache(path string) (cacheFile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return cacheFile{}, err
	}
	var out cacheFile
	if err = json.Unmarshal(data, &out); err != nil || out.Version != cacheVersion {
		return cacheFile{}, fmt.Errorf("invalid CLI update cache")
	}
	return out, nil
}

func writeCache(path string, value cacheFile) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	dir := filepath.Dir(path)
	if err = os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	if info, statErr := os.Lstat(path); statErr == nil && info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("refuse to replace update cache symlink")
	} else if statErr != nil && !errors.Is(statErr, os.ErrNotExist) {
		return statErr
	}
	temp, err := os.CreateTemp(dir, ".update-check-*")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)
	if err = temp.Chmod(0600); err == nil {
		_, err = temp.Write(data)
	}
	if err == nil {
		err = temp.Sync()
	}
	if closeErr := temp.Close(); err == nil {
		err = closeErr
	}
	if err == nil {
		err = os.Rename(tempPath, path)
	}
	if err != nil {
		return err
	}
	return syncDirectory(dir)
}

func decodeLimitedJSON(reader io.Reader, limit int64, out any) error {
	data, err := io.ReadAll(io.LimitReader(reader, limit+1))
	if err != nil {
		return err
	}
	if int64(len(data)) > limit {
		return fmt.Errorf("response exceeds %d bytes", limit)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	if err = decoder.Decode(out); err != nil {
		return err
	}
	var extra any
	if err = decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return fmt.Errorf("response has trailing JSON")
	}
	return nil
}

func (c *Client) httpClient() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return New().HTTP
}

func (c *Client) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

func (c *Client) operatingSystem() string {
	if c.OS != "" {
		return c.OS
	}
	return runtime.GOOS
}

func (c *Client) architecture() string {
	if c.Arch != "" {
		return c.Arch
	}
	return runtime.GOARCH
}

func findAsset(assets []releaseAsset, name string) (releaseAsset, bool) {
	for _, asset := range assets {
		if asset.Name == name {
			return asset, true
		}
	}
	return releaseAsset{}, false
}

func validateRemoteURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.User != nil || u.Fragment != "" {
		return fmt.Errorf("URL must be an absolute HTTPS URL")
	}
	loopback := isLoopbackURL(u)
	if u.Scheme != "https" && !(u.Scheme == "http" && loopback) {
		return fmt.Errorf("URL must use HTTPS")
	}
	return nil
}

func validatePolicyURL(raw, official string) error {
	if err := validateRemoteURL(raw); err != nil {
		return err
	}
	u, _ := url.Parse(raw)
	if !isLoopbackURL(u) && raw != official {
		return fmt.Errorf("URL must be the official Event-driven Context release endpoint")
	}
	return nil
}

func isLoopbackURL(u *url.URL) bool {
	if u.Hostname() == "localhost" {
		return true
	}
	ip := net.ParseIP(u.Hostname())
	return ip != nil && ip.IsLoopback()
}

func validSHA256(value string) bool {
	if len(value) != 64 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

type semver struct {
	major, minor, patch uint64
	pre                 []string
}

func validSemver(value string) bool {
	_, ok := parseSemver(value)
	return ok
}

func parseSemver(value string) (semver, bool) {
	value = strings.TrimPrefix(strings.TrimSpace(value), "v")
	value = strings.SplitN(value, "+", 2)[0]
	parts := strings.SplitN(value, "-", 2)
	core := strings.Split(parts[0], ".")
	if len(core) != 3 {
		return semver{}, false
	}
	values := make([]uint64, 3)
	for i, item := range core {
		if item == "" || (len(item) > 1 && item[0] == '0') {
			return semver{}, false
		}
		n, err := strconv.ParseUint(item, 10, 64)
		if err != nil {
			return semver{}, false
		}
		values[i] = n
	}
	out := semver{major: values[0], minor: values[1], patch: values[2]}
	if len(parts) == 2 {
		if parts[1] == "" {
			return semver{}, false
		}
		out.pre = strings.Split(parts[1], ".")
		for _, identifier := range out.pre {
			if identifier == "" {
				return semver{}, false
			}
			for _, r := range identifier {
				if !(r >= '0' && r <= '9') && !(r >= 'A' && r <= 'Z') && !(r >= 'a' && r <= 'z') && r != '-' {
					return semver{}, false
				}
			}
		}
	}
	return out, true
}

func compareSemver(a, b string) int {
	left, leftOK := parseSemver(a)
	right, rightOK := parseSemver(b)
	if !leftOK || !rightOK {
		return strings.Compare(a, b)
	}
	for _, pair := range [][2]uint64{{left.major, right.major}, {left.minor, right.minor}, {left.patch, right.patch}} {
		if pair[0] < pair[1] {
			return -1
		}
		if pair[0] > pair[1] {
			return 1
		}
	}
	if len(left.pre) == 0 && len(right.pre) == 0 {
		return 0
	}
	if len(left.pre) == 0 {
		return 1
	}
	if len(right.pre) == 0 {
		return -1
	}
	for i := 0; i < len(left.pre) && i < len(right.pre); i++ {
		ln, le := strconv.ParseUint(left.pre[i], 10, 64)
		rn, re := strconv.ParseUint(right.pre[i], 10, 64)
		switch {
		case le == nil && re == nil && ln != rn:
			if ln < rn {
				return -1
			}
			return 1
		case le == nil && re != nil:
			return -1
		case le != nil && re == nil:
			return 1
		case left.pre[i] != right.pre[i]:
			return strings.Compare(left.pre[i], right.pre[i])
		}
	}
	if len(left.pre) < len(right.pre) {
		return -1
	}
	if len(left.pre) > len(right.pre) {
		return 1
	}
	return 0
}

func safeVersion(value string) string {
	value = strings.TrimPrefix(value, "v")
	var out strings.Builder
	for _, r := range value {
		if (r >= '0' && r <= '9') || (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') || r == '.' || r == '-' {
			out.WriteRune(r)
		}
	}
	if out.Len() == 0 {
		return "unknown"
	}
	return out.String()
}

func syncDirectory(path string) error {
	dir, err := os.Open(path)
	if err != nil {
		return err
	}
	err = dir.Sync()
	closeErr := dir.Close()
	if err != nil {
		return err
	}
	return closeErr
}
