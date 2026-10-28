package core

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Release is a GitHub release with the assets we care about.
type Release struct {
	TagName     string    `json:"tag_name"`
	Name        string    `json:"name"`
	Prerelease  bool      `json:"prerelease"`
	PublishedAt time.Time `json:"published_at"`
	Assets      []Asset   `json:"assets"`
}

// Asset is a single downloadable release artifact.
type Asset struct {
	Name               string `json:"name"`
	Size               int64  `json:"size"`
	BrowserDownloadURL string `json:"browser_download_url"`
}

type ghClient struct {
	token string
	http  *http.Client
}

func newGHClient(token string) *ghClient {
	return &ghClient{token: token, http: &http.Client{Timeout: 30 * time.Second}}
}

func (g *ghClient) do(ctx context.Context, url string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "sxops")
	if g.token != "" {
		req.Header.Set("Authorization", "Bearer "+g.token)
	}
	return g.http.Do(req)
}

// ListReleases returns releases newest-first, optionally including
// pre-releases. It walks pagination up to 5 pages.
func (g *ghClient) ListReleases(ctx context.Context, repo string, includePre bool) ([]Release, error) {
	var out []Release
	for page := 1; page <= 5; page++ {
		url := fmt.Sprintf("https://api.github.com/repos/%s/releases?per_page=50&page=%d", repo, page)
		resp, err := g.do(ctx, url)
		if err != nil {
			return nil, err
		}
		if resp.StatusCode == 403 || resp.StatusCode == 429 {
			resp.Body.Close()
			return nil, fmt.Errorf("GitHub API rate limit exceeded - optionally set a token in Settings")
		}
		if resp.StatusCode != 200 {
			resp.Body.Close()
			return nil, fmt.Errorf("GitHub API %s for %s", resp.Status, url)
		}
		var rels []Release
		err = json.NewDecoder(resp.Body).Decode(&rels)
		resp.Body.Close()
		if err != nil {
			return nil, err
		}
		if len(rels) == 0 {
			break
		}
		for _, r := range rels {
			if includePre || !r.Prerelease {
				out = append(out, r)
			}
		}
		if len(rels) < 50 {
			break
		}
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].PublishedAt.After(out[j].PublishedAt)
	})
	return out, nil
}

// LatestRelease returns the newest release matching the channel.
// It falls back to scraping the GitHub releases page when the API is
// rate-limited (common without an API token).
func (g *ghClient) LatestRelease(ctx context.Context, repo string, includePre bool) (*Release, error) {
	rels, err := g.ListReleases(ctx, repo, includePre)
	if err == nil && len(rels) > 0 {
		return &rels[0], nil
	}
	if err != nil && !isRateLimit(err) && !isNotFound(err) {
		return nil, err
	}
	return g.latestViaHTML(ctx, repo, includePre)
}

func isRateLimit(err error) bool {
	return err != nil && strings.Contains(err.Error(), "rate limit")
}

func isNotFound(err error) bool {
	return err != nil && strings.Contains(err.Error(), "404")
}

