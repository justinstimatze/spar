# spar

Occasionally, spar shows you something that isn't there: a synthetic,
subtly wrong version of a diff you're reviewing (`spar review`), or a
plausible-but-wrong claim slipped into an AI assistant's own reply (live
mode). You engage with it like you would the real thing. Then it tells you
whether you caught it.

The premise: review against AI-generated output tends to drift into
rubber-stamping — the failure mode any large-volume review task eventually hits
once pattern-matching quietly replaces reading. spar is a standing way to check
whether you're still reading, without waiting for a real bug to prove it the
hard way.

## How it works

1. `spar review`, run inside a git repo with uncommitted changes, captures the real
   diff (staged first, else unstaged).
2. It coin-flips, at a configurable rate, whether this trial injects a bug.
3. If injecting: it picks one eligible changed file, skipping binary, deleted,
   or renamed files and anything over a size cap (1,500 lines or 60KB). It
   asks Claude to produce a subtly mutated version of that file's content,
   scoped to the lines you actually touched, in a category spar's own
   random-number generator picked — a code taxonomy (off-by-one, wrong
   operator, dropped nil check, and so on) for source files, a parallel prose
   taxonomy (flipped recommendation, misattributed tradeoff, unsupported
   claim, and so on) for markdown and plaintext, so reviewing an ADR gets a
   plausible ADR-shaped error, not a code bug wearing prose. It then
   regenerates a real, valid diff from that locally — the model never
   hand-writes diff syntax; spar always re-derives it via `git diff
   --no-index`, so a malformed diff is never a tell.
4. You review it exactly like a normal diff — Enter to approve, `f` to flag a
   concern.
5. spar reveals the ground truth immediately: injected or clean, and if
   injected, what and why.
6. The trial is logged to `~/.claude/spar/log.jsonl` — the only disk write
   spar makes. Your working tree and git state never change, regardless of
   outcome.

`spar stats` reads the log and reports your catch rate.

## Install

```sh
git clone https://github.com/justinstimatze/spar
cd spar
make install
```

Needs Go 1.25+ and, optionally, [`delta`][delta] on `PATH` for
syntax-highlighted diff rendering — spar falls back to plain text without
it. `make install` runs `go install`, so the binary lands at `$GOBIN` or
`$GOPATH/bin` (typically `~/go/bin`) — make sure that's on your `PATH`.

Injection needs `ANTHROPIC_API_KEY` (see Configuration below) and costs one
API call per trial that attempts injection, sending that one file's real
content — see [SECURITY.md](SECURITY.md) for exactly what does and doesn't
leave your machine. Without a key, spar still runs fine; every trial just
falls back to showing the real diff.

## Usage

```sh
spar doctor               # run this first — key, git, delta, log dir
spar review              # rate defaults to 0.4, or SPAR_INJECT_RATE
spar review --rate 0.6
spar stats
spar stats --since 30d --project myrepo   # --project optional, auto-detected
spar stats --trend         # catch rate by week and by category
```

Live mode adds `spar live-hook`, `spar live-hook-commit`, `spar
live-reveal`, `spar live-induce`, and `spar live-fixup` — see "Live mode"
below. The first three are invoked by Claude Code's hook system, not
something you run by hand; the last two are manual, run-when-you-want-them
commands.

## Configuration

`ANTHROPIC_API_KEY` — required for injection, resolved from, in order: the
shell environment, `.env` walked up from the current directory,
`~/.config/spar/.env`. Without it, every attempted trial falls back to
showing the real diff, logged with `no api key` as the reason — see
[SECURITY.md](SECURITY.md) for exactly what does and doesn't leave your
machine.

`SPAR_INJECT_RATE` — default injection probability (0–1), overridden
per-invocation by `--rate`. Default `0.4`.

`SPAR_LIVE_ENABLED`, `SPAR_LIVE_COOLDOWN`, `SPAR_LIVE_PENDING_TTL`,
`SPAR_LIVE_REVEAL_MODE`, `SPAR_LIVE_STATS_INTERVAL` — live mode's own env
vars; see "Live mode" below for what each does and their defaults.

## Safety invariant

spar reads a diff as text and, at most, shows you a locally-generated variant of
that text. It never touches your working tree, your index, or any git ref, under
any outcome — a run against real, uncommitted work never risks a synthetic bug
landing anywhere but your terminal.

This covers git state only, not the network: a trial that attempts injection
sends one file's content to the Anthropic API first — see
[SECURITY.md](SECURITY.md) for exactly what that does and doesn't include.
The mutated candidate is diffed against the original via two temp files
(mode `0600`) under `$TMPDIR`, removed as soon as the diff is regenerated.

## Live mode

