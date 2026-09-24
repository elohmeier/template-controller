<!-- This comment is uncommented when auto-synced to www-kluctl.io

---
title: ListForgejoPullRequests
linkTitle: ListForgejoPullRequests
description: ListForgejoPullRequests documentation
weight: 30
---
-->

# ListForgejoPullRequests

The `ListForgejoPullRequests` API allows to query the Forgejo API for a list of pull requests (PRs). These PRs
can be filtered when needed. The resulting list of PRs is written into the status of the
`ListForgejoPullRequests` object.

Gitea instances expose the same API and are supported as well.

The resulting PRs list inside the status can for example be used in `ObjectTemplate` to create objects based on
pull requests.

## Example

```yaml
apiVersion: templates.kluctl.io/v1alpha1
kind: ListForgejoPullRequests
metadata:
  name: list-forgejo-prs
  namespace: default
spec:
  interval: 1m
  url: https://codeberg.org
  owner: my-org
  repo: my-repo
  state: open
  base: main
  tokenRef:
    secretName: git-credentials
    key: forgejo-token
```

The above example will regularly (1m interval) query the Forgejo API for PRs inside the `my-org/my-repo`
repository. It will filter for open PRs and for PRs against the main branch.

## Spec fields

### interval

Specifies the interval in which to query the Forgejo API. Defaults to `5m`.

### url

Specifies the base URL of the Forgejo instance, for example `https://codeberg.org`. The API path `/api/v1` is
appended automatically. Defaults to `https://codeberg.org`.

### owner

Specifies the user or organisation name where the repository is located.

### repo

Specifies the repository name to query PRs for.

### tokenRef

In case of private repositories, this field can be used to specify a secret that contains a Forgejo API token.
The token needs the `read:repository` scope. If pull requests from a private repository should be listed, the
token owner must have read access to that repository.

### head

Specifies the head branch to filter PRs for. This matches the `head.label` field of the Forgejo API, which contains
the plain branch name (also for pull requests from forks). To distinguish forks from same-repository pull requests,
compare `head.repo.full_name` with `base.repo.full_name` in the template. The `head` field can also contain regular
expressions.

### base

Specifies the base branch to filter PRs for. The `base` field can also contain regular expressions.

### labels

Specifies a list of labels to filter PRs for.

### state

Specifies the PR state to filter for. Can either be `open`, `closed` or `all`. Default to `all`.

### limit

Limits the number of results to accept. This is a safeguard for repositories with hundreds/thousands of PRs. It defaults
to 100.

## Resulting status

The query result is written into the `status.pullRequests` field of the `ListForgejoPullRequests` object. Each entry
represents a reduced version of the
[Forgejo pull request API](https://codeberg.org/api/swagger#/repository/repoListPullRequests) results. The result is
reduced in verbosity to avoid overloading the Kubernetes apiserver. Reduction means that all fields describing users
(`user`, `assignee`, `assignees`, `merged_by`, `requested_reviewers`, repository owners) are reduced to `id` and `login`,
teams to `id` and `name`, `labels` to `id` and `name`, `milestone` to `id` and `title`, and the repositories inside
`head` and `base` to `id`, `name`, `full_name` and `owner`. All other fields, including `draft`, `merged`, `mergeable`
and the timestamps, are passed through unchanged.

Please note that the resulting PR objects do not follow the typical camel case notion found in CRDs, as these represent
a copy of Forgejo API objects.

Example:

```yaml
apiVersion: templates.kluctl.io/v1alpha1
kind: ListForgejoPullRequests
metadata:
  name: list-forgejo-prs
  namespace: default
spec:
  ...
status:
  conditions:
  - lastTransitionTime: "2026-09-24T14:55:36Z"
    message: Success
    observedGeneration: 3
    reason: Success
    status: "True"
    type: Ready
  pullRequests:
  - base:
      label: main
      ref: main
      repo:
        full_name: my-org/my-repo
        id: 14
        name: my-repo
        owner:
          id: 5
          login: my-org
      repo_id: 14
      sha: 3d6825b9832fca7528dc39ba41c4f629aaf4aa95
    body: "..."
    created_at: "2026-09-24T07:40:12Z"
    draft: false
    head:
      label: feat/previews
      ref: refs/pull/6/head
      repo:
        full_name: my-org/my-repo
        id: 14
        name: my-repo
        owner:
          id: 5
          login: my-org
      repo_id: 14
      sha: fd6d815cba06a71e9fbb73f6b2117da6e346f3a7
    html_url: https://codeberg.org/my-org/my-repo/pulls/6
    id: 42
    labels: []
    merged: false
    number: 6
    state: open
    title: '...'
    updated_at: "2026-09-24T08:08:41Z"
    user:
      id: 1
      login: enno
```
