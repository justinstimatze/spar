package inject

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// mutateResult is the shape the injector model returns via the forced
// return_mutated_file tool call.
type mutateResult struct {
	MutatedContent string `json:"mutated_content"`
	Category       string `json:"category"`
	Description    string `json:"description"`
	Severity       string `json:"severity"`
}

// injectPromptIntro and injectPromptClosing are shared between the code
// and prose variants of the system prompt; only the hard rules' item 4
// and the worked-examples block are kind-specific — see codeExamples,
// proseExamples, and systemPromptFor below.
//
// Each composed prompt is stable across every call of that kind (only
// the user message varies per trial), so it carries the ephemeral
// cache_control marker. The worked examples aren't padding for its own
// sake — they're the direct fix for a real risk the design doc flags:
// an injector that defaults to obvious, broken "bugs" makes the mutation
// mechanically easy to patch on sight rather than something that actually
// needs a close read (Pădurean et al., ICER 2026, arXiv:2607.05068 — see
// docs/DESIGN.md for exactly what that study does and doesn't show). If a
// prompt happens to clear Sonnet 5's 1024-token minimum
// cacheable prefix, caching is a bonus; if it doesn't, the marker is
// simply a no-op — no error, just no cache credit.
const injectPromptIntro = `You are the injector half of "spar," a personal review training tool built by its own operator, for their own use, with their full knowledge and consent. Your job: given one real file from their own project, return an almost-identical copy with exactly one subtle, realistic error planted in it, so they can practice catching errors in review before they see the real, unmodified version and log whether they caught it.

Hard rules:
1. Return the file's ENTIRE content, byte-for-byte identical to what you were given, except for the single injected change.
2. The change must fall within the line range the request specifies — never touch a line outside it.
3. Touch as few lines as possible — usually one, rarely more than three. A single wrong token, operator, condition, boundary, claim, or value is the target, not a rewritten block or section.
`

const injectPromptRule4Code = `4. The bug must be REALISTIC — the kind of mistake an experienced engineer actually makes under time pressure, that another engineer could plausibly wave through on a normal read. It must not be a syntax error, must not obviously fail to compile or parse, and must not be a nonsensical value nobody would ever write.
`

const injectPromptRule4Prose = `4. The error must be REALISTIC — the kind of mistake a careful writer makes under time pressure, that another reviewer could plausibly wave through on a normal read. It must not break the document's own structure or formatting, must not be a claim so absurd it's obviously wrong on sight, and must not introduce a claim nobody would ever plausibly write.
`

const injectPromptRulesRest = `5. Never add a comment hinting at the error. Never touch formatting, whitespace, or any content outside your one target change.
6. Report: the category you actually used (the requested one, or — if it genuinely doesn't fit this content — the nearest applicable one, and say so in the description), a one-sentence description of exactly what changed and why it's wrong, and a rough severity (low/medium/high).
7. Call return_mutated_file exactly once with your answer. No other text.
`

const codeExamples = `What "realistic and subtle" looks like, by category — GOOD (use these as your bar) vs BAD (never do this):

off-by-one — GOOD: a loop bound off by one character, like using <= where < was correct. BAD: a bound so far off it's obviously broken at a glance.

swapped-argument-order — GOOD: two same-typed, similarly-named parameters passed in the wrong order at a call site. BAD: passing a value of the wrong type, which is a compile error, not a subtle logic bug.

inverted-boolean — GOOD: one flipped negation in a compound condition. BAD: replacing an entire condition with a constant.

dropped-nil-check — GOOD: removing one error/nil check right after a call that can legitimately fail, so a zero value silently flows forward. BAD: removing every error check in the file.

wrong-operator — GOOD: a boundary comparison shifted by one operator (<= vs <) at a point where it changes behavior. BAD: swapping + for / or similar, which usually produces an immediately, obviously broken result.

stale-reference — GOOD: a copy-pasted usage a few lines from a rename that still refers to the old-but-same-typed value, so it still compiles. BAD: referencing an identifier that doesn't exist at all.

wrong-config-value — GOOD: a timeout, retry count, or threshold shifted by a plausible amount. BAD: a value set to something wildly implausible.

incorrect-default-value — GOOD: a default that's subtly wrong for the common case. BAD: a default that's obviously absurd on its face.
`

