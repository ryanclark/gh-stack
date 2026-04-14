package github

import (
	"bytes"
	"encoding/json"
	"fmt"

	"github.com/cli/go-gh/v2/pkg/api"
	graphql "github.com/cli/shurcooL-graphql"
)

// MergeQueueEntry represents a merge queue entry. When the GraphQL field
// mergeQueueEntry is null (PR not queued), the pointer will be nil.
type MergeQueueEntry struct {
	ID string `graphql:"id"`
}

// PullRequest represents a GitHub pull request.
type PullRequest struct {
	ID                string           `graphql:"id"`
	Number            int              `graphql:"number"`
	Title             string           `graphql:"title"`
	State             string           `graphql:"state"`
	URL               string           `graphql:"url"`
	HeadRefName       string           `graphql:"headRefName"`
	BaseRefName       string           `graphql:"baseRefName"`
	IsDraft           bool             `graphql:"isDraft"`
	Merged            bool             `graphql:"merged"`
	IsCrossRepository bool             `graphql:"isCrossRepository"`
	MergeQueueEntry   *MergeQueueEntry `graphql:"mergeQueueEntry"`
}

// IsQueued reports whether the pull request is currently in a merge queue.
func (pr *PullRequest) IsQueued() bool {
	return pr != nil && pr.MergeQueueEntry != nil && pr.MergeQueueEntry.ID != ""
}

// Client wraps GitHub API operations.
type Client struct {
	gql   *api.GraphQLClient
	rest  *api.RESTClient
	host  string
	owner string
	repo  string
	slug  string
}

// NewClient creates a new GitHub API client for the given repository.
// The host parameter specifies the GitHub hostname (e.g. "github.com" or a
// GHES hostname like "github.mycompany.com"). If empty, it defaults to
// "github.com".
func NewClient(host, owner, repo string) (*Client, error) {
	if host == "" {
		host = "github.com"
	}
	opts := api.ClientOptions{Host: host}
	gql, err := api.NewGraphQLClient(opts)
	if err != nil {
		return nil, fmt.Errorf("creating GraphQL client: %w", err)
	}
	rest, err := api.NewRESTClient(opts)
	if err != nil {
		return nil, fmt.Errorf("creating REST client: %w", err)
	}
	return &Client{
		gql:   gql,
		rest:  rest,
		host:  host,
		owner: owner,
		repo:  repo,
		slug:  owner + "/" + repo,
	}, nil
}

// PRURL constructs the web URL for a pull request on the given host.
func PRURL(host, owner, repo string, number int) string {
	if host == "" {
		host = "github.com"
	}
	return fmt.Sprintf("https://%s/%s/%s/pull/%d", host, owner, repo, number)
}

// FindPRForBranch finds an open PR by head branch name.
func (c *Client) FindPRForBranch(branch string) (*PullRequest, error) {
	var query struct {
		Repository struct {
			PullRequests struct {
				Nodes []PullRequest
			} `graphql:"pullRequests(headRefName: $head, states: [OPEN], first: 1)"`
		} `graphql:"repository(owner: $owner, name: $name)"`
	}

	variables := map[string]interface{}{
		"owner": graphql.String(c.owner),
		"name":  graphql.String(c.repo),
		"head":  graphql.String(branch),
	}

	if err := c.gql.Query("FindPRForBranch", &query, variables); err != nil {
		return nil, fmt.Errorf("querying PRs: %w", err)
	}

	nodes := query.Repository.PullRequests.Nodes
	if len(nodes) == 0 {
		return nil, nil
	}

	n := nodes[0]
	return &PullRequest{
		ID:              n.ID,
		Number:          n.Number,
		Title:           n.Title,
		State:           n.State,
		URL:             n.URL,
		HeadRefName:     n.HeadRefName,
		BaseRefName:     n.BaseRefName,
		IsDraft:         n.IsDraft,
		Merged:          n.Merged,
		MergeQueueEntry: n.MergeQueueEntry,
	}, nil
}

