package livetaxonomy

import (
	"math/rand"
	"testing"
)

func TestCategoriesHaveNameAndDescription(t *testing.T) {
	if len(Categories) == 0 {
		t.Fatal("Categories is empty")
	}
	seen := make(map[string]bool)
	for _, c := range Categories {
		if c.Name == "" {
			t.Errorf("category with empty Name: %+v", c)
		}
		if c.Description == "" {
			t.Errorf("category %q has empty Description", c.Name)
		}
		if seen[c.Name] {
			t.Errorf("duplicate category name %q", c.Name)
		}
		seen[c.Name] = true
	}
}

func TestPickReturnsOnlyKnownCategories(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	valid := make(map[string]bool)
	for _, c := range Categories {
		valid[c.Name] = true
	}
	for i := 0; i < 200; i++ {
		got := Pick(rng)
		if !valid[got.Name] {
			t.Fatalf("Pick returned unknown category %q", got.Name)
		}
	}
}

func TestPickCoversEveryCategory(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	seen := make(map[string]bool)
	for i := 0; i < 2000; i++ {
		seen[Pick(rng).Name] = true
	}
	for _, c := range Categories {
		if !seen[c.Name] {
			t.Errorf("category %q never picked in 2000 draws", c.Name)
		}
	}
}
