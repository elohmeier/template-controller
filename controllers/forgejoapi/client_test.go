package forgejoapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestNewClientValidatesURL(t *testing.T) {
	for _, bad := range []string{"codeberg.org", "ftp://codeberg.org", "://x"} {
		if _, err := NewClient(bad, ""); err == nil {
			t.Errorf("expected error for %q", bad)
		}
	}
	c, err := NewClient("", "")
	if err != nil {
		t.Fatal(err)
	}
	if c.baseURL != DefaultURL {
		t.Fatalf("expected default url, got %q", c.baseURL)
	}
	c, err = NewClient("https://forge.example.com/", "t")
	if err != nil {
		t.Fatal(err)
	}
	if c.baseURL != "https://forge.example.com" {
		t.Fatalf("expected trailing slash to be trimmed, got %q", c.baseURL)
	}
}

func TestListPullRequestsFollowsLinkHeader(t *testing.T) {
	var seenAuth []string
	var seenState []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/repos/my-org/my-repo/pulls" {
			http.NotFound(w, r)
			return
		}
		seenAuth = append(seenAuth, r.Header.Get("Authorization"))
		seenState = append(seenState, r.URL.Query().Get("state"))
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Query().Get("page") {
		case "1":
			w.Header().Set("Link", fmt.Sprintf(`<%s/api/v1/repos/my-org/my-repo/pulls?page=2&state=open>; rel="next",<%s/api/v1/repos/my-org/my-repo/pulls?page=2&state=open>; rel="last"`, serverURL(r), serverURL(r)))
			_, _ = w.Write([]byte(`[{"id": 10, "number": 1, "draft": true}, {"id": 11, "number": 2}]`))
		case "2":
			_, _ = w.Write([]byte(`[{"id": 12, "number": 3}]`))
		default:
			_, _ = w.Write([]byte(`[]`))
		}
	}))
	defer srv.Close()

	c, err := NewClient(srv.URL, "secret")
	if err != nil {
		t.Fatal(err)
	}
	prs, err := c.ListPullRequests(context.Background(), "my-org", "my-repo", "open", 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(prs) != 3 {
		t.Fatalf("expected 3 pull requests, got %d", len(prs))
	}
	if prs[0]["draft"] != true {
		t.Fatalf("expected raw draft field to be preserved, got %v", prs[0]["draft"])
	}
	if n, ok := prs[2]["id"].(json.Number); !ok || n.String() != "12" {
		t.Fatalf("expected json.Number id 12, got %#v", prs[2]["id"])
	}
	for _, a := range seenAuth {
		if a != "token secret" {
			t.Fatalf("unexpected authorization header %q", a)
		}
	}
	for _, s := range seenState {
		if s != "open" {
			t.Fatalf("unexpected state query %q", s)
		}
	}

	prs, err = c.ListPullRequests(context.Background(), "my-org", "my-repo", "open", 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(prs) != 2 {
		t.Fatalf("expected limit to truncate to 2, got %d", len(prs))
	}
}

func serverURL(r *http.Request) string {
	return "http://" + r.Host
}

func TestCommentsAndErrors(t *testing.T) {
	var lastBody map[string]string
	var lastMethod string
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/repos/o/r/issues/comments/404", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"message":"comment does not exist"}`))
	})
	mux.HandleFunc("/api/v1/repos/o/r/issues/comments/7", func(w http.ResponseWriter, r *http.Request) {
		lastMethod = r.Method
		if r.Method == http.MethodPatch {
			b, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(b, &lastBody)
		}
		_, _ = w.Write([]byte(`{"id": 7, "body": "hello", "user": {"id": 3, "login": "bot"}, "created_at": "2024-01-02T03:04:05Z"}`))
	})
	mux.HandleFunc("/api/v1/repos/o/r/issues/5/comments", func(w http.ResponseWriter, r *http.Request) {
		lastMethod = r.Method
		if r.Method == http.MethodPost {
			b, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(b, &lastBody)
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"id": 8, "body": "` + lastBody["body"] + `", "created_at": "2024-01-02T03:04:05Z"}`))
			return
		}
		if r.URL.Query().Get("since") == "" {
			t.Errorf("expected since query parameter")
		}
		_, _ = w.Write([]byte(`[{"id": 7, "body": "hello", "created_at": "2024-01-02T03:04:05Z"}]`))
	})
	mux.HandleFunc("/api/v1/repos/o/r/issues/6/comments", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"message":"token does not have at least one of required scope(s): [read:issue]"}`))
	})
	s := httptest.NewServer(mux)
	defer s.Close()

	c, err := NewClient(s.URL, "t")
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	n, err := c.GetIssueComment(ctx, "o", "r", 404)
	if err != nil || n != nil {
		t.Fatalf("expected nil, nil for missing comment, got %v, %v", n, err)
	}

	n, err = c.GetIssueComment(ctx, "o", "r", 7)
	if err != nil {
		t.Fatal(err)
	}
	if n.ID != 7 || n.Body != "hello" || n.User.Login != "bot" || n.CreatedAt != time.Date(2024, 1, 2, 3, 4, 5, 0, time.UTC) {
		t.Fatalf("unexpected comment %+v", n)
	}

	created, err := c.CreateIssueComment(ctx, "o", "r", 5, "new body")
	if err != nil {
		t.Fatal(err)
	}
	if created.ID != 8 || lastMethod != http.MethodPost || lastBody["body"] != "new body" {
		t.Fatalf("unexpected create result %+v (%s %v)", created, lastMethod, lastBody)
	}

	if _, err = c.EditIssueComment(ctx, "o", "r", 7, "edited"); err != nil {
		t.Fatal(err)
	}
	if lastMethod != http.MethodPatch || lastBody["body"] != "edited" {
		t.Fatalf("unexpected edit request %s %v", lastMethod, lastBody)
	}

	list, err := c.ListIssueComments(ctx, "o", "r", 5, time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].ID != 7 {
		t.Fatalf("unexpected list %+v", list)
	}

	_, err = c.ListIssueComments(ctx, "o", "r", 6, time.Time{})
	var apiErr *APIError
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusForbidden || apiErr.Message == "" {
		t.Fatalf("unexpected error %v", err)
	}
	if IsNotFound(err) {
		t.Fatal("403 must not be reported as not found")
	}
}

