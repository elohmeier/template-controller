package v1alpha1

import "k8s.io/apimachinery/pkg/util/intstr"

type ForgejoProject struct {
	// URL specifies the base URL of the Forgejo instance, for example https://codeberg.org.
	// Gitea instances are supported as well. If blank, uses https://codeberg.org.
	// +optional
	URL *string `json:"url,omitempty"`

	// Owner specifies the Forgejo user or organisation that owns the repository
	// +required
	Owner string `json:"owner"`

	// Repo specifies the repository name.
	// +required
	Repo string `json:"repo"`

	// TokenRef specifies a secret and key to load the Forgejo API token from
	// +optional
	TokenRef *SecretRef `json:"tokenRef"`
}

type ForgejoPullRequestRef struct {
	ForgejoProject `json:",inline"`

	// PullRequestId specifies the pull request index (the number shown in the Forgejo UI).
	// +required
	PullRequestId *intstr.IntOrString `json:"pullRequestId"`
}