// FindAnyPRForBranch finds the most recent PR by head branch name regardless of state.
func (c *Client) FindAnyPRForBranch(branch string) (*PullRequest, error) {
	var query struct {
		Repository struct {
			PullRequests struct {
				Nodes []PullRequest
			} `graphql:"pullRequests(headRefName: $head, last: 1)"`
		} `graphql:"repository(owner: $owner, name: $name)"`
	}

	variables := map[string]interface{}{
		"owner": graphql.String(c.owner),
		"name":  graphql.String(c.repo),
		"head":  graphql.String(branch),
	}

	if err := c.gql.Query("FindAnyPRForBranch", &query, variables); err != nil {
		return nil, fmt.Errorf("querying PRs: %w", err)
	}

	nodes := query.Repository.PullRequests.Nodes
	if len(nodes) == 0 {
		return nil, nil
	}

	n := nodes[0]
	return &PullRequest{
		ID:              n.ID,
		Number:          n.Number,
		Title:           n.Title,
		State:           n.State,
		URL:             n.URL,
		HeadRefName:     n.HeadRefName,
		BaseRefName:     n.BaseRefName,
		IsDraft:         n.IsDraft,
		Merged:          n.Merged,
		MergeQueueEntry: n.MergeQueueEntry,
	}, nil
}

// CreatePR creates a new pull request.
func (c *Client) CreatePR(base, head, title, body string, draft bool) (*PullRequest, error) {
	var mutation struct {
		CreatePullRequest struct {
			PullRequest struct {
				ID          string
				Number      int
				Title       string
				State       string
				URL         string `graphql:"url"`
				HeadRefName string
				BaseRefName string
				IsDraft     bool
			}
		} `graphql:"createPullRequest(input: $input)"`
	}

	repoID, err := c.repositoryID()
	if err != nil {
		return nil, err
	}

	type CreatePullRequestInput struct {
		RepositoryID string `json:"repositoryId"`
		BaseRefName  string `json:"baseRefName"`
		HeadRefName  string `json:"headRefName"`
		Title        string `json:"title"`
		Body         string `json:"body,omitempty"`
		Draft        bool   `json:"draft"`
	}

	variables := map[string]interface{}{
		"input": CreatePullRequestInput{
			RepositoryID: repoID,
			BaseRefName:  base,
			HeadRefName:  head,
			Title:        title,
			Body:         body,
			Draft:        draft,
		},
	}

	if err := c.gql.Mutate("CreatePullRequest", &mutation, variables); err != nil {
		return nil, fmt.Errorf("creating PR: %w", err)
	}

	pr := mutation.CreatePullRequest.PullRequest
	return &PullRequest{
		ID:          pr.ID,
		Number:      pr.Number,
		Title:       pr.Title,
		State:       pr.State,
		URL:         pr.URL,
		HeadRefName: pr.HeadRefName,
		BaseRefName: pr.BaseRefName,
		IsDraft:     pr.IsDraft,
	}, nil
}

// UpdatePRBase updates the base branch of an existing pull request.
func (c *Client) UpdatePRBase(number int, base string) error {
	type updatePRRequest struct {
		Base string `json:"base"`
	}

	body, err := json.Marshal(updatePRRequest{Base: base})
	if err != nil {
		return fmt.Errorf("marshaling request: %w", err)
	}

	path := fmt.Sprintf("repos/%s/%s/pulls/%d", c.owner, c.repo, number)
	return c.rest.Patch(path, bytes.NewReader(body), nil)
}

func (c *Client) repositoryID() (string, error) {
	var query struct {
		Repository struct {
			ID string
		} `graphql:"repository(owner: $owner, name: $name)"`
	}

	variables := map[string]interface{}{
		"owner": graphql.String(c.owner),
		"name":  graphql.String(c.repo),
	}

	if err := c.gql.Query("RepositoryID", &query, variables); err != nil {
		return "", fmt.Errorf("fetching repository ID: %w", err)
	}

	return query.Repository.ID, nil
}

