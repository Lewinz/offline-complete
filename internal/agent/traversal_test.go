package agent

import (
	"testing"

	"offline-complete/internal/rules"
)

func TestHasPrefixFold(t *testing.T) {
	for _, tc := range []struct {
		name, prefix string
		want         bool
	}{
		{"Documents/", "do", true},
		{"documents/", "Do", true},
		{"Downloads", "DOWN", true},
		{"docker", "do", true},
		{"Documents/", "Dr", false},
		{"", "d", false},
	} {
		if got := hasPrefixFold(tc.name, tc.prefix); got != tc.want {
			t.Errorf("hasPrefixFold(%q, %q) = %v, want %v", tc.name, tc.prefix, got, tc.want)
		}
	}
}

func TestDedupeCandidatesPrefersBareDirectoryName(t *testing.T) {
	got := dedupeCandidates([]rules.Candidate{
		{Name: "folder/", Description: "目录", Path: "/tmp/folder"},
		{Name: "folder", Description: "历史"},
		{Name: "other/", Description: "目录"},
	})
	if len(got) != 2 || got[0].Name != "folder" || got[1].Name != "other/" {
		t.Fatalf("deduped = %+v", got)
	}
}

func TestTraversalNoise(t *testing.T) {
	for _, tc := range []struct {
		name string
		want bool
	}{
		{"../", true},
		{"..", true},
		{"../../", true},
		{"../../../", true},
		{"../..", true},
		{"...", true},
		{".../", true},
		{"../sub2api/gateway/", false},
		{"Documents/", false},
		{"./", false},
	} {
		if got := traversalNoise(tc.name); got != tc.want {
			t.Errorf("traversalNoise(%q) = %v, want %v", tc.name, got, tc.want)
		}
	}
}
