package cmd

import (
	"fmt"
	"io"
	"testing"

	"github.com/ryanclark/gh-stack/internal/config"
	"github.com/ryanclark/gh-stack/internal/git"
	"github.com/ryanclark/gh-stack/internal/github"
	"github.com/ryanclark/gh-stack/internal/stack"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGeneratePRBody(t *testing.T) {
	assert.Equal(t, "", generatePRBody("", 0))
	assert.Equal(t, "Some description", generatePRBody("Some description", 0))
	assert.Equal(t, "Requires #42", generatePRBody("", 42))
	assert.Equal(t, "Requires #42\n\nSome description", generatePRBody("Some description", 42))
}

// newSubmitMock creates a MockOps pre-configured for submit tests.
func newSubmitMock(tmpDir string, currentBranch string) *git.MockOps {
	return &git.MockOps{
		GitDirFn:        func() (string, error) { return tmpDir, nil },
		CurrentBranchFn: func() (string, error) { return currentBranch, nil },
		ResolveRemoteFn: func(string) (string, error) { return "origin", nil },
		PushFn:          func(string, []string, bool, bool) error { return nil },
	}
}

func TestSubmit_CreatesPRs(t *testing.T) {
	s := stack.Stack{
		Trunk: stack.BranchRef{Branch: "main"},
		Branches: []stack.BranchRef{
			{Branch: "b1"},
			{Branch: "b2"},
		},
	}

	tmpDir := t.TempDir()
	writeStackFile(t, tmpDir, s)

	var pushCalls []pushCall
	var createdPRs []string

	mock := newSubmitMock(tmpDir, "b1")
	mock.PushFn = func(remote string, branches []string, force, atomic bool) error {
		pushCalls = append(pushCalls, pushCall{remote, branches, force, atomic})
		return nil
	}
	mock.LogRangeFn = func(base, head string) ([]git.CommitInfo, error) {
		return []git.CommitInfo{{Subject: "commit for " + head}}, nil
	}

	restore := git.SetOps(mock)
	defer restore()

	prCounter := 100
	cfg, _, errR := config.NewTestConfig()
	cfg.GitHubClientOverride = &github.MockClient{
		FindPRForBranchFn: func(branch string) (*github.PullRequest, error) {
			return nil, nil // No existing PR
		},
		CreatePRFn: func(base, head, title, body string, draft bool) (*github.PullRequest, error) {
			createdPRs = append(createdPRs, head)
			prCounter++
			return &github.PullRequest{
				Number: prCounter,
				ID:     fmt.Sprintf("PR_%d", prCounter),
				URL:    fmt.Sprintf("https://github.com/owner/repo/pull/%d", prCounter),
			}, nil
		},
	}

	cmd := SubmitCmd(cfg)
	cmd.SetArgs([]string{"--auto"})
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	err := cmd.Execute()

	cfg.Err.Close()
	errOut, _ := io.ReadAll(errR)
	output := string(errOut)

	assert.NoError(t, err)

	// Branches should be pushed
	require.Len(t, pushCalls, 1)
	assert.Equal(t, "origin", pushCalls[0].remote)
	assert.Equal(t, []string{"b1", "b2"}, pushCalls[0].branches)

	// PRs should be created
	assert.Equal(t, []string{"b1", "b2"}, createdPRs)

	assert.Contains(t, output, "Pushed and synced 2 branches")
}

func TestSubmit_PushFailure(t *testing.T) {
	s := stack.Stack{
		Trunk: stack.BranchRef{Branch: "main"},
		Branches: []stack.BranchRef{
			{Branch: "b1"},
		},
	}

	tmpDir := t.TempDir()
	writeStackFile(t, tmpDir, s)

	mock := newSubmitMock(tmpDir, "b1")
	mock.PushFn = func(string, []string, bool, bool) error {
		return fmt.Errorf("remote rejected")
	}

	restore := git.SetOps(mock)
	defer restore()

	cfg, _, errR := config.NewTestConfig()
	cfg.GitHubClientOverride = &github.MockClient{}
	cmd := SubmitCmd(cfg)
	cmd.SetArgs([]string{"--auto"})
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	err := cmd.Execute()

	cfg.Err.Close()
	errOut, _ := io.ReadAll(errR)
	output := string(errOut)

	assert.ErrorIs(t, err, ErrSilent)
	assert.Contains(t, output, "failed to push")
}

func TestSubmit_SkipsMergedBranches(t *testing.T) {
	s := stack.Stack{
		Trunk: stack.BranchRef{Branch: "main"},
		Branches: []stack.BranchRef{
			{Branch: "b1", PullRequest: &stack.PullRequestRef{Number: 1, Merged: true}},
			{Branch: "b2"},
			{Branch: "b3", PullRequest: &stack.PullRequestRef{Number: 3, Merged: true}},
		},
	}

	tmpDir := t.TempDir()
	writeStackFile(t, tmpDir, s)

	var pushCalls []pushCall

	mock := newSubmitMock(tmpDir, "b2")
	mock.PushFn = func(remote string, branches []string, force, atomic bool) error {
		pushCalls = append(pushCalls, pushCall{remote, branches, force, atomic})
		return nil
	}

	restore := git.SetOps(mock)
	defer restore()

	cfg, _, errR := config.NewTestConfig()
	cfg.GitHubClientOverride = &github.MockClient{
		FindPRForBranchFn: func(branch string) (*github.PullRequest, error) {
			return &github.PullRequest{Number: 2, URL: "https://github.com/owner/repo/pull/2"}, nil
		},
	}
	cmd := SubmitCmd(cfg)
	cmd.SetArgs([]string{"--auto"})
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	err := cmd.Execute()

	cfg.Err.Close()
	_, _ = io.ReadAll(errR)

	assert.NoError(t, err)
	require.Len(t, pushCalls, 1)
	assert.Equal(t, []string{"b2"}, pushCalls[0].branches)
}

func TestSubmit_DefaultPRTitleBody(t *testing.T) {
	t.Run("single_commit", func(t *testing.T) {
		restore := git.SetOps(&git.MockOps{
			LogRangeFn: func(base, head string) ([]git.CommitInfo, error) {
				return []git.CommitInfo{
					{Subject: "Add login page", Body: "Implements the OAuth flow"},
				}, nil
			},
		})
		defer restore()

		title, body := defaultPRTitleBody("main", "feat-login")
		assert.Equal(t, "Add login page", title)
		assert.Equal(t, "Implements the OAuth flow", body)
	})

	t.Run("multiple_commits", func(t *testing.T) {
		restore := git.SetOps(&git.MockOps{
			LogRangeFn: func(base, head string) ([]git.CommitInfo, error) {
				return []git.CommitInfo{
					{Subject: "First commit"},
					{Subject: "Second commit"},
				}, nil
			},
		})
		defer restore()

		title, body := defaultPRTitleBody("main", "my-feature")
		assert.Equal(t, "my feature", title)
		assert.Equal(t, "", body)
	})
}

func TestSubmit_Humanize(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"my-branch", "my branch"},
		{"my_branch", "my branch"},
		{"nobranch", "nobranch"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			assert.Equal(t, tt.want, humanize(tt.input))
		})
	}
}

