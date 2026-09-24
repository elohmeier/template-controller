package webgit

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/kluctl/template-controller/controllers/forgejoapi"
)

type fakeForgejo struct {
	mu        sync.Mutex
	comments  []map[string]any
	reviews   []map[string]any
	nextID    int64
	dismissed []int64
}

func (f *fakeForgejo) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/user", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"id": 3, "login": "bot"}`))
	})
	mux.HandleFunc("/api/v1/repos/o/r/issues/5/comments", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		if r.Method == http.MethodPost {
			var payload map[string]string
			b, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(b, &payload)
			f.nextID++
			c := map[string]any{"id": f.nextID, "body": payload["body"], "user": map[string]any{"id": 3, "login": "bot"}, "created_at": "2024-01-02T03:04:05Z"}
			f.comments = append(f.comments, c)
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(c)
			return
		}
		_ = json.NewEncoder(w).Encode(f.comments)
	})
	mux.HandleFunc("/api/v1/repos/o/r/issues/comments/", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		id, _ := json.Number(r.URL.Path[len("/api/v1/repos/o/r/issues/comments/"):]).Int64()
		for _, c := range f.comments {
			if c["id"].(int64) == id {
				if r.Method == http.MethodPatch {
					var payload map[string]string
					b, _ := io.ReadAll(r.Body)
					_ = json.Unmarshal(b, &payload)
					c["body"] = payload["body"]
				}
				_ = json.NewEncoder(w).Encode(c)
				return
			}
		}
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"message":"not found"}`))
	})
	mux.HandleFunc("/api/v1/repos/o/r/pulls/5/reviews", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		if r.Method == http.MethodPost {
			var payload map[string]string
			b, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(b, &payload)
			f.nextID++
			rv := map[string]any{"id": f.nextID, "state": payload["event"], "user": map[string]any{"id": 3, "login": "bot"}, "dismissed": false}
			f.reviews = append(f.reviews, rv)
			_ = json.NewEncoder(w).Encode(rv)
			return
		}
		_ = json.NewEncoder(w).Encode(f.reviews)
	})
	mux.HandleFunc("/api/v1/repos/o/r/pulls/5/reviews/", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		id, _ := json.Number(r.URL.Path[len("/api/v1/repos/o/r/pulls/5/reviews/") : len(r.URL.Path)-len("/dismissals")]).Int64()
		for _, rv := range f.reviews {
			if rv["id"].(int64) == id {
				rv["dismissed"] = true
				f.dismissed = append(f.dismissed, id)
			}
		}
		_, _ = w.Write([]byte(`{}`))
	})
	return mux
}

func newForgejoMergeRequest(t *testing.T, f *fakeForgejo) *ForgejoMergeRequest {
	t.Helper()
	srv := httptest.NewServer(f.handler())
	t.Cleanup(srv.Close)
	c, err := forgejoapi.NewClient(srv.URL, "t")
	if err != nil {
		t.Fatal(err)
	}
	return &ForgejoMergeRequest{ctx: context.Background(), client: c, owner: "o", repo: "r", prId: 5}
}

func TestForgejoNotes(t *testing.T) {
	f := &fakeForgejo{}
	mr := newForgejoMergeRequest(t, f)

	n, err := mr.GetMergeRequestNote("123")
	if err != nil || n != nil {
		t.Fatalf("expected missing note to return nil, nil: %v %v", n, err)
	}
	if _, err := mr.GetMergeRequestNote("abc"); err == nil {
		t.Fatal("expected error for non-numeric note id")
	}

	created, err := mr.CreateMergeRequestNote("first")
	if err != nil {
		t.Fatal(err)
	}
	if created.GetId() != "1" || created.GetBody() != "first" || created.GetCreatedAt().IsZero() {
		t.Fatalf("unexpected note %v", created)
	}

	if err := created.UpdateBody("second"); err != nil {
		t.Fatal(err)
	}
	if created.GetBody() != "second" {
		t.Fatalf("expected updated body, got %q", created.GetBody())
	}

	notes, err := mr.ListMergeRequestNotes()
	if err != nil || len(notes) != 1 || notes[0].GetBody() != "second" {
		t.Fatalf("unexpected notes %v %v", notes, err)
	}

	got, err := mr.GetMergeRequestNote("1")
	if err != nil || got == nil || got.GetBody() != "second" {
		t.Fatalf("unexpected note %v %v", got, err)
	}
}

func TestForgejoApprovals(t *testing.T) {
	f := &fakeForgejo{}
	mr := newForgejoMergeRequest(t, f)

	approved, err := mr.HasApproved()
	if err != nil || approved {
		t.Fatalf("expected no approval yet: %v %v", approved, err)
	}

	if err := mr.Approve(); err != nil {
		t.Fatal(err)
	}
	approved, err = mr.HasApproved()
	if err != nil || !approved {
		t.Fatalf("expected approval: %v %v", approved, err)
	}

	if err := mr.Unapprove(); err != nil {
		t.Fatal(err)
	}
	if len(f.dismissed) != 1 {
		t.Fatalf("expected one dismissal, got %v", f.dismissed)
	}

	// A fresh object must discover the dismissed review from the API and report no approval.
	mr2 := &ForgejoMergeRequest{ctx: context.Background(), client: mr.client, owner: "o", repo: "r", prId: 5}
	approved, err = mr2.HasApproved()
	if err != nil || approved {
		t.Fatalf("expected dismissed review to not count as approval: %v %v", approved, err)
	}
	if err := mr2.Unapprove(); err != nil {
		t.Fatal(err)
	}
	if len(f.dismissed) != 1 {
		t.Fatalf("unapprove must not dismiss an already dismissed review, got %v", f.dismissed)
	}
}