func TestReviews(t *testing.T) {
	var dismissed []string
	var createdEvent string
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/user", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"id": 3, "login": "bot"}`))
	})
	mux.HandleFunc("/api/v1/repos/o/r/pulls/5/reviews", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			b, _ := io.ReadAll(r.Body)
			var payload map[string]string
			_ = json.Unmarshal(b, &payload)
			createdEvent = payload["event"]
			_, _ = w.Write([]byte(`{"id": 99, "state": "APPROVED", "user": {"id": 3, "login": "bot"}}`))
			return
		}
		_, _ = w.Write([]byte(`[{"id": 1, "state": "COMMENT", "user": {"id": 3, "login": "bot"}}, {"id": 2, "state": "APPROVED", "dismissed": true, "user": {"id": 3, "login": "bot"}}]`))
	})
	mux.HandleFunc("/api/v1/repos/o/r/pulls/5/reviews/2/dismissals", func(w http.ResponseWriter, r *http.Request) {
		dismissed = append(dismissed, r.URL.Path)
		_, _ = w.Write([]byte(`{"id": 2}`))
	})
	s := httptest.NewServer(mux)
	defer s.Close()

	c, err := NewClient(s.URL, "t")
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	u, err := c.GetCurrentUser(ctx)
	if err != nil || u.ID != 3 {
		t.Fatalf("unexpected user %+v %v", u, err)
	}
	reviews, err := c.ListPullReviews(ctx, "o", "r", 5)
	if err != nil || len(reviews) != 2 || !reviews[1].Dismissed {
		t.Fatalf("unexpected reviews %+v %v", reviews, err)
	}
	review, err := c.CreatePullReview(ctx, "o", "r", 5, ReviewStateApproved, "Approved")
	if err != nil || review.ID != 99 || createdEvent != ReviewStateApproved {
		t.Fatalf("unexpected review %+v %v (%s)", review, err, createdEvent)
	}
	if err := c.DismissPullReview(ctx, "o", "r", 5, 2, "Not approved"); err != nil {
		t.Fatal(err)
	}
	if len(dismissed) != 1 {
		t.Fatalf("expected one dismissal, got %v", dismissed)
	}
}
