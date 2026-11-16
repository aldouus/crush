package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/spf13/cobra"
	"github.com/taigrr/crush/internal/session"
)

var (
	sessionArchiveJSON   bool
	sessionUnarchiveJSON bool
)

var sessionArchiveCmd = &cobra.Command{
	Use:   "archive <id...>",
	Short: "Archive sessions",
	Long:  "Archive one or more active sessions, hiding them from the session list. Use --json for machine-readable output. Each ID can be a UUID, UUID prefix, full hash, or hash prefix.",
	Args:  cobra.MinimumNArgs(1),
	RunE:  runSessionArchive,
}

var sessionUnarchiveCmd = &cobra.Command{
	Use:   "unarchive <id...>",
	Short: "Unarchive sessions",
	Long:  "Restore one or more archived sessions to the session list. Use --json for machine-readable output. Each ID can be a UUID, UUID prefix, full hash, or hash prefix of an archived session (see 'crush session list --archived').",
	Args:  cobra.MinimumNArgs(1),
	RunE:  runSessionUnarchive,
}

func init() {
	sessionArchiveCmd.Flags().BoolVar(&sessionArchiveJSON, "json", false, "output in JSON format")
	sessionUnarchiveCmd.Flags().BoolVar(&sessionUnarchiveJSON, "json", false, "output in JSON format")
	sessionCmd.AddCommand(sessionArchiveCmd)
	sessionCmd.AddCommand(sessionUnarchiveCmd)
}

func runSessionArchive(cmd *cobra.Command, args []string) error {
	return runSessionArchiveOp(cmd, args, sessionArchiveJSON, true)
}

func runSessionUnarchive(cmd *cobra.Command, args []string) error {
	return runSessionArchiveOp(cmd, args, sessionUnarchiveJSON, false)
}

// runSessionArchiveOp archives (archive=true) or unarchives every session
// named in refs. It keeps going past per-session failures and returns them
// joined once all refs have been processed.
//
// Archiving from the CLI writes the archive flag directly; snapshot refs
// are not pruned (same as archiving in a detached workspace).
func runSessionArchiveOp(cmd *cobra.Command, refs []string, asJSON, archive bool) error {
	ctx, svc, cleanup, err := sessionSetup(cmd)
	if err != nil {
		return err
	}
	defer cleanup()

	results, errs := archiveSessions(ctx, svc.sessions, refs, archive)

	out := cmd.OutOrStdout()
	if asJSON {
		enc := json.NewEncoder(out)
		enc.SetEscapeHTML(false)
		if err := enc.Encode(results); err != nil {
			return err
		}
	} else {
		verb := "Unarchived"
		if archive {
			verb = "Archived"
		}
		for _, res := range results {
			fmt.Fprintf(out, "%s session %s\n", verb, res.ID[:12])
		}
	}
	return errors.Join(errs...)
}

// archiveSessions resolves and archives or unarchives each ref. Archive
// resolves among active sessions; unarchive among archived ones.
func archiveSessions(ctx context.Context, svc session.Service, refs []string, archive bool) ([]sessionMutationResult, []error) {
	scope, opName := sessionScopeArchived, "unarchive"
	if archive {
		scope, opName = sessionScopeActive, "archive"
	}

	results := make([]sessionMutationResult, 0, len(refs))
	var errs []error
	for _, ref := range refs {
		sess, err := resolveSessionIDIn(ctx, svc, ref, scope)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if archive {
			err = svc.Archive(ctx, sess.ID)
		} else {
			err = svc.Unarchive(ctx, sess.ID)
		}
		if err != nil {
			errs = append(errs, fmt.Errorf("failed to %s session %s: %w", opName, ref, err))
			continue
		}
		results = append(results, sessionMutationResult{
			ID:         session.HashID(sess.ID),
			UUID:       sess.ID,
			Title:      sess.Title,
			Archived:   archive,
			Unarchived: !archive,
		})
	}
	return results, errs
}
