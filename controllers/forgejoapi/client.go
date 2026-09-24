// Package forgejoapi implements the small subset of the Forgejo REST API that the
// template-controller needs. Gitea instances expose the same API.
//
// A dedicated client is used instead of an SDK so that listed pull requests keep the raw
// JSON returned by the API (including fields such as `draft`), which templates can then
// access without the controller having to track every API field.
package forgejoapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// DefaultURL is used when no instance URL is specified.
const DefaultURL = "https://codeberg.org"

// pageSize is requested per page. Servers clamp it to their MAX_RESPONSE_ITEMS setting,
// which is why pagination follows the Link header instead of counting items.
const pageSize = 50

// maxBodySize limits the response bodies read by the client.
const maxBodySize = 32 << 20

var linkNextRegex = regexp.MustCompile(`<[^>]+>\s*;\s*rel="next"`)

// APIError is returned for non-2xx responses.
type APIError struct {
	StatusCode int
	Message    string
	Method     string
	Path       string
}

func (e *APIError) Error() string {
	if e.Message == "" {
		return fmt.Sprintf("forgejo api: %s %s returned HTTP %d", e.Method, e.Path, e.StatusCode)
	}
	return fmt.Sprintf("forgejo api: %s %s returned HTTP %d: %s", e.Method, e.Path, e.StatusCode, e.Message)
}

// IsNotFound reports whether err is an APIError with status 404.
func IsNotFound(err error) bool {
	var apiErr *APIError
	return errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusNotFound
}

// User is the reduced representation of a Forgejo user.
type User struct {
	ID    int64  `json:"id"`
	Login string `json:"login"`
}