func TestSubmit_UpdatesBaseBranch(t *testing.T) {
	// b1's PR has base "main" which is correct.
	// b2's PR has base "main" but it should be "b1" — needs update.
	s := stack.Stack{
		Trunk: stack.BranchRef{Branch: "main"},
		Branches: []stack.BranchRef{
			{Branch: "b1", PullRequest: &stack.PullRequestRef{Number: 10}},
			{Branch: "b2", PullRequest: &stack.PullRequestRef{Number: 11}},
		},
	}

	tmpDir := t.TempDir()
	writeStackFile(t, tmpDir, s)

	mock := newSubmitMock(tmpDir, "b1")

	restore := git.SetOps(mock)
	defer restore()

	var updatedPRs []struct {
		number int
		base   string
	}

	cfg, _, errR := config.NewTestConfig()
	cfg.GitHubClientOverride = &github.MockClient{
		FindPRForBranchFn: func(branch string) (*github.PullRequest, error) {
			switch branch {
			case "b1":
				return &github.PullRequest{
					Number: 10, ID: "PR_10",
					URL:         "https://github.com/owner/repo/pull/10",
					BaseRefName: "main", HeadRefName: "b1",
				}, nil
			case "b2":
				return &github.PullRequest{
					Number: 11, ID: "PR_11",
					URL:         "https://github.com/owner/repo/pull/11",
					BaseRefName: "main", HeadRefName: "b2", // wrong base
				}, nil
			}
			return nil, nil
		},
		UpdatePRBaseFn: func(number int, base string) error {
			updatedPRs = append(updatedPRs, struct {
				number int
				base   string
			}{number, base})
			return nil
		},
	}

	cmd := SubmitCmd(cfg)
	cmd.SetArgs([]string{"--auto"})
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	err := cmd.Execute()

	cfg.Err.Close()
	errOut, _ := io.ReadAll(errR)
	output := string(errOut)

	assert.NoError(t, err)
	// b1's base is "main" which is correct — no update.
	// b2's base is "main" but should be "b1" — should be updated.
	require.Len(t, updatedPRs, 1)
	assert.Equal(t, 11, updatedPRs[0].number)
	assert.Equal(t, "b1", updatedPRs[0].base)
	assert.Contains(t, output, "Updated base branch for PR")
}