const proseExamples = `What "realistic and subtle" looks like, by category — GOOD (use these as your bar) vs BAD (never do this):

flipped-recommendation — GOOD: the decision line states the opposite of what the reasoning just above it actually supports — one word or clause changed, reading confident and consistent unless checked against that reasoning. BAD: a recommendation that contradicts itself within the same sentence.

misattributed-tradeoff — GOOD: a real pro or con of the system gets filed under the wrong option's list — the property is true, just credited to the wrong side of the decision. BAD: a made-up tradeoff that isn't true of either option.

inverted-consequence — GOOD: an "if X then Y" becomes "if X then not-Y" in one clause, changing what the decision implies without changing how the sentence reads. BAD: a consequence that's absurd on its face and unrelated to X.

dropped-caveat — GOOD: a caveat or exception stated earlier goes quietly missing from a later summary of the same decision, so the summary overclaims. BAD: the entire rationale section is missing.

misstated-comparison — GOOD: "faster than" becomes "slower than," or a threshold direction flips ("above X" becomes "below X") in one spot while the rest of the document still assumes the original direction. BAD: a comparison so wrong it contradicts the very next sentence.

stale-reference — GOOD: a reference to an option or decision that was superseded earlier in the same document, left unedited after a later revision. BAD: referencing something never mentioned anywhere in the document.

wrong-constraint-value — GOOD: a stated deadline, budget, or limit shifted by a plausible amount. BAD: a constraint value that's wildly implausible on its face.

unsupported-claim — GOOD: a claim stated as settled fact that the document's own evidence doesn't actually establish — a real but weaker premise, subtly overclaimed. BAD: a claim that contradicts something stated elsewhere in the same document.
`

const injectPromptClosing = `
This is a legitimate, fully consented use: the person who will review this content built this exact tool for themselves, specifically to keep their own error-catching skills sharp, and will see the real unmodified file revealed to them immediately after they respond either way.`

// systemPromptFor composes the full system prompt for one content kind
// ("code" or "prose") — see contentKind in inject.go for how a file's
// kind is decided.
func systemPromptFor(kind string) string {
	rule4 := injectPromptRule4Code
	examples := codeExamples
	if kind == "prose" {
		rule4 = injectPromptRule4Prose
		examples = proseExamples
	}
	return injectPromptIntro + rule4 + injectPromptRulesRest + "\n" + examples + injectPromptClosing
}

func buildUserContent(path, after, hunkContext, category string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "File: %s\nCategory to use: %s\n\n", path, category)
	fmt.Fprintf(&b, "The reviewer's own real diff already changed these line ranges (1-indexed, inclusive) — your injected bug MUST land inside one of these ranges:\n%s\n\n", hunkContext)
	b.WriteString("Full current file content, with line numbers added for your reference only — do not include line numbers in your answer, return plain file content:\n\n")
	for i, line := range strings.Split(after, "\n") {
		fmt.Fprintf(&b, "%5d| %s\n", i+1, line)
	}
	return b.String()
}