// PRDetails holds enriched pull request data for display in the TUI.
type PRDetails struct {
	Number        int
	Title         string
	State         string // OPEN, CLOSED, MERGED
	URL           string
	IsDraft       bool
	Merged        bool
	IsQueued      bool
	CommentsCount int
}

// FindPRDetailsForBranch fetches enriched PR data for display purposes.
// Returns nil without error if no PR exists for the branch.
func (c *Client) FindPRDetailsForBranch(branch string) (*PRDetails, error) {
	var query struct {
		Repository struct {
			PullRequests struct {
				Nodes []struct {
					ID              string           `graphql:"id"`
					Number          int              `graphql:"number"`
					Title           string           `graphql:"title"`
					State           string           `graphql:"state"`
					URL             string           `graphql:"url"`
					HeadRefName     string           `graphql:"headRefName"`
					BaseRefName     string           `graphql:"baseRefName"`
					IsDraft         bool             `graphql:"isDraft"`
					Merged          bool             `graphql:"merged"`
					MergeQueueEntry *MergeQueueEntry `graphql:"mergeQueueEntry"`
					Comments        struct {
						TotalCount int `graphql:"totalCount"`
					} `graphql:"comments"`
				}
			} `graphql:"pullRequests(headRefName: $head, last: 1)"`
		} `graphql:"repository(owner: $owner, name: $name)"`
	}

	variables := map[string]interface{}{
		"owner": graphql.String(c.owner),
		"name":  graphql.String(c.repo),
		"head":  graphql.String(branch),
	}

	if err := c.gql.Query("FindPRDetailsForBranch", &query, variables); err != nil {
		return nil, fmt.Errorf("querying PR details: %w", err)
	}

	nodes := query.Repository.PullRequests.Nodes
	if len(nodes) == 0 {
		return nil, nil
	}

	n := nodes[0]
	return &PRDetails{
		Number:        n.Number,
		Title:         n.Title,
		State:         n.State,
		URL:           n.URL,
		IsDraft:       n.IsDraft,
		Merged:        n.Merged,
		IsQueued:      n.MergeQueueEntry != nil && n.MergeQueueEntry.ID != "",
		CommentsCount: n.Comments.TotalCount,
	}, nil
}

// FindPRByNumber fetches a pull request by its number.
func (c *Client) FindPRByNumber(number int) (*PullRequest, error) {
	var query struct {
		Repository struct {
			PullRequest struct {
				ID              string           `graphql:"id"`
				Number          int              `graphql:"number"`
				Title           string           `graphql:"title"`
				State           string           `graphql:"state"`
				URL             string           `graphql:"url"`
				HeadRefName     string           `graphql:"headRefName"`
				BaseRefName     string           `graphql:"baseRefName"`
				IsDraft         bool             `graphql:"isDraft"`
				Merged          bool             `graphql:"merged"`
				MergeQueueEntry *MergeQueueEntry `graphql:"mergeQueueEntry"`
			} `graphql:"pullRequest(number: $number)"`
		} `graphql:"repository(owner: $owner, name: $name)"`
	}

	variables := map[string]interface{}{
		"owner":  graphql.String(c.owner),
		"name":   graphql.String(c.repo),
		"number": graphql.Int(number),
	}

	if err := c.gql.Query("FindPRByNumber", &query, variables); err != nil {
		return nil, fmt.Errorf("querying PR #%d: %w", number, err)
	}

	n := query.Repository.PullRequest
	return &PullRequest{
		ID:              n.ID,
		Number:          n.Number,
		Title:           n.Title,
		State:           n.State,
		URL:             n.URL,
		HeadRefName:     n.HeadRefName,
		BaseRefName:     n.BaseRefName,
		IsDraft:         n.IsDraft,
		Merged:          n.Merged,
		MergeQueueEntry: n.MergeQueueEntry,
	}, nil
}

