package cmd

import (
	"github.com/ryanclark/gh-stack/internal/config"
	"github.com/ryanclark/gh-stack/internal/stack"
	"github.com/spf13/cobra"
)

type unstackOptions struct {
	target string
}

func UnstackCmd(cfg *config.Config) *cobra.Command {
	opts := &unstackOptions{}

	cmd := &cobra.Command{
		Use:     "unstack [branch]",
		Aliases: []string{"delete"},
		Short:   "Remove a stack from local tracking",
		Long:    "Remove a stack from local tracking.",
		Args:    cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) > 0 {
				opts.target = args[0]
			}
			return runUnstack(cfg, opts)
		},
	}

	return cmd
}

func runUnstack(cfg *config.Config, opts *unstackOptions) error {
	result, err := loadStack(cfg, opts.target)
	if err != nil {
		return ErrNotInStack
	}
	gitDir := result.GitDir
	sf := result.StackFile
	s := result.Stack

	// Remove the exact resolved stack from local tracking by pointer identity,
	// not by branch name — avoids removing the wrong stack when a trunk
	// branch is shared across multiple stacks.
	for i := range sf.Stacks {
		if &sf.Stacks[i] == s {
			sf.RemoveStack(i)
			break
		}
	}
	if err := stack.Save(gitDir, sf); err != nil {
		return handleSaveError(cfg, err)
	}
	cfg.Successf("Stack removed from local tracking")

	return nil
}
