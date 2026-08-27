# Security Policy

If you discover a security vulnerability, please email justin@justinstimatze.com directly rather than opening a public issue or PR.

I'll acknowledge receipt within 7 days and aim to provide an initial assessment within 30 days. We can coordinate on a disclosure timeline — defaulting to 90 days from initial report unless circumstances warrant otherwise.

## What spar sends over the network

`spar review` sends the full content of one file from your diff to the Anthropic API whenever a trial attempts injection — not your whole repo, and not on every run: only on trials where the injection coin flip fires (`--rate` / `SPAR_INJECT_RATE`), and only the one file spar picked. Without `ANTHROPIC_API_KEY` set, spar never makes a network call.

If the model's mutation then fails validation, or the diff can't be regenerated, that trial still falls back to showing your real diff — but the API call already happened. `spar review`'s live output always says so ("injection was attempted but fell back: ..."), and the JSONL log preserves the same distinction via `inject_attempted`, separately from whether the trial displays as injected.

Your API key is read from the environment, `.env`, or `~/.config/spar/.env` — never logged, and never written into `~/.claude/spar/log.jsonl`.

Live mode's chat trigger (`spar live-hook`) and commit-narration trigger (`spar live-hook-commit` in its default `narrate` mode) make no network calls of their own — `spar live-reveal` doesn't either. The reply that plants or discloses a claim comes from whichever Claude session you're already in, not a separate spar-initiated API call.

`spar live-hook-commit`'s `notify` mode is the one exception: it runs the same `internal/inject` pipeline `spar review` uses, headlessly, against your staged diff before a commit — same coin flip (`SPAR_INJECT_RATE`), same one-file-at-a-time content sent to the Anthropic API, same fallback-to-clean behavior on any failure. The only difference from `spar review`'s own call is a tighter latency budget (`inject.HookConfig()`: one HTTP attempt, an 8s timeout, no validation retry) so an automatic hook can't hang a commit for minutes. `gate` mode makes no network call at all — it only ever reads your staged diff locally and shows it to you via Claude Code's own permission prompt.

## What spar keeps locally

Every trial — injected, clean, or fallen-back — writes one line to `~/.claude/spar/log.jsonl`, including the fallback reason when a trial attempted injection but didn't complete. On an API error, that reason can include up to 300 characters of Anthropic's raw error response — never your file content, never your key. Nothing in the log is sent anywhere; it's a local, append-only file you own.

Live mode trials add a few fields to that same log: `session_id`, `live_kind` (which trigger flavor produced the trial — empty for narration, `diff-mutation` for `notify`), and — once revealed — `injected_description` (the model's own account of what it planted for `narrate` trials; spar's own exact record for `notify` trials, when the API response actually returned one — falls back to the model's account on the rare empty response, same as `narrate`) and `user_flag_text` (a paraphrase of the behavior grounding the reveal classification). A `notify`-mode trial additionally logs `injected_file`, `injected_severity`, and `diff_hash` — spar's own ground truth from its `internal/inject` call, the same fields `spar review` already logs for its own trials. Everything here stays local — none of it leaves your machine.

`gate` mode writes nothing to `log.jsonl` at all — no trial, no session id, nothing. It's unscored by design (see README's "Live mode" section), so there's genuinely nothing to log.

The planted reply is also a permanent part of your actual Claude Code transcript, the same as any other reply — Claude Code writes it there, not spar. `spar live-hook` opens that transcript file exactly once, read-only, for the reveal-time corroboration check (`internal/transcript`); it never writes to it. Because nothing automatic ever edits that file after the fact, the reveal is instructed to always restate the correct fact plainly, not just name that something was wrong — see "Live mode" in the README for what that instruction requires.

`spar live-fixup` is the one exception, and it's manual — a person runs it by hand, never triggered by a hook, never during an open session. Before writing anything it: refuses if the target transcript's mtime is within the last 5 minutes unless `--force` (a soft guard against an accidentally-still-open session, not a real liveness check); copies the whole file to a sibling `<path>.spar-fixup-backup-<timestamp>` first, so a bad patch is always reversible with `mv`; and only ever replaces text it finds as an exact, single, unambiguous match inside a genuine assistant reply — never inside a tool call, a tool result, or a harness-composed record, and never a guess when the text appears more than once. `--session` is required; there's no mode that touches every session's transcript at once. What it treats as "the plant's real wording" was itself verified byte-for-byte against the live transcript at reveal time (`cmd_live_reveal.go`), not trusted on the model's memory of its own earlier reply.

Thanks for helping keep this project and its users safe.