func TestSubmit_CreatesMissingPRsAndUpdatesExisting(t *testing.T) {
	// b1 has a PR, b2 does not, b3 has a PR with wrong base.
	// Submit should create b2's PR and fix b3's base.
	s := stack.Stack{
		Trunk: stack.BranchRef{Branch: "main"},
		Branches: []stack.BranchRef{
			{Branch: "b1", PullRequest: &stack.PullRequestRef{Number: 10}},
			{Branch: "b2"},
			{Branch: "b3", PullRequest: &stack.PullRequestRef{Number: 12}},
		},
	}

	tmpDir := t.TempDir()
	writeStackFile(t, tmpDir, s)

	mock := newSubmitMock(tmpDir, "b1")
	mock.LogRangeFn = func(base, head string) ([]git.CommitInfo, error) {
		return []git.CommitInfo{{Subject: "commit for " + head}}, nil
	}

	restore := git.SetOps(mock)
	defer restore()

	var createdPRs []string
	var updatedBases []struct {
		number int
		base   string
	}

	cfg, _, errR := config.NewTestConfig()
	cfg.GitHubClientOverride = &github.MockClient{
		FindPRForBranchFn: func(branch string) (*github.PullRequest, error) {
			switch branch {
			case "b1":
				return &github.PullRequest{
					Number: 10, ID: "PR_10",
					URL:         "https://github.com/owner/repo/pull/10",
					BaseRefName: "main", HeadRefName: "b1",
				}, nil
			case "b2":
				return nil, nil // no PR
			case "b3":
				return &github.PullRequest{
					Number: 12, ID: "PR_12",
					URL:         "https://github.com/owner/repo/pull/12",
					BaseRefName: "main", HeadRefName: "b3", // wrong base — should be b2
				}, nil
			}
			return nil, nil
		},
		CreatePRFn: func(base, head, title, body string, draft bool) (*github.PullRequest, error) {
			createdPRs = append(createdPRs, head)
			return &github.PullRequest{
				Number: 11, ID: "PR_11",
				URL: "https://github.com/owner/repo/pull/11",
			}, nil
		},
		UpdatePRBaseFn: func(number int, base string) error {
			updatedBases = append(updatedBases, struct {
				number int
				base   string
			}{number, base})
			return nil
		},
	}

	cmd := SubmitCmd(cfg)
	cmd.SetArgs([]string{"--auto"})
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	err := cmd.Execute()

	cfg.Err.Close()
	errOut, _ := io.ReadAll(errR)
	output := string(errOut)

	assert.NoError(t, err)

	// b2 should have been created
	assert.Equal(t, []string{"b2"}, createdPRs)
	assert.Contains(t, output, "Created PR")

	// b3's base should have been updated from "main" to "b2"
	require.Len(t, updatedBases, 1)
	assert.Equal(t, 12, updatedBases[0].number)
	assert.Equal(t, "b2", updatedBases[0].base)
	assert.Contains(t, output, "Updated base branch for PR")
}
