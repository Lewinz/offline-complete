package generator

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"offline-complete/internal/rules"
)

func TestNodeRunnerSequentialRequestsReuseTheSameProcess(t *testing.T) {
	runner := NewNodeRunner()
	if !runner.Available() {
		t.Skip("node is unavailable")
	}
	defer runner.Close()
	runner.timeout = 2 * time.Second

	source := `export default {name:"tool",subcommands:[{name:"one"},{name:"two"}]}`
	for attempt := 0; attempt < 4; attempt++ {
		start := time.Now()
		got, err := runner.Complete(NodeRequest{
			Source:  source,
			Command: "tool",
			Kind:    "subcommand",
			Tokens:  []string{"tool", ""},
		})
		if err != nil {
			t.Fatalf("request %d: %v", attempt, err)
		}
		if len(got) != 2 {
			t.Fatalf("request %d candidates = %#v", attempt, got)
		}
		if elapsed := time.Since(start); elapsed >= runner.timeout {
			t.Fatalf("request %d took %v, expected a response before the timeout", attempt, elapsed)
		}
	}
}

func TestNodeRunnerKeepsFilepathGeneratorResultsWhileTyping(t *testing.T) {
	runner := NewNodeRunner()
	if !runner.Available() {
		t.Skip("node is unavailable")
	}
	defer runner.Close()

	data, err := rules.LoadFile(filepath.Join("..", "..", "rules", "base.bin"))
	if err != nil {
		t.Fatal(err)
	}
	pack, err := rules.FromJSON(data)
	if err != nil {
		t.Fatal(err)
	}
	store := &rules.SpecStore{Dir: filepath.Join("..", "..", "rules", "specs")}
	source, err := store.Load(pack.Specs["cd"])
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	for _, name := range []string{"one", "two", "three", "Alpha", "beta"} {
		if err := os.Mkdir(filepath.Join(dir, name), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "file"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		prefix string
		want   string
	}{
		{"", "one/"},
		{"o", "one/"},
		{"tw", "two/"},
		{"al", "Alpha/"},
		{"BE", "beta/"},
	} {
		got, err := runner.Complete(NodeRequest{
			Source:  string(source),
			Command: "cd",
			Kind:    "positional",
			Prefix:  tc.prefix,
			CWD:     dir,
			Tokens:  []string{"cd", tc.prefix},
		})
		if err != nil {
			t.Fatalf("prefix %q: %v", tc.prefix, err)
		}
		found := false
		for _, candidate := range got {
			if candidate.Name != tc.want {
				continue
			}
			found = true
			if candidate.Path != filepath.Join(dir, strings.TrimSuffix(tc.want, "/")) {
				t.Fatalf("prefix %q path = %q, want %q", tc.prefix, candidate.Path, filepath.Join(dir, strings.TrimSuffix(tc.want, "/")))
			}
		}
		if !found {
			t.Fatalf("prefix %q candidates = %#v, missing %q", tc.prefix, got, tc.want)
		}
	}
}

func TestNodeRunnerExecutesKiroGitGenerator(t *testing.T) {
	runner := NewNodeRunner()
	if !runner.Available() {
		t.Skip("node is unavailable")
	}
	defer runner.Close()
	data, err := rules.LoadFile(filepath.Join("..", "..", "rules", "base.bin"))
	if err != nil {
		t.Fatal(err)
	}
	pack, err := rules.FromJSON(data)
	if err != nil {
		t.Fatal(err)
	}
	id := pack.Specs["git"]
	if id == "" {
		t.Fatal("git spec mapping missing")
	}
	store := &rules.SpecStore{Dir: filepath.Join("..", "..", "rules", "specs")}
	source, err := store.Load(id)
	if err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	for _, args := range [][]string{
		{"init", "-q"},
		{"config", "user.email", "test@example.invalid"},
		{"config", "user.name", "Test"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, output)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "file"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"add", "file"}, {"commit", "-qm", "initial"}, {"branch", "feature-test"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, output)
		}
	}

	got, err := runner.Complete(NodeRequest{
		Source:      string(source),
		Command:     "git",
		Subcommands: []string{"checkout"},
		Kind:        "positional",
		Slot:        0,
		Prefix:      "fe",
		CWD:         dir,
		Tokens:      []string{"git", "checkout", "fe"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Name != "feature-test" {
		t.Fatalf("candidates = %#v", got)
	}
}

func TestNodeRunnerRunsCustomFilepathGeneratorForOptionValue(t *testing.T) {
	runner := NewNodeRunner()
	if !runner.Available() {
		t.Skip("node is unavailable")
	}
	defer runner.Close()
	source, err := os.ReadFile(filepath.Join("..", "..", "rules", "specs", "codesign.js"))
	if err != nil {
		// The development spec directory uses a hashed copy for the installed
		// pack; avoid coupling this test to that generated filename.
		t.Skip("codesign spec is unavailable")
	}
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "sub"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "sub", "a.plist"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := runner.Complete(NodeRequest{
		Source:  string(source),
		Command: "codesign",
		Kind:    "option-value",
		Option:  "--resource-rules",
		Prefix:  "sub/a",
		CWD:     dir,
		Tokens:  []string{"codesign", "--resource-rules", "sub/a"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Name != "sub/a.plist" {
		t.Fatalf("candidates = %#v", got)
	}
}

func TestNodeRunnerLoadsCrossSpec(t *testing.T) {
	runner := NewNodeRunner()
	if !runner.Available() {
		t.Skip("node is unavailable")
	}
	defer runner.Close()
	store := &rules.SpecStore{Dir: filepath.Join("..", "..", "rules", "specs.enc")}
	source, err := store.LoadName("aws")
	if err != nil {
		t.Skip("aws spec index is unavailable")
	}
	runner.Specs = store
	got, err := runner.Complete(NodeRequest{
		Source:      string(source),
		Command:     "aws",
		Subcommands: []string{"s3"},
		Kind:        "subcommand",
		Prefix:      "l",
		CWD:         t.TempDir(),
		Tokens:      []string{"aws", "s3", "l"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) == 0 || got[0].Name != "ls" {
		t.Fatalf("cross-spec candidates = %#v", got)
	}
}

func TestNodeRunnerExecutesGenerateSpec(t *testing.T) {
	runner := NewNodeRunner()
	if !runner.Available() {
		t.Skip("node is unavailable")
	}
	defer runner.Close()
	source := []byte(`export default {name:"tool", generateSpec: async () => ({subcommands:[{name:"dynamic",description:"generated"}]})}`)
	got, err := runner.Complete(NodeRequest{
		Source:  string(source),
		Command: "tool",
		Kind:    "subcommand",
		Prefix:  "d",
		Tokens:  []string{"tool", "d"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Name != "dynamic" || got[0].Description != "generated" {
		t.Fatalf("candidates = %#v", got)
	}
}

func TestNodeRunnerUsesOCAISettingCommand(t *testing.T) {
	runner := NewNodeRunner()
	if !runner.Available() {
		t.Skip("node is unavailable")
	}
	defer runner.Close()
	t.Setenv("OFFLINE_COMPLETE_AI_URL", "http://127.0.0.1:1/v1/chat/completions")
	source := []byte(`export default {name:"tool",args:{name:"x",generators:{custom:async(t,e)=>{const s=await e({command:"oc",args:["setting","autocomplete.ai.enabled"]});return [{name:s.stdout}]}}}}`)
	got, err := runner.Complete(NodeRequest{
		Source:  string(source),
		Command: "tool",
		Kind:    "positional",
		Slot:    0,
		CWD:     t.TempDir(),
		Tokens:  []string{"tool", ""},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Name != "true" {
		t.Fatalf("candidates = %#v", got)
	}
}

func TestNodeRunnerOCAIProtocols(t *testing.T) {
	source := `export default {name:"tool",args:{name:"x",generators:{custom:async(t,e)=>{const r=await e({command:"oc",args:["ai","request","--method","POST","--body",JSON.stringify({model:"spec-model",source:"autocomplete",name:"n",messages:[{role:"system",content:"sys"},{role:"user",content:"hi"}],temperature:0.2})]});const j=JSON.parse(r.stdout);return [{name:j.choices[0].message.content}]}}}}`

	t.Run("openai", func(t *testing.T) {
		runner := NewNodeRunner()
		if !runner.Available() {
			t.Skip("node is unavailable")
		}
		defer runner.Close()
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			body, _ := io.ReadAll(r.Body)
			var payload map[string]any
			if err := json.Unmarshal(body, &payload); err != nil {
				t.Errorf("body: %v", err)
			}
			if _, ok := payload["source"]; ok {
				t.Errorf("OpenAI body retained source: %s", body)
			}
			if r.Header.Get("Authorization") != "Bearer test-key" {
				t.Errorf("authorization = %q", r.Header.Get("Authorization"))
			}
			_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"openai-ok"}}]}`))
		}))
		defer server.Close()
		t.Setenv("OFFLINE_COMPLETE_AI_URL", server.URL+"/v1/chat/completions")
		t.Setenv("OFFLINE_COMPLETE_AI_PROTOCOL", "openai")
		t.Setenv("OFFLINE_COMPLETE_AI_KEY", "test-key")
		got, err := runner.Complete(NodeRequest{Source: source, Command: "tool", Kind: "positional", Slot: 0, CWD: t.TempDir(), Tokens: []string{"tool", ""}})
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 1 || got[0].Name != "openai-ok" {
			t.Fatalf("candidates = %#v", got)
		}
	})

	t.Run("anthropic", func(t *testing.T) {
		runner := NewNodeRunner()
		if !runner.Available() {
			t.Skip("node is unavailable")
		}
		defer runner.Close()
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			body, _ := io.ReadAll(r.Body)
			var payload map[string]any
			if err := json.Unmarshal(body, &payload); err != nil {
				t.Errorf("body: %v", err)
			}
			if payload["system"] != "sys" {
				t.Errorf("system = %#v", payload["system"])
			}
			if r.Header.Get("x-api-key") != "test-key" {
				t.Errorf("x-api-key = %q", r.Header.Get("x-api-key"))
			}
			_, _ = w.Write([]byte(`{"content":[{"type":"text","text":"anthropic-ok"}]}`))
		}))
		defer server.Close()
		t.Setenv("OFFLINE_COMPLETE_AI_URL", server.URL+"/v1/messages")
		t.Setenv("OFFLINE_COMPLETE_AI_PROTOCOL", "anthropic")
		t.Setenv("OFFLINE_COMPLETE_AI_KEY", "test-key")
		got, err := runner.Complete(NodeRequest{Source: source, Command: "tool", Kind: "positional", Slot: 0, CWD: t.TempDir(), Tokens: []string{"tool", ""}})
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 1 || got[0].Name != "anthropic-ok" {
			t.Fatalf("candidates = %#v", got)
		}
	})
}