`spar review` only helps if you actually read diffs. If you don't — if what
you actually read is Claude's own chat replies and plan text — then review
mode is testing a muscle you don't use. Live mode plants the same kind of
error directly into a real end-of-turn reply instead: a conceptual,
architectural, or procedural claim that's subtly wrong, with no warning, in
the text you were going to read anyway.

**This is a materially different safety posture than review mode — read this
before enabling it.** Review mode never writes to your repo regardless of
outcome; nothing it shows you can be acted on by mistake, because it's a
read-only render of a diff. Live mode has no such sandbox: a planted claim
lives in a real conversation and could inform a real decision before you
catch it. The category taxonomy (`internal/livetaxonomy`) is written to stay
out of that territory — no fabricated test/verification/security claims, no
invented numbers, nothing you'd act on directly — but there's no independent
check enforcing that boundary the way `spar review`'s diff validation does.
The instruction text is the whole safety mechanism.

That claim also becomes a permanent part of your actual Claude Code
transcript, not just something shown on screen — it's the model's own real
reply, and Claude Code persists every reply the same way. `spar live-hook`
never writes to that file itself; it only reads it, once, for the
reveal-time corroboration check below. Because nothing automatic ever edits
that file after the fact, the reveal always restates the correct fact
plainly rather than only naming that something was wrong — and if you want
the transcript itself corrected too, `spar live-fixup` is a separate,
manually-invoked command for exactly that, described below.

How it works: a `spar live-hook` UserPromptSubmit hook — Claude Code's
mechanism for running a command before each prompt reaches the model — on a
cooldown (`SPAR_LIVE_COOLDOWN`, default 45m), tells the model to weave one
such claim into an otherwise fully correct reply, without flagging it. By
default, the reveal never asks you anything: asking hands you a fresh,
explicit invitation to go re-examine that reply, which is a different
signal than whether you noticed it during ordinary reading. Instead, on
your very next turn, spar tells the model to classify what you actually
did — did your message show you noticed or corrected the claim, did it
show you proceeded as if the claim were true, or does nothing about it
bear on the claim either way — log that classification via
`spar live-reveal`, then disclose what was planted, and the correct fact
in its place, as a plain statement, not a question, before addressing
whatever you actually asked for.

Prefer being asked directly instead? Set `SPAR_LIVE_REVEAL_MODE=ask` for
the earlier two-turn flow: your next turn gets only the question — nothing
about what was planted, nothing about logging — and only the turn after
that, once your actual answer exists, does spar disclose and log. That
split exists so the `live-reveal` Bash call, whose arguments name what was
planted, can never be composed before a real answer is on the table.

An unrevealed plant (session ended, `/clear`, whatever) expires after
`SPAR_LIVE_PENDING_TTL` (default 8h) and gets logged as unrevealed rather
than silently dropped.

**Off by default, and not something `make install` turns on.** Enable it per
project by adding a hook entry to that project's own
`.claude/settings.local.json` (never the shared `settings.json` — this
should never turn on for someone who just clones a repo) and setting
`SPAR_LIVE_ENABLED=1` in your shell:

```json
{
  "hooks": {
    "UserPromptSubmit": [
      { "hooks": [
        { "type": "command", "command": "spar live-hook", "timeout": 10 }
      ] }
    ]
  }
}
```

Merge that into your existing `settings.local.json` rather than replacing
it. `SPAR_LIVE_COOLDOWN` and `SPAR_LIVE_PENDING_TTL` accept Go duration
strings (`45m`, `8h`) and default as noted above.

Waiting out the cooldown to see it fire is slow. `spar live-induce` forces
the very next prompt to plant, cooldown ignored, for testing — no effect if
a plant is already pending reveal for the session that prompts next.

### Narrating a commit: `spar live-hook-commit`

`spar review` needs you to remember to run it. `spar live-hook-commit` is
the same idea as the hook above, but triggered by a `git commit` instead of
a chat turn: when Claude is about to commit, it plants one subtly wrong
fact into how it narrates that commit afterward — never touching the
commit's actual content, only the description of it. Enable it alongside
`spar live-hook` with a second block in the same `settings.local.json`:

```json
{
  "hooks": {
    "PreToolUse": [
      { "matcher": "Bash", "hooks": [
        { "type": "command", "command": "spar live-hook-commit", "timeout": 10, "if": "Bash(git commit *)" }
      ] }
    ]
  }
}
```

It shares `spar live-hook`'s cooldown and pending-plant slot — one plant in
flight per session, regardless of which trigger caused it — and reveals
through the exact same `spar live-reveal` flow. `spar live-induce` only
forces the chat trigger; it has no effect on this one, deliberately —
forcing a plant on your very next commit would test something you didn't
ask it to.

