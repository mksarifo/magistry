package registry

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

var testDigest = "sha256:" + strings.Repeat("a", 64)

func testClient(t *testing.T, handler http.HandlerFunc) *Client {
	t.Helper()
	s := httptest.NewServer(handler)
	t.Cleanup(s.Close)
	c, err := New(s.URL)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestCatalogPagination(t *testing.T) {
	var pages int
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		pages++
		if r.URL.Path != "/v2/_catalog" || r.URL.Query().Get("n") != "1000" {
			t.Errorf("unexpected URL %s", r.URL)
		}
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Query().Get("last") == "" {
			w.Header().Set("Link", `</v2/_catalog?last=team%2Fa&n=1>; rel="next"`)
			fmt.Fprint(w, `{"repositories":["team/a"]}`)
		} else {
			if r.URL.Query().Get("last") != "team/a" {
				t.Errorf("wrong cursor: %s", r.URL)
			}
			fmt.Fprint(w, `{"repositories":["team/b"]}`)
		}
	})
	repos, err := c.Repositories(context.Background())
	if err != nil || strings.Join(repos, ",") != "team/a,team/b" || pages != 2 {
		t.Fatalf("repos=%v pages=%d err=%v", repos, pages, err)
	}
}

func TestTagMetadataAndPartialFailure(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v2/team/app/tags/list":
			fmt.Fprint(w, `{"name":"team/app","tags":["latest","broken"]}`)
		case "/v2/team/app/manifests/latest":
			if !strings.Contains(r.Header.Get("Accept"), "application/vnd.oci.image.manifest.v1+json") {
				t.Error("missing OCI accept")
			}
			w.Header().Set("Docker-Content-Digest", testDigest)
			fmt.Fprintf(w, `{"mediaType":"application/vnd.oci.image.manifest.v1+json","config":{"digest":%q},"layers":[{"size":100},{"size":200}]}`, testDigest)
		case "/v2/team/app/blobs/" + testDigest:
			fmt.Fprint(w, `{"os":"linux","architecture":"arm64","created":"2026-01-01T00:00:00Z"}`)
		default:
			w.WriteHeader(404)
		}
	})
	tags, err := c.Tags(context.Background(), "team/app")
	if err != nil || len(tags) != 2 {
		t.Fatalf("%v %v", tags, err)
	}
	if tags[0].Name != "broken" || tags[0].Error == "" {
		t.Errorf("failed tag missing error: %+v", tags[0])
	}
	latest := tags[1]
	if latest.Digest != testDigest || latest.Size != 300 || latest.Created == "" || strings.Join(latest.Platforms, ",") != "linux/arm64" {
		t.Errorf("wrong metadata %+v", latest)
	}
}

func TestMultiPlatformCountsSharedLayersOnce(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, "/tags/list") {
			fmt.Fprint(w, `{"name":"app","tags":["latest"]}`)
			return
		}
		if strings.HasSuffix(r.URL.Path, "/manifests/latest") {
			w.Header().Set("Docker-Content-Digest", testDigest)
			fmt.Fprintf(w, `{"manifests":[{"digest":%q,"platform":{"os":"linux","architecture":"amd64"}},{"digest":%q,"platform":{"os":"linux","architecture":"arm64","variant":"v8"}}]}`, testDigest, "sha256:"+strings.Repeat("b", 64))
			return
		}
		fmt.Fprint(w, `{"layers":[{"digest":"shared","size":100}]}`)
	})
	tags, err := c.Tags(context.Background(), "app")
	if err != nil || len(tags) != 1 {
		t.Fatalf("%v %v", tags, err)
	}
	if tags[0].Size != 100 || strings.Join(tags[0].Platforms, ",") != "linux/amd64,linux/arm64/v8" {
		t.Errorf("unexpected index metadata %+v", tags[0])
	}
}

func TestDeleteUsesDigestAndRejectsTraversal(t *testing.T) {
	calls := 0
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method != "DELETE" || r.URL.Path != "/v2/team/app/manifests/"+testDigest {
			t.Errorf("unexpected delete %s %s", r.Method, r.URL.Path)
		}
		w.WriteHeader(202)
	})
	if err := c.Delete(context.Background(), "team/app", testDigest); err != nil {
		t.Fatal(err)
	}
	for _, repo := range []string{"../app", "app/../../x", "app?x=1", "App", "app//image"} {
		if err := c.Delete(context.Background(), repo, testDigest); err == nil {
			t.Errorf("accepted %q", repo)
		}
	}
	if err := c.Delete(context.Background(), "app", "latest"); err == nil {
		t.Error("accepted tag for digest deletion")
	}
	if calls != 1 {
		t.Errorf("invalid inputs reached upstream: %d", calls)
	}
}

func TestEmptyTagsAreJSONArray(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, _ *http.Request) { fmt.Fprint(w, `{"name":"app","tags":null}`) })
	tags, err := c.Tags(context.Background(), "app")
	if err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(tags)
	if string(data) != "[]" {
		t.Fatalf("got %s", data)
	}
}

func TestRejectInvalidOrigins(t *testing.T) {
	for _, origin := range []string{"file:///tmp/x", "http://user:pass@registry", "http://registry/path", "http://registry?x=y"} {
		if _, err := New(origin); err == nil {
			t.Errorf("accepted %q", origin)
		}
	}
}
