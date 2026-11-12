package registry

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

const manifestAccept = "application/vnd.oci.image.index.v1+json, application/vnd.docker.distribution.manifest.list.v2+json, application/vnd.oci.image.manifest.v1+json, application/vnd.docker.distribution.manifest.v2+json"

var (
	repositoryPattern = regexp.MustCompile(`^[a-z0-9]+(?:(?:[._]|__|-+)[a-z0-9]+)*(?:/[a-z0-9]+(?:(?:[._]|__|-+)[a-z0-9]+)*)*$`)
	digestPattern     = regexp.MustCompile(`^sha256:[a-f0-9]{64}$`)
)

func ValidRepository(name string) bool {
	return len(name) <= 255 && repositoryPattern.MatchString(name)
}
func ValidDigest(digest string) bool { return digestPattern.MatchString(digest) }

type Client struct {
	URL  *url.URL
	http *http.Client
}

func New(rawURL string) (*Client, error) {
	u, err := url.Parse(rawURL)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return nil, errors.New("REGISTRY_URL must be an http(s) origin without credentials, path, or query")
	}
	u.Path = ""
	return &Client{URL: u, http: &http.Client{Timeout: 30 * time.Second, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}

type UpstreamError struct{ Status int }

func (e *UpstreamError) Error() string {
	if e.Status == http.StatusNotFound {
		return "Image or repository no longer exists. Refresh and try again."
	}
	if e.Status == http.StatusMethodNotAllowed {
		return "Deletion is disabled on the registry."
	}
	return fmt.Sprintf("Registry returned HTTP %d", e.Status)
}

func (c *Client) request(ctx context.Context, method, path string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, c.URL.String()+path, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", manifestAccept)
	res, err := c.http.Do(req)
	if err != nil {
		return nil, errors.New("Cannot reach the registry. Check that the registry container is running.")
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		res.Body.Close()
		return nil, &UpstreamError{Status: res.StatusCode}
	}
	return res, nil
}

func decode(res *http.Response, target any) error {
	defer res.Body.Close()
	if err := json.NewDecoder(io.LimitReader(res.Body, 16<<20)).Decode(target); err != nil {
		return errors.New("Registry returned invalid or oversized JSON")
	}
	return nil
}

func (c *Client) Ping(ctx context.Context) error {
	res, err := c.request(ctx, http.MethodGet, "/v2/")
	if err == nil {
		res.Body.Close()
	}
	return err
}

// Construct each page locally; never follow an upstream Link to another host.
func (c *Client) list(ctx context.Context, path, field string) ([]string, error) {
	items := []string{}
	last := ""
	for page := 0; page < 1000; page++ {
		q := url.Values{"n": {"1000"}}
		if last != "" {
			q.Set("last", last)
		}
		res, err := c.request(ctx, http.MethodGet, path+"?"+q.Encode())
		if err != nil {
			return nil, err
		}
		hasNext := res.Header.Get("Link") != ""
		var data struct {
			Repositories []string `json:"repositories"`
			Tags         []string `json:"tags"`
		}
		if err := decode(res, &data); err != nil {
			return nil, err
		}
		batch := data.Repositories
		if field == "tags" {
			batch = data.Tags
		}
		items = append(items, batch...)
		if len(batch) == 0 || (!hasNext && len(batch) < 1000) {
			sort.Strings(items)
			return items, nil
		}
		next := batch[len(batch)-1]
		if next == last {
			return nil, errors.New("Registry pagination did not advance")
		}
		last = next
	}
	return nil, errors.New("Registry listing exceeded pagination limit")
}

func (c *Client) Repositories(ctx context.Context) ([]string, error) {
	return c.list(ctx, "/v2/_catalog", "repositories")
}

type descriptor struct {
	Digest   string `json:"digest"`
	Size     int64  `json:"size"`
	Platform struct {
		OS           string `json:"os"`
		Architecture string `json:"architecture"`
		Variant      string `json:"variant"`
	} `json:"platform"`
}

type manifest struct {
	MediaType string       `json:"mediaType"`
	Config    descriptor   `json:"config"`
	Layers    []descriptor `json:"layers"`
	Manifests []descriptor `json:"manifests"`
}

type Tag struct {
	Name      string   `json:"name"`
	Digest    string   `json:"digest"`
	Size      int64    `json:"size"`
	Created   string   `json:"created,omitempty"`
	Platforms []string `json:"platforms"`
	MediaType string   `json:"mediaType,omitempty"`
	Error     string   `json:"error,omitempty"`
}

func (c *Client) readManifest(ctx context.Context, repo, ref string) (manifest, string, error) {
	res, err := c.request(ctx, http.MethodGet, "/v2/"+repo+"/manifests/"+url.PathEscape(ref))
	if err != nil {
		return manifest{}, "", err
	}
	digest := res.Header.Get("Docker-Content-Digest")
	var m manifest
	err = decode(res, &m)
	return m, digest, err
}

func (c *Client) tag(ctx context.Context, repo, name string) Tag {
	t := Tag{Name: name, Platforms: []string{}}
	m, digest, err := c.readManifest(ctx, repo, name)
	if err != nil {
		t.Error = err.Error()
		return t
	}
	t.Digest, t.MediaType = digest, m.MediaType
	if len(m.Manifests) > 0 {
		// Count each compressed layer once, including shared layers across platforms.
		layers := map[string]int64{}
		platforms := map[string]bool{}
		for _, child := range m.Manifests {
			if !ValidDigest(child.Digest) {
				t.Error = "Image index contains an unsupported digest"
				return t
			}
			cm, _, err := c.readManifest(ctx, repo, child.Digest)
			if err != nil {
				t.Error = err.Error()
				return t
			}
			for _, layer := range cm.Layers {
				layers[layer.Digest] = layer.Size
			}
			p := child.Platform.OS + "/" + child.Platform.Architecture
			if child.Platform.Variant != "" {
				p += "/" + child.Platform.Variant
			}
			if p != "/" && !strings.HasPrefix(p, "unknown/") {
				platforms[p] = true
			}
		}
		for _, size := range layers {
			t.Size += size
		}
		for p := range platforms {
			t.Platforms = append(t.Platforms, p)
		}
		sort.Strings(t.Platforms)
		return t
	}
	for _, l := range m.Layers {
		t.Size += l.Size
	}
	if m.Config.Digest != "" {
		if !ValidDigest(m.Config.Digest) {
			t.Error = "Image configuration contains an unsupported digest"
			return t
		}
		res, err := c.request(ctx, http.MethodGet, "/v2/"+repo+"/blobs/"+m.Config.Digest)
		if err != nil {
			t.Error = err.Error()
			return t
		}
		var config struct {
			Created      string `json:"created"`
			OS           string `json:"os"`
			Architecture string `json:"architecture"`
			Variant      string `json:"variant"`
		}
		if err := decode(res, &config); err != nil {
			t.Error = err.Error()
			return t
		}
		t.Created = config.Created
		if config.OS != "" && config.Architecture != "" {
			p := config.OS + "/" + config.Architecture
			if config.Variant != "" {
				p += "/" + config.Variant
			}
			t.Platforms = append(t.Platforms, p)
		}
	}
	return t
}

func (c *Client) Tags(ctx context.Context, repo string) ([]Tag, error) {
	if !ValidRepository(repo) {
		return nil, errors.New("Invalid repository name")
	}
	names, err := c.list(ctx, "/v2/"+repo+"/tags/list", "tags")
	if err != nil {
		return nil, err
	}
	tags := make([]Tag, len(names))
	jobs := make(chan int)
	var wg sync.WaitGroup
	for i := 0; i < 6; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for index := range jobs {
				tags[index] = c.tag(ctx, repo, names[index])
			}
		}()
	}
	for i := range names {
		select {
		case jobs <- i:
		case <-ctx.Done():
			close(jobs)
			wg.Wait()
			return nil, ctx.Err()
		}
	}
	close(jobs)
	wg.Wait()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return tags, nil
}

func (c *Client) Delete(ctx context.Context, repo, digest string) error {
	if !ValidRepository(repo) || !ValidDigest(digest) {
		return errors.New("Invalid repository or manifest digest")
	}
	res, err := c.request(ctx, http.MethodDelete, "/v2/"+repo+"/manifests/"+digest)
	if err == nil {
		res.Body.Close()
	}
	return err
}
