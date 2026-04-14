package github

// ClientOps defines the interface for GitHub API operations.
// The concrete Client type satisfies this interface.
// Tests can substitute a MockClient.
type ClientOps interface {
	FindPRForBranch(branch string) (*PullRequest, error)
	FindAnyPRForBranch(branch string) (*PullRequest, error)
	FindPRByNumber(number int) (*PullRequest, error)
	FindPRByBaseBranch(base string) (*PullRequest, error)
	FindPRDetailsForBranch(branch string) (*PRDetails, error)
	CreatePR(base, head, title, body string, draft bool) (*PullRequest, error)
	UpdatePRBase(number int, base string) error
}

// Compile-time check that Client satisfies ClientOps.
var _ ClientOps = (*Client)(nil)