// FindPRByBaseBranch finds an open PR by base branch name.
func (c *Client) FindPRByBaseBranch(base string) (*PullRequest, error) {
	var query struct {
		Repository struct {
			PullRequests struct {
				Nodes []PullRequest
			} `graphql:"pullRequests(baseRefName: $base, states: [OPEN], first: 1)"`
		} `graphql:"repository(owner: $owner, name: $name)"`
	}

	variables := map[string]interface{}{
		"owner": graphql.String(c.owner),
		"name":  graphql.String(c.repo),
		"base":  graphql.String(base),
	}

	if err := c.gql.Query("FindPRByBaseBranch", &query, variables); err != nil {
		return nil, fmt.Errorf("querying PRs by base: %w", err)
	}

	nodes := query.Repository.PullRequests.Nodes
	if len(nodes) == 0 {
		return nil, nil
	}

	n := nodes[0]
	return &PullRequest{
		ID:              n.ID,
		Number:          n.Number,
		Title:           n.Title,
		State:           n.State,
		URL:             n.URL,
		HeadRefName:     n.HeadRefName,
		BaseRefName:     n.BaseRefName,
		IsDraft:         n.IsDraft,
		Merged:          n.Merged,
		MergeQueueEntry: n.MergeQueueEntry,
	}, nil
}

// DiscoverPRStack discovers a stack of PRs by following the base/head
// branch chain from a starting PR number. It walks down (toward trunk)
// by finding PRs whose head matches each PR's base, and up (away from
// trunk) by finding PRs whose base matches each PR's head.
// Returns the trunk branch name and the ordered list of PRs (bottom to top).
func DiscoverPRStack(client ClientOps, prNumber int) (string, []*PullRequest, error) {
	startPR, err := client.FindPRByNumber(prNumber)
	if err != nil {
		return "", nil, fmt.Errorf("fetching PR #%d: %w", prNumber, err)
	}
	if startPR == nil {
		return "", nil, fmt.Errorf("PR #%d not found", prNumber)
	}

	seen := map[int]bool{startPR.Number: true}

	// Walk down toward trunk: find PRs whose head is our base.
	// Uses FindAnyPRForBranch to also discover merged parent PRs.
	// Skips cross-repository (fork) PRs and PRs where head == base.
	var parents []*PullRequest
	current := startPR
	for {
		parent, err := client.FindAnyPRForBranch(current.BaseRefName)
		if err != nil || parent == nil || seen[parent.Number] {
			break
		}
		if parent.IsCrossRepository || parent.HeadRefName == parent.BaseRefName {
			break
		}
		seen[parent.Number] = true
		parents = append(parents, parent)
		current = parent
	}

	// Reverse parents to bottom-to-top order.
	for i, j := 0, len(parents)-1; i < j; i, j = i+1, j-1 {
		parents[i], parents[j] = parents[j], parents[i]
	}

	// Trunk is the base of the bottommost PR.
	trunk := startPR.BaseRefName
	if len(parents) > 0 {
		trunk = parents[0].BaseRefName
	}

	// Build chain: parents + start PR.
	chain := make([]*PullRequest, 0, len(parents)+1)
	chain = append(chain, parents...)
	chain = append(chain, startPR)

	// Walk up away from trunk: find open PRs whose base is our head.
	// Skips cross-repository (fork) PRs.
	current = startPR
	for {
		child, err := client.FindPRByBaseBranch(current.HeadRefName)
		if err != nil || child == nil || seen[child.Number] {
			break
		}
		if child.IsCrossRepository || child.HeadRefName == child.BaseRefName {
			break
		}
		seen[child.Number] = true
		chain = append(chain, child)
		current = child
	}

	return trunk, chain, nil
}

