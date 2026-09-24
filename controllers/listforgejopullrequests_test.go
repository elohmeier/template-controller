package controllers

import (
	"bytes"
	"encoding/json"
	"testing"
)

const forgejoPullRequestFixture = `{
  "id": 42,
  "number": 6,
  "title": "Add previews",
  "body": "text",
  "draft": false,
  "state": "open",
  "flow": 0,
  "html_url": "https://forge.example.com/my-org/my-repo/pulls/6",
  "user": {"id": 1, "login": "enno", "email": "enno@example.com", "avatar_url": "https://x/avatar"},
  "assignee": null,
  "assignees": [{"id": 2, "login": "mirko", "email": "m@example.com"}],
  "requested_reviewers": [{"id": 3, "login": "reviewer", "email": "r@example.com"}],
  "requested_reviewers_teams": [{"id": 9, "name": "owners", "description": "d", "permission": "admin"}],
  "labels": [{"id": 5, "name": "preview", "color": "00aabb", "url": "https://x"}],
  "milestone": {"id": 7, "title": "v1", "description": "d", "state": "open"},
  "merged_by": {"id": 1, "login": "enno", "email": "enno@example.com"},
  "head": {
    "label": "feat/previews",
    "ref": "refs/pull/6/head",
    "sha": "fd6d815cba06a71e9fbb73f6b2117da6e346f3a7",
    "repo_id": 14,
    "repo": {"id": 14, "name": "my-repo", "full_name": "my-org/my-repo", "description": "d", "private": true,
             "owner": {"id": 5, "login": "my-org", "email": "org@example.com"}, "clone_url": "https://x"}
  },
  "base": {
    "label": "main",
    "ref": "main",
    "sha": "3d6825b9832fca7528dc39ba41c4f629aaf4aa95",
    "repo_id": 14,
    "repo": {"id": 14, "name": "my-repo", "full_name": "my-org/my-repo", "owner": {"id": 5, "login": "my-org", "email": "x"}}
  },
  "updated_at": "2026-09-24T08:08:41Z"
}`

func decodeForgejoFixture(t *testing.T) map[string]any {
	t.Helper()
	dec := json.NewDecoder(bytes.NewReader([]byte(forgejoPullRequestFixture)))
	dec.UseNumber()
	var pr map[string]any
	if err := dec.Decode(&pr); err != nil {
		t.Fatal(err)
	}
	return pr
}

func TestForgejoReducePullRequest(t *testing.T) {
	pr := decodeForgejoFixture(t)
	reduceForgejoPullRequest(pr)

	j, err := json.Marshal(pr)
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	if err := json.Unmarshal(j, &out); err != nil {
		t.Fatal(err)
	}

	if out["draft"] != false || out["flow"] != float64(0) || out["title"] != "Add previews" {
		t.Fatalf("top-level fields must be preserved: %v", out)
	}
	user := out["user"].(map[string]any)
	if _, ok := user["email"]; ok || user["login"] != "enno" || user["id"] != float64(1) {
		t.Fatalf("user must be reduced to id/login: %v", user)
	}
	if out["assignee"] != nil {
		t.Fatalf("null fields must stay null: %v", out["assignee"])
	}
	assignees := out["assignees"].([]any)
	if _, ok := assignees[0].(map[string]any)["email"]; ok {
		t.Fatalf("assignees must be reduced: %v", assignees)
	}
	reviewers := out["requested_reviewers"].([]any)
	if reviewers[0].(map[string]any)["login"] != "reviewer" || len(reviewers[0].(map[string]any)) != 2 {
		t.Fatalf("requested reviewers must be reduced: %v", reviewers)
	}
	teams := out["requested_reviewers_teams"].([]any)
	if len(teams[0].(map[string]any)) != 2 || teams[0].(map[string]any)["name"] != "owners" {
		t.Fatalf("teams must be reduced: %v", teams)
	}
	labels := out["labels"].([]any)
	if len(labels[0].(map[string]any)) != 2 || labels[0].(map[string]any)["name"] != "preview" {
		t.Fatalf("labels must be reduced: %v", labels)
	}
	milestone := out["milestone"].(map[string]any)
	if len(milestone) != 2 || milestone["title"] != "v1" {
		t.Fatalf("milestone must be reduced: %v", milestone)
	}
	head := out["head"].(map[string]any)
	if head["label"] != "feat/previews" || head["sha"] != "fd6d815cba06a71e9fbb73f6b2117da6e346f3a7" || head["repo_id"] != float64(14) {
		t.Fatalf("head branch info must be preserved: %v", head)
	}
	repo := head["repo"].(map[string]any)
	if len(repo) != 4 || repo["full_name"] != "my-org/my-repo" {
		t.Fatalf("head repo must be reduced: %v", repo)
	}
	owner := repo["owner"].(map[string]any)
	if len(owner) != 2 || owner["login"] != "my-org" {
		t.Fatalf("repo owner must be reduced: %v", owner)
	}
	if forgejoNumberField(pr, "id") != 42 {
		t.Fatalf("unexpected id %d", forgejoNumberField(pr, "id"))
	}
}

func TestForgejoPullRequestFilter(t *testing.T) {
	s := func(v string) *string { return &v }

	cases := []struct {
		name   string
		head   *string
		base   *string
		labels []string
		want   bool
	}{
		{"no filter", nil, nil, nil, true},
		{"head match", s("feat/.*"), nil, nil, true},
		{"head anchored", s("feat"), nil, nil, false},
		{"base match", nil, s("main"), nil, true},
		{"base mismatch", nil, s("develop"), nil, false},
		{"labels match", nil, nil, []string{"preview"}, true},
		{"labels missing", nil, nil, []string{"preview", "other"}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f, err := newForgejoPullRequestFilter(c.head, c.base, c.labels)
			if err != nil {
				t.Fatal(err)
			}
			if got := f.matches(decodeForgejoFixture(t)); got != c.want {
				t.Fatalf("expected %v, got %v", c.want, got)
			}
		})
	}

	if _, err := newForgejoPullRequestFilter(s("("), nil, nil); err == nil {
		t.Fatal("expected invalid regex error")
	}
}
