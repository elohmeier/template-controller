/*
Copyright 2022.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controllers

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"

	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	templatesv1alpha1 "github.com/kluctl/template-controller/api/v1alpha1"
	"github.com/kluctl/template-controller/controllers/forgejoapi"
)

// ListForgejoPullRequestsReconciler reconciles a ListForgejoPullRequests object
type ListForgejoPullRequestsReconciler struct {
	client.Client
	Scheme       *runtime.Scheme
	FieldManager string
}

//+kubebuilder:rbac:groups=templates.kluctl.io,resources=listforgejopullrequests,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=templates.kluctl.io,resources=listforgejopullrequests/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=templates.kluctl.io,resources=listforgejopullrequests/finalizers,verbs=update
//+kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch

func (r *ListForgejoPullRequestsReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	var obj templatesv1alpha1.ListForgejoPullRequests
	err := r.Get(ctx, req.NamespacedName, &obj)
	if err != nil {
		err = client.IgnoreNotFound(err)
		if err != nil {
			logger.Error(err, "Get failed")
		}
		return ctrl.Result{}, err
	}

	err = r.doReconcile(ctx, &obj)
	if err != nil {
		c := metav1.Condition{
			Type:               "Ready",
			Status:             metav1.ConditionFalse,
			ObservedGeneration: obj.GetGeneration(),
			Reason:             "Error",
			Message:            err.Error(),
		}
		apimeta.SetStatusCondition(&obj.Status.Conditions, c)
	} else {
		c := metav1.Condition{
			Type:               "Ready",
			Status:             metav1.ConditionTrue,
			ObservedGeneration: obj.GetGeneration(),
			Reason:             "Success",
			Message:            "Success",
		}
		apimeta.SetStatusCondition(&obj.Status.Conditions, c)
	}

	// TODO optimize the update as it currently causes to update all pull requests on every call
	// patching is not working very well as causes nulls to be pruned and full array replacement for every single change
	err = r.Status().Update(ctx, &obj, SubResourceFieldOwner(r.FieldManager))
	if err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{
		RequeueAfter: obj.Spec.Interval.Duration,
	}, nil
}

func (r *ListForgejoPullRequestsReconciler) doReconcile(ctx context.Context, obj *templatesv1alpha1.ListForgejoPullRequests) error {
	var token string
	var err error

	if obj.Spec.TokenRef != nil {
		token, err = GetSecretToken(ctx, r.Client, obj.Namespace, *obj.Spec.TokenRef)
		if err != nil {
			return err
		}
	}

	if obj.Spec.Owner == "" {
		return fmt.Errorf("missing forgejo owner")
	}
	if obj.Spec.Repo == "" {
		return fmt.Errorf("missing forgejo repo")
	}

	filter, err := newForgejoPullRequestFilter(obj.Spec.Head, obj.Spec.Base, obj.Spec.Labels)
	if err != nil {
		return err
	}

	baseURL := ""
	if obj.Spec.URL != nil {
		baseURL = *obj.Spec.URL
	}
	fc, err := forgejoapi.NewClient(baseURL, token)
	if err != nil {
		return err
	}

	state := obj.Spec.State
	if state == "" {
		state = "all"
	}

	result, err := fc.ListPullRequests(ctx, obj.Spec.Owner, obj.Spec.Repo, state, obj.Spec.Limit)
	if err != nil {
		return err
	}

	sort.SliceStable(result, func(i, j int) bool {
		return forgejoNumberField(result[i], "id") < forgejoNumberField(result[j], "id")
	})

	newPullRequests := make([]runtime.RawExtension, 0, len(result))

	for _, pr := range result {
		if !filter.matches(pr) {
			continue
		}

		reduceForgejoPullRequest(pr)

		j, err := json.Marshal(pr)
		if err != nil {
			return err
		}

		newPullRequests = append(newPullRequests, runtime.RawExtension{Raw: j})
	}

	obj.Status.PullRequests = newPullRequests

	return nil
}

type forgejoPullRequestFilter struct {
	head   *regexp.Regexp
	base   *regexp.Regexp
	labels []string
}

func newForgejoPullRequestFilter(head *string, base *string, labels []string) (*forgejoPullRequestFilter, error) {
	f := &forgejoPullRequestFilter{labels: labels}
	var err error
	if head != nil {
		f.head, err = regexp.Compile(fmt.Sprintf("^%s$", *head))
		if err != nil {
			return nil, err
		}
	}
	if base != nil {
		f.base, err = regexp.Compile(fmt.Sprintf("^%s$", *base))
		if err != nil {
			return nil, err
		}
	}
	return f, nil
}

func (f *forgejoPullRequestFilter) matches(pr map[string]any) bool {
	if f.head != nil && !f.head.MatchString(forgejoStringField(pr, "head", "label")) {
		return false
	}
	if f.base != nil && !f.base.MatchString(forgejoStringField(pr, "base", "ref")) {
		return false
	}
	if len(f.labels) == 0 {
		return true
	}
	prLabels := map[string]bool{}
	if labels, ok := pr["labels"].([]any); ok {
		for _, l := range labels {
			if lm, ok := l.(map[string]any); ok {
				if name, ok := lm["name"].(string); ok {
					prLabels[name] = true
				}
			}
		}
	}
	for _, l := range f.labels {
		if !prLabels[l] {
			return false
		}
	}
	return true
}

func forgejoStringField(m map[string]any, path ...string) string {
	var cur any = m
	for _, p := range path {
		cm, ok := cur.(map[string]any)
		if !ok {
			return ""
		}
		cur, ok = cm[p]
		if !ok {
			return ""
		}
	}
	s, _ := cur.(string)
	return s
}

func forgejoNumberField(m map[string]any, key string) int64 {
	switch x := m[key].(type) {
	case json.Number:
		i, err := x.Int64()
		if err != nil {
			f, err := x.Float64()
			if err != nil {
				return 0
			}
			return int64(f)
		}
		return i
	case float64:
		return int64(x)
	case int64:
		return x
	case int:
		return int64(x)
	}
	return 0
}

var (
	forgejoUserFields       = []string{"id", "login"}
	forgejoTeamFields       = []string{"id", "name"}
	forgejoLabelFields      = []string{"id", "name"}
	forgejoMilestoneFields  = []string{"id", "title"}
	forgejoRepositoryFields = []string{"id", "name", "full_name", "owner"}
)

// reduceForgejoPullRequest reduces the verbosity of a raw pull request object. All fields that describe
// users, teams, labels, milestones and repositories are reduced to their identifying fields, similar to
// what is done for GitHub pull requests.
func reduceForgejoPullRequest(pr map[string]any) {
	for _, k := range []string{"user", "assignee", "merged_by"} {
		reduceForgejoMapField(pr, k, forgejoUserFields)
	}
	for _, k := range []string{"assignees", "requested_reviewers"} {
		reduceForgejoListField(pr, k, forgejoUserFields)
	}
	reduceForgejoListField(pr, "requested_reviewers_teams", forgejoTeamFields)
	reduceForgejoListField(pr, "labels", forgejoLabelFields)
	reduceForgejoMapField(pr, "milestone", forgejoMilestoneFields)
	for _, k := range []string{"head", "base"} {
		branch, ok := pr[k].(map[string]any)
		if !ok {
			continue
		}
		reduceForgejoMapField(branch, "repo", forgejoRepositoryFields)
		if repo, ok := branch["repo"].(map[string]any); ok {
			reduceForgejoMapField(repo, "owner", forgejoUserFields)
		}
	}
}

func reduceForgejoMapField(m map[string]any, key string, fields []string) {
	v, ok := m[key].(map[string]any)
	if !ok {
		return
	}
	m[key] = pickForgejoFields(v, fields)
}

func reduceForgejoListField(m map[string]any, key string, fields []string) {
	l, ok := m[key].([]any)
	if !ok {
		return
	}
	for i, e := range l {
		if em, ok := e.(map[string]any); ok {
			l[i] = pickForgejoFields(em, fields)
		}
	}
}

func pickForgejoFields(m map[string]any, fields []string) map[string]any {
	ret := make(map[string]any, len(fields))
	for _, f := range fields {
		if v, ok := m[f]; ok {
			ret[f] = v
		}
	}
	return ret
}

// SetupWithManager sets up the controller with the Manager.
func (r *ListForgejoPullRequestsReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&templatesv1alpha1.ListForgejoPullRequests{}).
		Complete(r)
}
