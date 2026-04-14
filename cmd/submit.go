package cmd

import (
	"errors"
	"fmt"
	"strings"

	"github.com/cli/go-gh/v2/pkg/prompter"
	"github.com/ryanclark/gh-stack/internal/config"
	"github.com/ryanclark/gh-stack/internal/git"
	"github.com/ryanclark/gh-stack/internal/stack"
	"github.com/spf13/cobra"
)

type submitOptions struct {
	auto   bool
	draft  bool
	remote string
}

func SubmitCmd(cfg *config.Config) *cobra.Command {
	opts := &submitOptions{}

	cmd := &cobra.Command{
		Use:   "submit",
		Short: "Create a stack of PRs on GitHub",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSubmit(cfg, opts)
		},
	}

	cmd.Flags().BoolVar(&opts.auto, "auto", false, "Use auto-generated PR titles without prompting")
	cmd.Flags().BoolVar(&opts.draft, "draft", false, "Create PRs as drafts")
	cmd.Flags().StringVar(&opts.remote, "remote", "", "Remote to push to (defaults to auto-detected remote)")

	return cmd
}

func runSubmit(cfg *config.Config, opts *submitOptions) error {
	gitDir, err := git.GitDir()
	if err != nil {
		cfg.Errorf("not a git repository")
		return ErrNotInStack
	}

	sf, err := stack.Load(gitDir)
	if err != nil {
		cfg.Errorf("failed to load stack state: %s", err)
		return ErrNotInStack
	}

	currentBranch, err := git.CurrentBranch()
	if err != nil {
		cfg.Errorf("failed to get current branch: %s", err)
		return ErrNotInStack
	}

	// Find the stack for the current branch without switching branches.
	// Submit should never change the user's checked-out branch.
	stacks := sf.FindAllStacksForBranch(currentBranch)
	if len(stacks) == 0 {
		cfg.Errorf("current branch %q is not part of a stack", currentBranch)
		return ErrNotInStack
	}
	if len(stacks) > 1 {
		cfg.Errorf("branch %q belongs to multiple stacks; checkout a non-trunk branch first", currentBranch)
		return ErrDisambiguate
	}
	s := stacks[0]

	client, err := cfg.GitHubClient()
	if err != nil {
		cfg.Errorf("failed to create GitHub client: %s", err)
		return ErrAPIFailure
	}

	// Sync PR state to detect merged/queued PRs before pushing.
	syncStackPRs(cfg, s)

	// Push all active branches atomically
	remote, err := pickRemote(cfg, currentBranch, opts.remote)
	if err != nil {
		if !errors.Is(err, errInterrupt) {
			cfg.Errorf("%s", err)
		}
		return ErrSilent
	}
	merged := s.MergedBranches()
	if len(merged) > 0 {
		cfg.Printf("Skipping %d merged %s", len(merged), plural(len(merged), "branch", "branches"))
	}
	queued := s.QueuedBranches()
	if len(queued) > 0 {
		cfg.Printf("Skipping %d queued %s", len(queued), plural(len(queued), "branch", "branches"))
	}
	activeBranches := activeBranchNames(s)
	if len(activeBranches) == 0 {
		cfg.Printf("All branches are merged or queued, nothing to submit")
		return nil
	}
	cfg.Printf("Pushing %d %s to %s...", len(activeBranches), plural(len(activeBranches), "branch", "branches"), remote)
	if err := git.Push(remote, activeBranches, true, true); err != nil {
		cfg.Errorf("failed to push: %s", err)
		return ErrSilent
	}

	// Create or update PRs — ensure every active branch has a PR with the
	// correct base branch. This makes submit idempotent: running it again
	// fills gaps and fixes base branches before syncing the stack.
	for i, b := range s.Branches {
		if s.Branches[i].IsMerged() || s.Branches[i].IsQueued() {
			continue
		}
		baseBranch := s.ActiveBaseBranch(b.Branch)

		pr, err := client.FindPRForBranch(b.Branch)
		if err != nil {
			cfg.Warningf("failed to check PR for %s: %v", b.Branch, err)
			continue
		}

		if pr == nil {
			// Create new PR — auto-generate title from commits/branch name,
			// then prompt interactively unless --auto or non-interactive.
			baseBranchForDiff := s.ActiveBaseBranch(b.Branch)
			title, commitBody := defaultPRTitleBody(baseBranchForDiff, b.Branch)
			originalTitle := title
			if !opts.auto && cfg.IsInteractive() {
				p := prompter.New(cfg.In, cfg.Out, cfg.Err)
				input, err := p.Input(fmt.Sprintf("Title for PR (branch %s):", b.Branch), title)
				if err != nil {
					if isInterruptError(err) {
						printInterrupt(cfg)
						return ErrSilent
					}
					// Non-interrupt error: keep the auto-generated title.
				} else if input != "" {
					title = input
				}
			}

			// If the user changed the title and the commit had a multi-line
			// message, put the full commit message in the PR body so no
			// content is lost.
			prBody := commitBody
			if title != originalTitle && commitBody != "" {
				prBody = originalTitle + "\n\n" + commitBody
			}
			body := generatePRBody(prBody, parentPRNumber(s, i))

			newPR, createErr := client.CreatePR(baseBranch, b.Branch, title, body, opts.draft)
			if createErr != nil {
				cfg.Warningf("failed to create PR for %s: %v", b.Branch, createErr)
				continue
			}
			cfg.Successf("Created PR %s for %s", cfg.PRLink(newPR.Number, newPR.URL), b.Branch)
			s.Branches[i].PullRequest = &stack.PullRequestRef{
				Number: newPR.Number,
				ID:     newPR.ID,
				URL:    newPR.URL,
			}
		} else {
			// PR already exists — record it and fix base branch if needed.
			if s.Branches[i].PullRequest == nil {
				s.Branches[i].PullRequest = &stack.PullRequestRef{
					Number: pr.Number,
					ID:     pr.ID,
					URL:    pr.URL,
				}
			}

			if pr.BaseRefName != baseBranch {
				if err := client.UpdatePRBase(pr.Number, baseBranch); err != nil {
					cfg.Warningf("failed to update base branch for PR %s: %v",
						cfg.PRLink(pr.Number, pr.URL), err)
				} else {
					cfg.Successf("Updated base branch for PR %s to %s",
						cfg.PRLink(pr.Number, pr.URL), baseBranch)
				}
			} else {
				cfg.Printf("PR %s for %s is up to date", cfg.PRLink(pr.Number, pr.URL), b.Branch)
			}
		}
	}

	// Update base commit hashes and sync PR state
	updateBaseSHAs(s)
	syncStackPRs(cfg, s)

	if err := stack.Save(gitDir, sf); err != nil {
		return handleSaveError(cfg, err)
	}

	cfg.Successf("Pushed and synced %d branches", len(s.ActiveBranches()))
	return nil
}