func callAPI(cfg Config, path, before, after, hunkContext, category, kind string) (mutateResult, error) {
	_ = before // before is not sent to the model; only the after-content and target ranges are

	body := map[string]any{
		"model":      cfg.Model,
		"max_tokens": 24000,
		"system": []any{map[string]any{
			"type":          "text",
			"text":          systemPromptFor(kind),
			"cache_control": map[string]any{"type": "ephemeral"},
		}},
		"messages": []any{map[string]any{
			"role":    "user",
			"content": buildUserContent(path, after, hunkContext, category),
		}},
		"tools": []any{map[string]any{
			"name":        "return_mutated_file",
			"description": "Return the file with exactly one subtle bug injected.",
			"input_schema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"mutated_content": map[string]any{"type": "string"},
					"category":        map[string]any{"type": "string"},
					"description":     map[string]any{"type": "string"},
					"severity":        map[string]any{"type": "string"},
				},
				"required": []any{"mutated_content", "category", "description", "severity"},
			},
		}},
		"tool_choice": map[string]any{"type": "tool", "name": "return_mutated_file"},
	}
	reqBody, err := json.Marshal(body)
	if err != nil {
		return mutateResult{}, err
	}

	client := &http.Client{Timeout: cfg.HTTPTimeout}
	maxAttempts := cfg.MaxHTTPAttempts
	if maxAttempts <= 0 {
		maxAttempts = 1
	}
	var lastReason string
	for attempt := 0; attempt < maxAttempts; attempt++ {
		if attempt > 0 {
			time.Sleep(time.Duration(1<<uint(attempt-1)) * time.Second)
		}
		req, err := http.NewRequest("POST", "https://api.anthropic.com/v1/messages", bytes.NewReader(reqBody))
		if err != nil {
			return mutateResult{}, fmt.Errorf("request build failed: %w", err)
		}
		req.Header.Set("x-api-key", cfg.APIKey)
		req.Header.Set("anthropic-version", "2023-06-01")
		req.Header.Set("content-type", "application/json")

		resp, err := client.Do(req)
		if err != nil {
			lastReason = "http: " + err.Error()
			continue
		}
		respBody, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()

		if resp.StatusCode == 429 || resp.StatusCode == 529 || (resp.StatusCode >= 500 && resp.StatusCode < 600) {
			lastReason = fmt.Sprintf("http %d (retrying)", resp.StatusCode)
			continue
		}
		if resp.StatusCode != 200 {
			return mutateResult{}, fmt.Errorf("http %d: %s", resp.StatusCode, truncate(string(respBody), 300))
		}

		var r struct {
			Content []struct {
				Type  string          `json:"type"`
				Name  string          `json:"name"`
				Input json.RawMessage `json:"input"`
			} `json:"content"`
		}
		if err := json.Unmarshal(respBody, &r); err != nil {
			return mutateResult{}, fmt.Errorf("decode response: %w", err)
		}
		for _, c := range r.Content {
			if c.Type == "tool_use" && c.Name == "return_mutated_file" {
				var mr mutateResult
				if err := json.Unmarshal(c.Input, &mr); err != nil {
					return mutateResult{}, fmt.Errorf("decode tool input: %w", err)
				}
				return mr, nil
			}
		}
		return mutateResult{}, fmt.Errorf("no return_mutated_file tool_use in response")
	}
	return mutateResult{}, fmt.Errorf("retries exhausted: %s", lastReason)
}

func truncate(s string, max int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) > max {
		return s[:max] + "…"
	}
	return s
}

// loadAPIKey resolves the key from, in order: the ANTHROPIC_API_KEY
// env var, a .env walked up from cwd, then a global ~/.config/spar/.env
// fallback — so `spar review` resolves a key regardless of which
// project directory it's run from.
func loadAPIKey() string {
	key, _ := ResolveAPIKey()
	return key
}

// ResolveAPIKey is loadAPIKey plus a label for which of the three
// sources actually supplied the key (empty source if none did) — used
// by `spar doctor` to report not just whether a key resolved, but from
// where, since a key found by the wrong source (e.g. a stray project
// .env instead of the global config) is itself worth surfacing.
func ResolveAPIKey() (key, source string) {
	if v := os.Getenv("ANTHROPIC_API_KEY"); v != "" {
		return stripQuotes(v), "the ANTHROPIC_API_KEY environment variable"
	}
	dir, err := os.Getwd()
	if err == nil {
		for {
			path := filepath.Join(dir, ".env")
			if v := readEnvFrom(path); v != "" {
				return v, path
			}
			parent := filepath.Dir(dir)
			if parent == dir {
				break
			}
			dir = parent
		}
	}
	if home, err := os.UserHomeDir(); err == nil {
		path := filepath.Join(home, ".config", "spar", ".env")
		if v := readEnvFrom(path); v != "" {
			return v, path
		}
	}
	return "", ""
}

func readEnvFrom(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if v, ok := strings.CutPrefix(line, "ANTHROPIC_API_KEY="); ok {
			return stripQuotes(strings.TrimSpace(v))
		}
	}
	return ""
}

func stripQuotes(s string) string {
	if len(s) >= 2 {
		if (s[0] == '"' && s[len(s)-1] == '"') || (s[0] == '\'' && s[len(s)-1] == '\'') {
			return s[1 : len(s)-1]
		}
	}
	return s
}
