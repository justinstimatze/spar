// Command spar is an epistemic chaos monkey for code review: it
// occasionally shows a synthetic bug instead of your real diff when
// you run `spar review`, and tracks whether you catch it. It never
// writes to your working tree or git state.
package main

import (
	"fmt"
	"io"
	"os"
	"runtime/debug"
)

// version is "dev" by default and baked at release time via
//
//	go install -ldflags "-X main.version=$(git describe --tags --always --dirty)" ./cmd/spar
//
// The git tag is the single source of truth — there is no hand-maintained
// version constant to drift out of sync. buildVersion() resolves it.
var version = "dev"

// buildVersion reports the binary's version, preferring (in order): a
// release value baked in via -ldflags; the module version when installed
// with `go install …@vX.Y.Z`; the embedded VCS commit (+dirty) for local
// `go build`. Falls back to "dev" when none is available.
func buildVersion() string {
	if version != "dev" {
		return version
	}
	bi, ok := debug.ReadBuildInfo()
	if !ok {
		return version
	}
	if v := bi.Main.Version; v != "" && v != "(devel)" {
		return v
	}
	var rev, dirty string
	for _, s := range bi.Settings {
		switch s.Key {
		case "vcs.revision":
			if len(s.Value) >= 12 {
				rev = s.Value[:12]
			} else {
				rev = s.Value
			}
		case "vcs.modified":
			if s.Value == "true" {
				dirty = "-dirty"
			}
		}
	}
	if rev != "" {
		return rev + dirty
	}
	return version
}

func main() {
	if len(os.Args) < 2 {
		usage(os.Stderr)
		os.Exit(1)
	}
	args := os.Args[1:]
	switch args[0] {
	case "review":
		cmdReview(args[1:])
	case "stats":
		cmdStats(args[1:])
	case "doctor":
		cmdDoctor(args[1:])
	case "live-hook":
		cmdLiveHook(args[1:])
	case "live-hook-commit":
		cmdLiveHookCommit(args[1:])
	case "live-reveal":
		cmdLiveReveal(args[1:])
	case "live-induce":
		cmdLiveInduce(args[1:])
	case "live-fixup":
		cmdLiveFixup(args[1:])
	case "version", "--version", "-v":
		fmt.Println("spar", buildVersion())
	case "help", "--help", "-h":
		usage(os.Stdout)
	default:
		fmt.Fprintf(os.Stderr, "spar: unknown command %q\n\n", args[0])
		usage(os.Stderr)
		os.Exit(1)
	}
}

func usage(w io.Writer) {
	fmt.Fprint(w, `spar — epistemic chaos monkey for code review

Usage:
  spar review [--rate 0.4]      Review the current git diff. Sometimes shows
                                 a synthetic bug instead of the real thing;
                                 tracks whether you catch it. Never writes to
                                 your working tree or git state.
  spar stats [--since 30d] [--project NAME]
                                 Summarize catch rate from the trial log.
  spar doctor                   Check that a key resolves (and from where),
                                 git and delta are on PATH, and the log
                                 directory is writable.
  spar live-hook                UserPromptSubmit hook entrypoint for live
                                 mode — not meant to be run by hand. See
                                 README's "Live mode" section for what it
                                 does and how to enable it.
  spar live-hook-commit         PreToolUse hook entrypoint for live mode's
                                 commit-time trigger — narrate (default),
                                 notify, or gate, via SPAR_LIVE_COMMIT_MODE.
                                 Not meant to be run by hand. See README's
                                 "Live mode" section.
  spar live-reveal --session ID --token TOK --caught yes|no|partial|unengaged|not_planted
                                 Closes out a live-mode pending plant. Called
                                 by the model, not typically by hand.
  spar live-induce               Force the next prompt to plant, ignoring
                                 cooldown — for testing/dogfooding live mode
                                 without waiting out SPAR_LIVE_COOLDOWN.
  spar live-fixup [--session ID] [--apply] [--force]
                                 Patch a live-mode plant's corrected fact
                                 into a closed session transcript. Manual,
                                 run "once in a while" — never automatic,
                                 never during an open session. No args lists
                                 sessions with something to fix; --session
                                 alone is a dry run; --apply writes, after a
                                 backup. See README's "Live mode" section.
  spar version                  Print version.

Requires ANTHROPIC_API_KEY (shell env, ./.env, or ~/.config/spar/.env) for
injection; without it, spar review always shows the real diff.
`)
}
