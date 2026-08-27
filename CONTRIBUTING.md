# Contributing

spar is a small personal project maintained by [@justinstimatze](https://github.com/justinstimatze). PRs and issues welcome.

## Ground rules

- **Go stdlib only.** No third-party dependencies — the Anthropic API client is a raw `net/http` call, no SDK in the dependency graph. If you think spar needs one, open an issue first.
- **The model never hand-writes diff syntax.** Injection always returns full mutated file content; spar regenerates the actual diff locally via `git diff --no-index`. Any change to `internal/gitdiff` needs to preserve that — a malformed diff is an instant tell and defeats the point.
- **spar never writes to the working tree or git state.** This is the safety invariant that makes it reasonable to run against real, uncommitted work. Any change that touches `internal/gitdiff` or `cmd/spar/cmd_review.go` needs to keep it true under every outcome, including error paths.
- **Injection categories are RNG-requested.** spar's RNG picks which category to request per trial, and the model is told to use it or substitute the nearest applicable one, saying which it used. The logged category is the model's self-report rather than an enforced value — the requested distribution is auditable; the logged one is trusted self-reporting.
- **Live mode categories stay out of actionable territory.** `internal/livetaxonomy`'s categories must never produce a claim about tests passing, security, safety, or anything else the user might act on directly before it's caught. That boundary is the mechanism's entire safety argument — unlike `spar review`, there's no independent validator checking a live-mode claim the way diff regeneration checks an injected diff, so a new category holding to it is on the category's own wording, not on a downstream check catching a slip.

## Dev setup

```sh
git clone https://github.com/justinstimatze/spar
cd spar
go test ./...
go build ./cmd/spar
```

Runs on the Go version pinned in `go.mod`. Pure-stdlib; no external tools needed beyond `go` and, optionally, `delta` for diff rendering.

## Tests

Pure logic (diff parsing, mutation validation, category rotation) is tested against synthetic fixtures. `internal/gitdiff` and `internal/inject`'s git-shelling functions (`Capture`, `FileContent`, `RegenerateFileDiff`, ...) are tested against real scratch repos in `*_live_test.go` files, deliberately — a fixture-only suite can't catch a mismatch with what git actually does, and that's exactly how the added-file diff header bug slipped through undetected. Nothing in the permanent suite makes a live call to the Anthropic API; `internal/inject/api.go`'s `callAPI` stays untested for that reason.

Before submitting a PR:

```sh
go test ./...
go vet ./...
go build ./cmd/spar
```

## Commit style

Short imperative subject lines. Body explains why, not what.

## Release process (maintainer)

1. Update `CHANGELOG.md`.
2. Tag `vX.Y.Z` on `main`.
3. `make install` picks up the tag automatically via `git describe`.