// latestViaHTML resolves the latest release without the REST API:
// /releases/latest (or /releases for pre-releases) redirects to the
// tag page and the expanded-assets fragment lists the download URLs.
func (g *ghClient) latestViaHTML(ctx context.Context, repo string, includePre bool) (*Release, error) {
	page := "latest"
	if includePre {
		page = ""
	}
	url := fmt.Sprintf("https://github.com/%s/releases/%s", repo, page)
	resp, err := g.do(ctx, url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return nil, err
	}
	finalURL := resp.Request.URL.String()
	m := regexp.MustCompile(`/releases/tag/([^/"]+)`).FindStringSubmatch(finalURL)
	if m == nil {
		m = regexp.MustCompile(`/releases/tag/([^/"]+)`).FindStringSubmatch(string(body))
	}
	if m == nil {
		return nil, fmt.Errorf("GitHub API rate limit exceeded and HTML fallback failed - optionally set a token in Settings")
	}
	tag := m[1]
	if includePre && tag == "" {
		return nil, fmt.Errorf("no releases found")
	}
	// fetch asset list
	au := fmt.Sprintf("https://github.com/%s/releases/expanded_assets/%s", repo, tag)
	aresp, err := g.do(ctx, au)
	if err != nil {
		return nil, err
	}
	defer aresp.Body.Close()
	abody, err := io.ReadAll(io.LimitReader(aresp.Body, 4<<20))
	if err != nil {
		return nil, err
	}
	rel := &Release{TagName: tag, Name: tag, Prerelease: strings.Contains(tag, "beta") || strings.Contains(tag, "rc") || strings.Contains(tag, "alpha")}
	am := regexp.MustCompile(`href="([^"]*/releases/download/[^"]+)"`)
	seen := map[string]bool{}
	for _, mm := range am.FindAllStringSubmatch(string(abody), -1) {
		u := mm[1]
		if !strings.HasPrefix(u, "https://") {
			u = "https://github.com" + u
		}
		name := u[strings.LastIndexByte(u, '/')+1:]
		if !seen[name] {
			seen[name] = true
			rel.Assets = append(rel.Assets, Asset{Name: name, BrowserDownloadURL: u})
		}
	}
	return rel, nil
}

// FindAsset locates the asset in a release by exact name.
func FindAsset(r *Release, name string) (*Asset, error) {
	for i := range r.Assets {
		if r.Assets[i].Name == name {
			return &r.Assets[i], nil
		}
	}
	return nil, fmt.Errorf("asset %q not found in release %s", name, r.TagName)
}

// DownloadResult carries the outcome of a verified download.
type DownloadResult struct {
	Path   string
	SHA256 string
	Size   int64
}

// Download fetches url into destDir/name, computing the SHA-256 while
// streaming. progress (may be nil) receives byte counts.
func Download(ctx context.Context, url, destDir, name string, token string, progress func(int64, int64)) (*DownloadResult, error) {
	if err := os.MkdirAll(destDir, 0o700); err != nil {
		return nil, err
	}
	tmp := filepath.Join(destDir, name+".part")
	g := newGHClient(token)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "sxops")
	resp, err := g.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("download %s: HTTP %s", url, resp.Status)
	}
	f, err := os.Create(tmp)
	if err != nil {
		return nil, err
	}
	h := sha256.New()
	var total int64
	if resp.ContentLength > 0 {
		total = resp.ContentLength
	}
	buf := make([]byte, 256*1024)
	var written int64
	for {
		n, rerr := resp.Body.Read(buf)
		if n > 0 {
			if _, werr := f.Write(buf[:n]); werr != nil {
				f.Close()
				os.Remove(tmp)
				return nil, werr
			}
			h.Write(buf[:n])
			written += int64(n)
			if progress != nil {
				progress(written, total)
			}
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			f.Close()
			os.Remove(tmp)
			return nil, rerr
		}
	}
	if err := f.Close(); err != nil {
		return nil, err
	}
	final := filepath.Join(destDir, name)
	if err := os.Rename(tmp, final); err != nil {
		return nil, err
	}
	return &DownloadResult{
		Path:   final,
		SHA256: hex.EncodeToString(h.Sum(nil)),
		Size:   written,
	}, nil
}

// VerifySha256 computes the SHA-256 of a file (upgrade integrity check).
func VerifySha256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// SumsAssetName is the conventional signed sums asset published with
// server releases (present in some versions; verified when available).
func SumsAssetName() string { return "_sha256sums" }

// ParseSha256sums parses "<hex>  <file>" lines.
func ParseSha256sums(data string) map[string]string {
	out := map[string]string{}
	for _, line := range strings.Split(data, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 {
			out[filepath.Base(fields[1])] = strings.ToLower(fields[0])
		}
	}
	return out
}