// Comment is an issue or pull request comment.
type Comment struct {
	ID        int64     `json:"id"`
	Body      string    `json:"body"`
	User      *User     `json:"user"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// PullReview is a pull request review.
type PullReview struct {
	ID        int64  `json:"id"`
	User      *User  `json:"user"`
	State     string `json:"state"`
	Dismissed bool   `json:"dismissed"`
}

// ReviewStateApproved is the review event/state used for approvals.
const ReviewStateApproved = "APPROVED"

// Client talks to one Forgejo instance.
type Client struct {
	baseURL    string
	token      string
	httpClient *http.Client
}

// NewClient creates a client for the instance at baseURL. An empty baseURL selects DefaultURL.
// An empty token results in anonymous requests.
func NewClient(baseURL string, token string) (*Client, error) {
	if baseURL == "" {
		baseURL = DefaultURL
	}
	u, err := url.Parse(baseURL)
	if err != nil {
		return nil, fmt.Errorf("invalid forgejo url %q: %w", baseURL, err)
	}
	if (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, fmt.Errorf("invalid forgejo url %q: expected an absolute http(s) URL", baseURL)
	}
	return &Client{
		baseURL:    strings.TrimRight(baseURL, "/"),
		token:      token,
		httpClient: &http.Client{Timeout: 60 * time.Second},
	}, nil
}

func (c *Client) do(ctx context.Context, method string, apiPath string, query url.Values, body any, out any) (*http.Response, error) {
	u := c.baseURL + "/api/v1" + apiPath
	if len(query) > 0 {
		u += "?" + query.Encode()
	}

	var reader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		reader = bytes.NewReader(b)
	}

	req, err := http.NewRequestWithContext(ctx, method, u, reader)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.token != "" {
		req.Header.Set("Authorization", "token "+c.token)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(io.LimitReader(resp.Body, maxBodySize))
	if err != nil {
		return nil, err
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return resp, &APIError{
			StatusCode: resp.StatusCode,
			Message:    apiMessage(data),
			Method:     method,
			Path:       apiPath,
		}
	}

	if out != nil && len(bytes.TrimSpace(data)) > 0 {
		dec := json.NewDecoder(bytes.NewReader(data))
		dec.UseNumber()
		if err := dec.Decode(out); err != nil {
			return resp, fmt.Errorf("forgejo api: decoding %s %s response: %w", method, apiPath, err)
		}
	}
	return resp, nil
}

func apiMessage(data []byte) string {
	var m struct {
		Message string `json:"message"`
	}
	if err := json.Unmarshal(data, &m); err == nil && m.Message != "" {
		return m.Message
	}
	s := strings.TrimSpace(string(data))
	if len(s) > 200 {
		s = s[:200] + "..."
	}
	return s
}

func hasNextPage(resp *http.Response) bool {
	if resp == nil {
		return false
	}
	for _, l := range resp.Header.Values("Link") {
		if linkNextRegex.MatchString(l) {
			return true
		}
	}
	return false
}

func repoPath(owner string, repo string) string {
	return "/repos/" + url.PathEscape(owner) + "/" + url.PathEscape(repo)
}

// ListPullRequests returns the pull requests of a repository as raw API objects. state must be
// one of "open", "closed" or "all". A limit <= 0 means no limit.
func (c *Client) ListPullRequests(ctx context.Context, owner string, repo string, state string, limit int) ([]map[string]any, error) {
	var result []map[string]any
	for page := 1; limit <= 0 || len(result) < limit; page++ {
		q := url.Values{}
		if state != "" {
			q.Set("state", state)
		}
		q.Set("page", strconv.Itoa(page))
		q.Set("limit", strconv.Itoa(pageSize))

		var items []map[string]any
		resp, err := c.do(ctx, http.MethodGet, repoPath(owner, repo)+"/pulls", q, nil, &items)
		if err != nil {
			return nil, err
		}
		result = append(result, items...)
		if len(items) == 0 || !hasNextPage(resp) {
			break
		}
	}
	if limit > 0 && len(result) > limit {
		result = result[:limit]
	}
	return result, nil
}

// GetCurrentUser returns the user that owns the API token.
func (c *Client) GetCurrentUser(ctx context.Context) (*User, error) {
	var user User
	if _, err := c.do(ctx, http.MethodGet, "/user", nil, nil, &user); err != nil {
		return nil, err
	}
	return &user, nil
}

// ListIssueComments returns the comments of an issue or pull request. If since is non-zero,
// only comments updated at or after that time are returned.
func (c *Client) ListIssueComments(ctx context.Context, owner string, repo string, index int64, since time.Time) ([]Comment, error) {
	var result []Comment
	for page := 1; ; page++ {
		q := url.Values{}
		q.Set("page", strconv.Itoa(page))
		q.Set("limit", strconv.Itoa(pageSize))
		if !since.IsZero() {
			q.Set("since", since.UTC().Format(time.RFC3339))
		}

		var items []Comment
		resp, err := c.do(ctx, http.MethodGet, repoPath(owner, repo)+"/issues/"+strconv.FormatInt(index, 10)+"/comments", q, nil, &items)
		if err != nil {
			return nil, err
		}
		result = append(result, items...)
		if len(items) == 0 || !hasNextPage(resp) {
			break
		}
	}
	return result, nil
}

// GetIssueComment returns a single comment, or nil when it does not exist.
func (c *Client) GetIssueComment(ctx context.Context, owner string, repo string, id int64) (*Comment, error) {
	var comment Comment
	if _, err := c.do(ctx, http.MethodGet, repoPath(owner, repo)+"/issues/comments/"+strconv.FormatInt(id, 10), nil, nil, &comment); err != nil {
		if IsNotFound(err) {
			return nil, nil
		}
		return nil, err
	}
	return &comment, nil
}

// CreateIssueComment posts a new comment to an issue or pull request.
func (c *Client) CreateIssueComment(ctx context.Context, owner string, repo string, index int64, body string) (*Comment, error) {
	var comment Comment
	payload := map[string]string{"body": body}
	if _, err := c.do(ctx, http.MethodPost, repoPath(owner, repo)+"/issues/"+strconv.FormatInt(index, 10)+"/comments", nil, payload, &comment); err != nil {
		return nil, err
	}
	return &comment, nil
}

// EditIssueComment replaces the body of an existing comment.
func (c *Client) EditIssueComment(ctx context.Context, owner string, repo string, id int64, body string) (*Comment, error) {
	var comment Comment
	payload := map[string]string{"body": body}
	if _, err := c.do(ctx, http.MethodPatch, repoPath(owner, repo)+"/issues/comments/"+strconv.FormatInt(id, 10), nil, payload, &comment); err != nil {
		return nil, err
	}
	return &comment, nil
}

// ListPullReviews returns all reviews of a pull request in chronological order.
func (c *Client) ListPullReviews(ctx context.Context, owner string, repo string, index int64) ([]PullReview, error) {
	var result []PullReview
	for page := 1; ; page++ {
		q := url.Values{}
		q.Set("page", strconv.Itoa(page))
		q.Set("limit", strconv.Itoa(pageSize))

		var items []PullReview
		resp, err := c.do(ctx, http.MethodGet, repoPath(owner, repo)+"/pulls/"+strconv.FormatInt(index, 10)+"/reviews", q, nil, &items)
		if err != nil {
			return nil, err
		}
		result = append(result, items...)
		if len(items) == 0 || !hasNextPage(resp) {
			break
		}
	}
	return result, nil
}

// CreatePullReview submits a review with the given event (for example ReviewStateApproved).
func (c *Client) CreatePullReview(ctx context.Context, owner string, repo string, index int64, event string, body string) (*PullReview, error) {
	var review PullReview
	payload := map[string]string{"event": event, "body": body}
	if _, err := c.do(ctx, http.MethodPost, repoPath(owner, repo)+"/pulls/"+strconv.FormatInt(index, 10)+"/reviews", nil, payload, &review); err != nil {
		return nil, err
	}
	return &review, nil
}

// DismissPullReview dismisses a review.
func (c *Client) DismissPullReview(ctx context.Context, owner string, repo string, index int64, reviewID int64, message string) error {
	payload := map[string]string{"message": message}
	_, err := c.do(ctx, http.MethodPost, repoPath(owner, repo)+"/pulls/"+strconv.FormatInt(index, 10)+"/reviews/"+strconv.FormatInt(reviewID, 10)+"/dismissals", nil, payload, nil)
	return err
}