// defaultPRTitleBody generates a PR title and body from the branch's commits.
// If there is exactly one commit, use its subject as the title and its body
// (if any) as the PR body. Otherwise, humanize the branch name for the title.
func defaultPRTitleBody(base, head string) (string, string) {
	commits, err := git.LogRange(base, head)
	if err == nil && len(commits) == 1 {
		return commits[0].Subject, strings.TrimSpace(commits[0].Body)
	}
	return humanize(head), ""
}

// generatePRBody returns the PR body, prepending a "Requires #N" line
// if this PR depends on another PR in the stack.
func generatePRBody(commitBody string, dependsOn int) string {
	var parts []string
	if dependsOn > 0 {
		parts = append(parts, fmt.Sprintf("Requires #%d", dependsOn))
	}
	if commitBody != "" {
		parts = append(parts, commitBody)
	}
	return strings.Join(parts, "\n\n")
}

// parentPRNumber returns the PR number of the nearest non-merged ancestor
// branch in the stack, or 0 if there is none (i.e. this is the bottom branch).
func parentPRNumber(s *stack.Stack, branchIndex int) int {
	for j := branchIndex - 1; j >= 0; j-- {
		if s.Branches[j].IsMerged() {
			continue
		}
		if s.Branches[j].PullRequest != nil {
			return s.Branches[j].PullRequest.Number
		}
		return 0
	}
	return 0
}

// humanize replaces hyphens and underscores with spaces.
func humanize(s string) string {
	return strings.Map(func(r rune) rune {
		if r == '-' || r == '_' {
			return ' '
		}
		return r
	}, s)
}

