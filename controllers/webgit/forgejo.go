package webgit

import (
	"context"
	"fmt"
	"strconv"
	"sync"
	"time"

	"github.com/kluctl/template-controller/api/v1alpha1"
	"github.com/kluctl/template-controller/controllers/forgejoapi"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type ForgejoMergeRequest struct {
	ctx context.Context

	client *forgejoapi.Client
	owner  string
	repo   string
	prId   int64

	currentUserCache *forgejoapi.User
	currentUserMutex sync.Mutex

	review *forgejoapi.PullReview
	mutex  sync.Mutex
}

func (g *ForgejoMergeRequest) getCurrentUser() (*forgejoapi.User, error) {
	g.currentUserMutex.Lock()
	defer g.currentUserMutex.Unlock()

	if g.currentUserCache != nil {
		return g.currentUserCache, nil
	}

	user, err := g.client.GetCurrentUser(g.ctx)
	if err != nil {
		return nil, err
	}
	g.currentUserCache = user
	return g.currentUserCache, nil
}

func (g *ForgejoMergeRequest) convertComment(c *forgejoapi.Comment) Note {
	return &ForgejoNote{
		g:       g,
		comment: c,
	}
}

// findReview returns the latest review of the current user, or nil if there is none.
func (g *ForgejoMergeRequest) findReview() (*forgejoapi.PullReview, error) {
	currentUser, err := g.getCurrentUser()
	if err != nil {
		return nil, err
	}

	g.mutex.Lock()
	defer g.mutex.Unlock()

	if g.review != nil {
		return g.review, nil
	}

	reviews, err := g.client.ListPullReviews(g.ctx, g.owner, g.repo, g.prId)
	if err != nil {
		return nil, err
	}
	for i := len(reviews) - 1; i >= 0; i-- {
		r := reviews[i]
		if r.User != nil && r.User.ID == currentUser.ID {
			g.review = &r
			return g.review, nil
		}
	}

	return nil, nil
}

func (g *ForgejoMergeRequest) HasApproved() (bool, error) {
	review, err := g.findReview()
	if err != nil {
		return false, err
	}
	if review == nil {
		return false, nil
	}
	return review.State == forgejoapi.ReviewStateApproved && !review.Dismissed, nil
}

func (g *ForgejoMergeRequest) Approve() error {
	review, err := g.client.CreatePullReview(g.ctx, g.owner, g.repo, g.prId, forgejoapi.ReviewStateApproved, "Approved")
	if err != nil {
		return err
	}
	g.mutex.Lock()
	defer g.mutex.Unlock()
	g.review = review
	return nil
}

func (g *ForgejoMergeRequest) Unapprove() error {
	review, err := g.findReview()
	if err != nil {
		return err
	}
	if review == nil || review.Dismissed || review.State != forgejoapi.ReviewStateApproved {
		return nil
	}

	err = g.client.DismissPullReview(g.ctx, g.owner, g.repo, g.prId, review.ID, "Not approved")
	if err != nil {
		return err
	}
	g.mutex.Lock()
	defer g.mutex.Unlock()
	g.review = nil
	return nil
}

func (g *ForgejoMergeRequest) CreateMergeRequestNote(body string) (Note, error) {
	c, err := g.client.CreateIssueComment(g.ctx, g.owner, g.repo, g.prId, body)
	if err != nil {
		return nil, err
	}
	return g.convertComment(c), nil
}

func (g *ForgejoMergeRequest) GetMergeRequestNote(noteId string) (Note, error) {
	noteId2, err := strconv.ParseInt(noteId, 10, 64)
	if err != nil {
		return nil, err
	}
	c, err := g.client.GetIssueComment(g.ctx, g.owner, g.repo, noteId2)
	if err != nil {
		return nil, err
	}
	if c == nil {
		return nil, nil
	}
	return g.convertComment(c), nil
}

func (g *ForgejoMergeRequest) ListMergeRequestNotes() ([]Note, error) {
	return g.ListMergeRequestNotesAfter(time.Time{})
}

func (g *ForgejoMergeRequest) ListMergeRequestNotesAfter(t time.Time) ([]Note, error) {
	comments, err := g.client.ListIssueComments(g.ctx, g.owner, g.repo, g.prId, t)
	if err != nil {
		return nil, err
	}
	ret := make([]Note, 0, len(comments))
	for i := range comments {
		ret = append(ret, g.convertComment(&comments[i]))
	}
	return ret, nil
}

type ForgejoNote struct {
	g       *ForgejoMergeRequest
	comment *forgejoapi.Comment
}

func (n *ForgejoNote) GetId() string {
	return strconv.FormatInt(n.comment.ID, 10)
}

func (n *ForgejoNote) GetBody() string {
	return n.comment.Body
}

func (n *ForgejoNote) GetCreatedAt() time.Time {
	return n.comment.CreatedAt
}

func (n *ForgejoNote) UpdateBody(body string) error {
	newComment, err := n.g.client.EditIssueComment(n.g.ctx, n.g.owner, n.g.repo, n.comment.ID, body)
	if err != nil {
		return err
	}
	n.comment = newComment
	return nil
}

func BuildWebgitMergeRequestForgejo(ctx context.Context, client client.Client, namespace string, info v1alpha1.ForgejoPullRequestRef) (*ForgejoMergeRequest, error) {
	if info.Owner == "" {
		return nil, fmt.Errorf("missing forgejo owner")
	}
	if info.Repo == "" {
		return nil, fmt.Errorf("missing forgejo repo")
	}
	if info.TokenRef == nil {
		return nil, fmt.Errorf("missing forgejo tokenRef")
	}
	if info.PullRequestId == nil {
		return nil, fmt.Errorf("missing forgejo pullRequestId")
	}

	sn := types.NamespacedName{
		Namespace: namespace,
		Name:      info.TokenRef.SecretName,
	}

	var secret v1.Secret
	err := client.Get(ctx, sn, &secret)
	if err != nil {
		return nil, err
	}

	tokenBytes, ok := secret.Data[info.TokenRef.Key]
	if !ok {
		return nil, fmt.Errorf("forgejo token is missing in secret")
	}
	token := string(tokenBytes)

	var prId int64
	switch info.PullRequestId.Type {
	case intstr.Int:
		prId = int64(info.PullRequestId.IntValue())
	case intstr.String:
		prIdString := info.PullRequestId.String()
		prIdInt, err := strconv.ParseInt(prIdString, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("invalid PullRequestId value %q: %v", info.PullRequestId.StrVal, err)
		}
		prId = prIdInt
	default:
		return nil, fmt.Errorf("invalid PullRequestId value: neither int nor string")
	}

	baseURL := ""
	if info.URL != nil {
		baseURL = *info.URL
	}
	fc, err := forgejoapi.NewClient(baseURL, token)
	if err != nil {
		return nil, err
	}

	return &ForgejoMergeRequest{
		ctx:    ctx,
		client: fc,
		owner:  info.Owner,
		repo:   info.Repo,
		prId:   prId,
	}, nil
}
