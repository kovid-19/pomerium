package okta

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"strings"
	"testing"

	"github.com/go-chi/chi"
	"github.com/go-chi/chi/middleware"
	"github.com/stretchr/testify/assert"
	"github.com/tomnomnom/linkheader"

	"github.com/pomerium/pomerium/pkg/grpc/directory"
)

type M = map[string]interface{}

func newMockOkta(srv *httptest.Server, userEmailToGroups map[string][]string) http.Handler {
	allGroups := map[string]struct{}{}
	for _, groups := range userEmailToGroups {
		for _, group := range groups {
			allGroups[group] = struct{}{}
		}
	}

	r := chi.NewRouter()
	r.Use(middleware.Logger)
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("Authorization") != "SSWS APITOKEN" {
				http.Error(w, "forbidden", http.StatusForbidden)
				return
			}
			next.ServeHTTP(w, r)
		})
	})
	r.Get("/api/v1/groups", func(w http.ResponseWriter, r *http.Request) {
		lastUpdated := strings.Contains(r.URL.Query().Get("filter"), "lastUpdated ")
		var groups []string
		for group := range allGroups {
			if lastUpdated && group != "user-updated" {
				continue
			}
			if !lastUpdated && group == "user-updated" {
				continue
			}
			groups = append(groups, group)
		}
		sort.Strings(groups)

		var result []M

		found := r.URL.Query().Get("after") == ""
		for i := range groups {
			if found {
				result = append(result, M{
					"id": groups[i],
					"profile": M{
						"name": groups[i] + "-name",
					},
				})
				break
			}
			found = r.URL.Query().Get("after") == groups[i]
		}

		if len(result) > 0 {
			nextURL := mustParseURL(srv.URL).ResolveReference(r.URL)
			q := nextURL.Query()
			q.Set("after", result[0]["id"].(string))
			nextURL.RawQuery = q.Encode()
			w.Header().Set("Link", linkheader.Link{
				URL: nextURL.String(),
				Rel: "next",
			}.String())
		}

		_ = json.NewEncoder(w).Encode(result)
	})
	r.Get("/api/v1/groups/{group}/users", func(w http.ResponseWriter, r *http.Request) {
		group := chi.URLParam(r, "group")

		var result []M
		for email, groups := range userEmailToGroups {
			for _, g := range groups {
				if group == g {
					result = append(result, M{
						"id": email,
						"profile": M{
							"email": email,
						},
					})
				}
			}
		}
		sort.Slice(result, func(i, j int) bool {
			return result[i]["id"].(string) < result[j]["id"].(string)
		})

		_ = json.NewEncoder(w).Encode(result)
	})
	return r
}

func TestProvider_UserGroups(t *testing.T) {
	var mockOkta http.Handler
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mockOkta.ServeHTTP(w, r)
	}))
	defer srv.Close()
	mockOkta = newMockOkta(srv, map[string][]string{
		"a@example.com": {"user", "admin"},
		"b@example.com": {"user", "test"},
		"c@example.com": {"user"},
	})

	p := New(
		WithServiceAccount(&ServiceAccount{APIKey: "APITOKEN"}),
		WithProviderURL(mustParseURL(srv.URL)),
	)
	groups, users, err := p.UserGroups(context.Background())
	assert.NoError(t, err)
	assert.Equal(t, []*directory.User{
		{
			Id:       "okta/a@example.com",
			GroupIds: []string{"admin", "user"},
		},
		{
			Id:       "okta/b@example.com",
			GroupIds: []string{"test", "user"},
		},
		{
			Id:       "okta/c@example.com",
			GroupIds: []string{"user"},
		},
	}, users)
	assert.Len(t, groups, 3)
}

func TestProvider_UserGroupsQueryUpdated(t *testing.T) {
	var mockOkta http.Handler
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mockOkta.ServeHTTP(w, r)
	}))
	defer srv.Close()
	mockOkta = newMockOkta(srv, map[string][]string{
		"a@example.com":       {"user", "admin"},
		"b@example.com":       {"user", "test"},
		"c@example.com":       {"user"},
		"updated@example.com": {"user-updated"},
	})

	p := New(
		WithServiceAccount(&ServiceAccount{APIKey: "APITOKEN"}),
		WithProviderURL(mustParseURL(srv.URL)),
	)
	groups, users, err := p.UserGroups(context.Background())
	assert.NoError(t, err)
	assert.Equal(t, []*directory.User{
		{
			Id:       "okta/a@example.com",
			GroupIds: []string{"admin", "user"},
		},
		{
			Id:       "okta/b@example.com",
			GroupIds: []string{"test", "user"},
		},
		{
			Id:       "okta/c@example.com",
			GroupIds: []string{"user"},
		},
	}, users)
	assert.Len(t, groups, 3)

	groups, users, err = p.UserGroups(context.Background())
	assert.NoError(t, err)
	assert.Equal(t, []*directory.User{
		{
			Id:       "okta/a@example.com",
			GroupIds: []string{"admin", "user"},
		},
		{
			Id:       "okta/b@example.com",
			GroupIds: []string{"test", "user"},
		},
		{
			Id:       "okta/c@example.com",
			GroupIds: []string{"user"},
		},
		{
			Id:       "okta/updated@example.com",
			GroupIds: []string{"user-updated"},
		},
	}, users)
	assert.Len(t, groups, 4)
}

func mustParseURL(rawurl string) *url.URL {
	u, err := url.Parse(rawurl)
	if err != nil {
		panic(err)
	}
	return u
}