The `if` filter is best-effort text matching, not a real shell parse: `git
-C ../other commit` won't match (a known, accepted gap — not fixed, since a
broader pattern risks false positives), and a Bash command containing
`$()`, a backtick, or a `$VAR` reference can match even when it isn't a
commit at all — a documented Claude Code behavior, not a bug in spar. `spar
live-hook-commit` guards against that itself before planting anything, and
the model is separately instructed to check the tool result and plant
nothing if the commit failed or the matched command wasn't really a
commit.

### Patching the transcript itself: `spar live-fixup`

The reveal restates the correct fact in a later turn, but it doesn't touch
the earlier turn where the false claim actually lives — `spar live-hook`
never writes to your transcript, only reads it. If you want the historical
record itself corrected, not just a later turn that explains it, run
`spar live-fixup` by hand, whenever you like:

```sh
spar live-fixup                      # lists sessions with something to fix
spar live-fixup --session ID         # dry run: shows what would change
spar live-fixup --session ID --apply # writes, after a backup
```

This is a manual maintenance step, not something live mode ever triggers on
its own — run it between sessions, never during one. Each `--apply` run:
refuses if the transcript was modified in the last 5 minutes (a soft guard
against an accidentally-still-open session; `--force` bypasses it), backs
up the whole file first (`<path>.spar-fixup-backup-<timestamp>`, restore
with `mv` if you ever need to undo a patch), then finds the plant's exact
wording — verified byte-for-byte against the real transcript at reveal
time, never trusted on the model's memory alone — and replaces it with the
corrected fact, and only that one exact spot. If the wording doesn't appear
exactly once, it skips that trial rather than guessing — including the
common case where a clear disclosure quotes the original wording back to
you, which makes the match ambiguous on purpose. `--session` is required to
patch anything; there's no "sweep every session" mode.

At most once every `SPAR_LIVE_STATS_INTERVAL` (default 24h, global across
every project — not per session), spar mentions your current catch rate as
a brief aside and points to `spar stats --trend` for the full breakdown,
rather than reproducing it inline — a nudge toward the CLI, not a
scoreboard in the chat. It never fires on the same turn as a reveal. Set
`SPAR_LIVE_STATS_INTERVAL=0` to disable it.

## What v1 doesn't do

No scoring, streaks, or rewards — just a catch-rate log. Adding a score tied to
catch rate risks training reviewers to exploit how mechanically easy a small,
localized mutation is to patch, rather than rewarding genuine close reading
(a 2026 student study on injected-vs-natural GenAI bugs is the citation
behind this concern — see [docs/DESIGN.md](docs/DESIGN.md) for exactly what
it does and doesn't show). Worth building once there's real baseline data
to design against, not before. Live mode doesn't change this — it widens
which surface gets an injected error, not the incentive model.

No live-embedded hook **in review mode** — `spar review` is still a manual
command run against `git diff`, invoked by hand each time, never intercepting
a real commit or watching a session as it happens. Live mode (above) is the
one exception, opt-in per project — it plants into a chat reply, and, via
`spar live-hook-commit`, into how a commit gets narrated. Neither ever
touches what actually lands in your repo; "intercept" here means "observe
and comment on," not "gate or alter."

## Prior art

spar is a personal-scale instance of a known technique: seed a known error
into real work, keep the ground truth hidden, measure whether it gets
caught. [Weinberg's "bebugging"][bebugging] (1970) proposed exactly this for
keeping programmers — and, per Weinberg, radar operators before them — from
going complacent. [Buçinca, Malaya & Gajos (CSCW 2021)][bucinca] ran the
closest mechanical ancestor: a simulated AI with a controlled, known error
rate, built as a one-off experimental control, never packaged as a standing
tool. The UK's [PERFORMS][performs] breast-screening self-assessment scheme
is the closest real "standing tool" match, running since 1991 — in
radiology, not code review. See [docs/DESIGN.md](docs/DESIGN.md) for the
fuller lineage and the reasoning behind what v1 does and doesn't do.

## Why "spar"

Double meaning: a lustrous mineralogy term (as in "feldspar"), and to
practice-fight — the sparring-partner framing the project started from.

> "If you are desirous to learn, always play with a strong player, rather
> than with an inferior one. A single game with a really good player, at
> such odds as he thinks proper, will be worth much more to you than the
> winning and beating a dozen bad players."
> — Samuel Standidge Boden, *A Popular Introduction to the Study and
> Practice of Chess*, 1851

## License

MIT — see [LICENSE](LICENSE).

[delta]: https://github.com/dandavison/delta
[bebugging]: https://en.wikipedia.org/wiki/Bebugging
[bucinca]: https://arxiv.org/pdf/2102.09692.pdf
[performs]: https://www.performs.org.uk/about/
