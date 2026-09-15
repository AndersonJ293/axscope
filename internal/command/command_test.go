package command

import (
	"reflect"
	"strings"
	"testing"

	"github.com/AndersonJ293/axscope/internal/protocol"
)

func TestParse(t *testing.T) {
	cases := []struct {
		name    string
		tokens  []string
		want    protocol.Request
		wantErr bool
	}{
		{
			name:   "simple positional",
			tokens: []string{"open", "https://x.test"},
			want:   protocol.Request{Cmd: "open", Args: map[string]any{"url": "https://x.test"}},
		},
		{
			name:   "boolean flag",
			tokens: []string{"open", "https://x.test", "--new"},
			want:   protocol.Request{Cmd: "open", Args: map[string]any{"url": "https://x.test", "new": true}},
		},
		{
			name:   "key=value of a known argument",
			tokens: []string{"drag", "e1", "e2", "type=pointer"},
			want:   protocol.Request{Cmd: "drag", Args: map[string]any{"from": "e1", "to": "e2", "type": "pointer"}},
		},
		{
			// `k=v` only becomes a pair when `k` is known; otherwise it is
			// positional, and a text with "=" (JS, free value) is not lost as a pair.
			name:   "text with equals lands as positional",
			tokens: []string{"fill", "#a", "a=b"},
			want:   protocol.Request{Cmd: "fill", Args: map[string]any{"target": "#a", "value": "a=b"}},
		},
		{
			name:   "optional positional omitted",
			tokens: []string{"read"},
			want:   protocol.Request{Cmd: "read", Args: map[string]any{}},
		},
		{
			name:   "optional positional filled",
			tokens: []string{"read", "#content"},
			want:   protocol.Request{Cmd: "read", Args: map[string]any{"selector": "#content"}},
		},
		{
			name:    "no tokens",
			tokens:  []string{},
			wantErr: true,
		},
		{
			name:    "unknown command",
			tokens:  []string{"voar"},
			wantErr: true,
		},
		{
			name:    "unknown flag",
			tokens:  []string{"open", "https://x.test", "--voar"},
			wantErr: true,
		},
		{
			name:    "extra positional argument",
			tokens:  []string{"open", "a", "b"},
			wantErr: true,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := Parse(c.tokens)
			if c.wantErr {
				if err == nil {
					t.Fatalf("expected error, got %+v", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !reflect.DeepEqual(got, c.want) {
				t.Errorf("request diverged:\n got: %+v\nwant: %+v", got, c.want)
			}
		})
	}
}

// Regression: `fill` and `type` could not target by `text=`. The token was
// swallowed as a key=value pair, because the positional was called `text` — so,
// precisely in the two commands where one most wants to aim by label.
func TestParse_FillTargetsByText(t *testing.T) {
	for _, cmd := range []string{"fill", "type"} {
		req, err := Parse([]string{cmd, "text=CAPTCHA", "A7K9P"})
		if err != nil {
			t.Fatalf("%s: %v", cmd, err)
		}
		if req.Args["target"] != "text=CAPTCHA" || req.Args["value"] != "A7K9P" {
			t.Errorf("%s: args = %v", cmd, req.Args)
		}
	}
}

// Regression: `scroll` had no way to target a container. The lab's infinite
// scroll list showed the gap: scrolling the page does not load the next batch,
// and the gesture without a target scrolled whatever was under the center of
// the screen.
func TestParse_ScrollWithTarget(t *testing.T) {
	req, err := Parse([]string{"scroll", "200", "target=text=Infinite item 3"})
	if err != nil {
		t.Fatalf("Parse rejected the target: %v", err)
	}
	if req.Args["dy"] != "200" || req.Args["target"] != "text=Infinite item 3" {
		t.Errorf("args = %v", req.Args)
	}
	// And without a target it still holds: it is optional.
	req, err = Parse([]string{"scroll", "500"})
	if err != nil {
		t.Fatalf("scroll without target should hold: %v", err)
	}
	if req.Args["dy"] != "500" {
		t.Errorf("args = %v", req.Args)
	}
}

// Regression: `upload` declares the positional as `target`, and the handler
// reads `target`. The name has to match: when it did not, the target fell into
// the void and the command always picked the first `<input type=file>` — and the
// lab accepted it by accident, because its dropzone has a hidden input inside.
func TestParse_UploadWithTarget(t *testing.T) {
	req, err := Parse([]string{"upload", "/tmp/a.txt"})
	if err != nil {
		t.Fatalf("Parse rejected the upload: %v", err)
	}
	if req.Args["file"] != "/tmp/a.txt" {
		t.Errorf("without target: args = %v", req.Args)
	}

	req, err = Parse([]string{"upload", "/tmp/a.txt", "target=css=#dropzone"})
	if err != nil {
		t.Fatalf("Parse rejected the target: %v", err)
	}
	if req.Args["file"] != "/tmp/a.txt" || req.Args["target"] != "css=#dropzone" {
		t.Errorf("with target: args = %v", req.Args)
	}
}

// The help derives from the table: every spec shows up in the list.
func TestHelpListsAllSpecs(t *testing.T) {
	help := Help()
	for _, spec := range Specs {
		if !strings.Contains(help, spec.Cmd) {
			t.Errorf("help does not mention %q", spec.Cmd)
		}
	}
}

// Regression: the wait handler reads `timeout`, but the command did not expose
// the option — Parse rejected it as extra, and the option was unreachable.
func TestParse_WaitWithTimeout(t *testing.T) {
	req, err := Parse([]string{"wait", "carregando", "5000"})
	if err != nil {
		t.Fatalf("Parse rejected the timeout: %v", err)
	}
	if req.Args["text"] != "carregando" || req.Args["timeout"] != "5000" {
		t.Errorf("args = %v", req.Args)
	}
	// And without the timeout it still holds: it is optional.
	req, err = Parse([]string{"wait", "carregando"})
	if err != nil {
		t.Errorf("wait without timeout should hold: %v", err)
	}
}

// Regression (lab v3): `wait` by text matched anywhere on the page — waiting for
// a role that also appeared in the sidebar came back in 2ms, with the search
// dropdown still closed. `within=` limits the search; and the state flags wait
// for a target that only enables later.
func TestParse_WaitWithScopeAndState(t *testing.T) {
	req, err := Parse([]string{"wait", "Senior Recruiter", "5000", "within=css=#suggest"})
	if err != nil {
		t.Fatalf("Parse rejected the scope: %v", err)
	}
	if req.Args["text"] != "Senior Recruiter" || req.Args["timeout"] != "5000" || req.Args["within"] != "css=#suggest" {
		t.Errorf("args = %v", req.Args)
	}

	req, err = Parse([]string{"wait", "css=#submitApply", "8000", "--enabled"})
	if err != nil {
		t.Fatalf("Parse rejected the state: %v", err)
	}
	if req.Args["enabled"] != true || req.Args["text"] != "css=#submitApply" {
		t.Errorf("args = %v", req.Args)
	}

	// waitgone also accepts a scope.
	if req, err := Parse([]string{"waitgone", "Carregando", "within=css=#lista"}); err != nil {
		t.Fatalf("waitgone with scope: %v", err)
	} else if req.Args["within"] != "css=#lista" {
		t.Errorf("args = %v", req.Args)
	}
}
