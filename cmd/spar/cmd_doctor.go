package main

import (
	"fmt"
	"os"
	"os/exec"

	"github.com/justinstimatze/spar/internal/inject"
	"github.com/justinstimatze/spar/internal/store"
)

// cmdDoctor checks the things spar can't tell you from a failed run
// alone: is a key resolvable and from where, are the external tools it
// shells out to on PATH, and is its state directory actually writable.
// Nothing it checks makes a network call.
func cmdDoctor(args []string) {
	ok := true

	if key, source := inject.ResolveAPIKey(); key != "" {
		fmt.Printf("[ok]   ANTHROPIC_API_KEY resolved from %s\n", source)
	} else {
		ok = false
		fmt.Println("[fail] no API key found (checked: the environment, .env walked up from the current directory, ~/.config/spar/.env)")
		fmt.Println("       spar review will always show the real diff until this is set — no error, just no trials to measure.")
	}

	if path, err := exec.LookPath("git"); err == nil {
		fmt.Printf("[ok]   git found at %s\n", path)
	} else {
		ok = false
		fmt.Println("[fail] git not found on PATH — spar can't capture or regenerate diffs without it")
	}

	if path, err := exec.LookPath("delta"); err == nil {
		fmt.Printf("[ok]   delta found at %s\n", path)
	} else {
		fmt.Println("[warn] delta not found on PATH — spar review will fall back to plain-text diffs")
	}

	if err := checkLogDirWritable(); err != nil {
		ok = false
		fmt.Printf("[fail] log directory not writable: %v\n", err)
	} else {
		dir, _ := store.SparDir()
		fmt.Printf("[ok]   log directory writable (%s)\n", dir)
	}

	if !ok {
		os.Exit(1)
	}
}

// checkLogDirWritable creates spar's state dir if needed and confirms
// it's actually writable, without touching the real log file — it
// writes and immediately removes a throwaway file instead.
func checkLogDirWritable() error {
	dir, err := store.SparDir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".doctor-check-*")
	if err != nil {
		return err
	}
	name := f.Name()
	f.Close()
	return os.Remove(name)
}
