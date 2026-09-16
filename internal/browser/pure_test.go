// Tests for the pure helpers of the package: the ones that take values and can
// be pinned without a live CDP client or a browser.
package browser

import (
	"encoding/json"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/AndersonJ293/axscope/internal/dom"
)

// keyInfo is the mapping from a key name to the CDP {key, code, virtualKeyCode}
// triple. A wrong vk makes Enter not submit a form and a shortcut miss its
// handler, so the whole table is pinned.
func TestKeyInfo(t *testing.T) {
	cases := []struct {
		key  string
		want keyDef
	}{
		{"a", keyDef{key: "a", code: "KeyA", vk: 65}},
		{"A", keyDef{key: "A", code: "KeyA", vk: 65}},
		{"7", keyDef{key: "7", code: "Key7", vk: 55}},
		{"Enter", keyDef{key: "Enter", code: "Enter", vk: 13}},
		{"Tab", keyDef{key: "Tab", code: "Tab", vk: 9}},
		{"Escape", keyDef{key: "Escape", code: "Escape", vk: 27}},
		{"Esc", keyDef{key: "Escape", code: "Escape", vk: 27}},
		{"Backspace", keyDef{key: "Backspace", code: "Backspace", vk: 8}},
		{"Delete", keyDef{key: "Delete", code: "Delete", vk: 46}},
		{"ArrowUp", keyDef{key: "ArrowUp", code: "ArrowUp", vk: 38}},
		{"ArrowDown", keyDef{key: "ArrowDown", code: "ArrowDown", vk: 40}},
		{"ArrowLeft", keyDef{key: "ArrowLeft", code: "ArrowLeft", vk: 37}},
		{"ArrowRight", keyDef{key: "ArrowRight", code: "ArrowRight", vk: 39}},
		{"Home", keyDef{key: "Home", code: "Home", vk: 36}},
		{"End", keyDef{key: "End", code: "End", vk: 35}},
		{"PageUp", keyDef{key: "PageUp", code: "PageUp", vk: 33}},
		{"PageDown", keyDef{key: "PageDown", code: "PageDown", vk: 34}},
		{"Space", keyDef{key: " ", code: "Space", vk: 32}},
		{"F1", keyDef{key: "F1", code: "F1", vk: 112}},
		{"F12", keyDef{key: "F12", code: "F12", vk: 123}},
	}
	for _, c := range cases {
		t.Run(c.key, func(t *testing.T) {
			if got := keyInfo(c.key); got != c.want {
				t.Errorf("keyInfo(%q) = %#v, want %#v", c.key, got, c.want)
			}
		})
	}

	// Unknown keys carry no code, which is how Press refuses them instead of
	// sending an empty event. A single letter (including "F") is a real key.
	for _, name := range []string{"", "Nope", "F0", "F13", "SuperKey"} {
		if got := keyInfo(name); got.code != "" {
			t.Errorf("keyInfo(%q) = %#v, want the zero key (no code)", name, got)
		}
	}
}

// The cursor delay is an environment knob; an invalid or negative value must
// fall back to the default or to no delay, never to a negative sleep.
func TestCursorDelay(t *testing.T) {
	cases := []struct {
		env     string
		wantMs  int
		wantDur time.Duration
	}{
		{"", 160, 160 * time.Millisecond},
		{"250", 250, 250 * time.Millisecond},
		{"0", 0, 0},
		{"-10", 0, 0},
		{"abc", 160, 160 * time.Millisecond},
	}
	for _, c := range cases {
		t.Run("env="+c.env, func(t *testing.T) {
			t.Setenv("AXSCOPE_CURSOR_DELAY", c.env)
			if got := cursorDelayMs(); got != c.wantMs {
				t.Errorf("cursorDelayMs() = %d, want %d", got, c.wantMs)
			}
			if got := visualDelay(); got != c.wantDur {
				t.Errorf("visualDelay() = %v, want %v", got, c.wantDur)
			}
		})
	}
}

func TestEnvIntAndBool(t *testing.T) {
	intCases := []struct {
		env  string
		def  int
		want int
	}{
		{"12", 0, 12},
		{"-3", 0, -3},
		{"", 9, 9},
		{"abc", 9, 9},
		{" 5 ", 0, 0}, // Atoi rejects the spaces; the default wins.
	}
	for _, c := range intCases {
		t.Run("int "+c.env, func(t *testing.T) {
			t.Setenv("AXSCOPE_TEST_INT", c.env)
			if got := envInt("AXSCOPE_TEST_INT", c.def); got != c.want {
				t.Errorf("envInt(%q, %d) = %d, want %d", c.env, c.def, got, c.want)
			}
		})
	}

	boolCases := []struct {
		env  string
		def  bool
		want bool
	}{
		{"1", false, true},
		{"true", false, true},
		{"yes", false, true},
		{"on", false, true},
		{"0", true, false},
		{"false", true, false},
		{"no", true, false},
		{"off", true, false},
		{"", true, true},
		{"", false, false},
		{"maybe", true, true},
	}
	for _, c := range boolCases {
		t.Run("bool "+c.env, func(t *testing.T) {
			t.Setenv("AXSCOPE_TEST_BOOL", c.env)
			if got := envBool("AXSCOPE_TEST_BOOL", c.def); got != c.want {
				t.Errorf("envBool(%q, %v) = %v, want %v", c.env, c.def, got, c.want)
			}
		})
	}
}

func TestDecodeBase64(t *testing.T) {
	got, err := decodeBase64("aGVsbG8=")
	if err != nil {
		t.Fatalf("decodeBase64: %v", err)
	}
	if string(got) != "hello" {
		t.Errorf("decodeBase64 = %q, want hello", got)
	}
	if _, err := decodeBase64("not!base64"); err == nil {
		t.Error("invalid base64 must return an error")
	}
}

// rawBool accepts both the JSON boolean and the JSON string "true" the CDP
// sometimes uses for an accessibility property.
func TestRawAXHelpers(t *testing.T) {
	for _, raw := range []json.RawMessage{json.RawMessage("true"), json.RawMessage(`"true"`)} {
		if !rawBool(raw) {
			t.Errorf("rawBool(%s) = false, want true", raw)
		}
	}
	for _, raw := range []json.RawMessage{json.RawMessage("false"), json.RawMessage(`"false"`), json.RawMessage(`"1"`), nil} {
		if rawBool(raw) {
			t.Errorf("rawBool(%s) = true, want false", raw)
		}
	}

	for _, raw := range []json.RawMessage{json.RawMessage(`"mixed"`), json.RawMessage(`{"x":"mixed"}`)} {
		if !isMixed(raw) {
			t.Errorf("isMixed(%s) = false, want true", raw)
		}
	}
	if isMixed(json.RawMessage(`"false"`)) {
		t.Error("isMixed must be false without the word mixed")
	}

	if got := axRawString(json.RawMessage(`"hello"`)); got != "hello" {
		t.Errorf("axRawString(JSON string) = %q, want hello", got)
	}
	if got := axRawString(json.RawMessage(`bare`)); got != "bare" {
		t.Errorf("axRawString(bare) = %q, want bare", got)
	}
}

func TestTextHelpers(t *testing.T) {
	repeat := []struct {
		text, parent string
		want         bool
	}{
		{"Item", "item list", true},
		{"ITEM", "my item", true},
		{"xyz", "abc", false},
		{"", "abc", false},
		{"abc", "", false},
	}
	for _, c := range repeat {
		if got := repeatOf(c.text, c.parent); got != c.want {
			t.Errorf("repeatOf(%q, %q) = %v, want %v", c.text, c.parent, got, c.want)
		}
	}

	separators := []struct {
		in   string
		want bool
	}{
		{" | · • ", true},
		{"", true},
		{"...", true},
		{"\u00a0", true},
		{"a", false},
		{"| x |", false},
	}
	for _, c := range separators {
		if got := separatorOnly(c.in); got != c.want {
			t.Errorf("separatorOnly(%q) = %v, want %v", c.in, got, c.want)
		}
	}

	norms := []struct{ in, want string }{
		{"  a\n\tb  c ", "a b c"},
		{"", ""},
		{"a\r\nb", "a b"},
	}
	for _, c := range norms {
		if got := norm(c.in); got != c.want {
			t.Errorf("norm(%q) = %q, want %q", c.in, got, c.want)
		}
	}

	truncs := []struct {
		in   string
		max  int
		want string
	}{
		{"hello", 10, "hello"},
		{"hello", 5, "hello"},
		{"hello", 3, "hel…"},
		{"hello", 0, "…"},
	}
	for _, c := range truncs {
		if got := truncate(c.in, c.max); got != c.want {
			t.Errorf("truncate(%q, %d) = %q, want %q", c.in, c.max, got, c.want)
		}
	}
}

func TestObserveHelpers(t *testing.T) {
	texts := []struct {
		name        string
		value       any
		description string
		typ         string
		want        string
	}{
		{"string value wins", "hello", "desc", "string", "hello"},
		{"number becomes JSON", float64(3), "", "number", "3"},
		{"bool becomes JSON", true, "", "boolean", "true"},
		{"null falls back to description", nil, "some description", "object", "some description"},
		{"null with only type", nil, "", "object", "<object>"},
		{"nothing at all", nil, "", "", ""},
	}
	for _, c := range texts {
		t.Run(c.name, func(t *testing.T) {
			if got := remoteObjectText(c.value, c.description, c.typ); got != c.want {
				t.Errorf("remoteObjectText = %q, want %q", got, c.want)
			}
		})
	}

	levels := []struct{ in, want string }{
		{"error", "error"}, {"assert", "error"},
		{"warning", "warn"},
		{"debug", "debug"}, {"verbose", "debug"},
		{"info", "info"},
		{"log", "log"}, {"trace", "log"}, {"", "log"},
	}
	for _, c := range levels {
		if got := consoleLevel(c.in); got != c.want {
			t.Errorf("consoleLevel(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestDropPoint(t *testing.T) {
	target := &Target{Rect: dom.Rect{X: 100, Y: 200, Width: 40, Height: 80}}
	cases := []struct {
		at           string
		wantX, wantY float64
	}{
		{"", 120, 240},       // default is the element center
		{"center", 120, 240}, // any unknown value falls back to the center
		{"top", 120, 220},    // 200 + 80*0.25
		{"bottom", 120, 260}, // 200 + 80*0.75
	}
	for _, c := range cases {
		x, y := dropPoint(target, c.at)
		if x != c.wantX || y != c.wantY {
			t.Errorf("dropPoint(at=%q) = (%v, %v), want (%v, %v)", c.at, x, y, c.wantX, c.wantY)
		}
	}

	// A pos=x,y target moves where the drop happens, not the element's box.
	pos := &Target{Rect: dom.Rect{X: 100, Y: 200, Width: 40, Height: 80}, Point: &Point{X: 5, Y: 6}}
	if x, y := dropPoint(pos, ""); x != 5 || y != 6 {
		t.Errorf("dropPoint with pos target = (%v, %v), want (5, 6)", x, y)
	}
}

func TestAppendCapped(t *testing.T) {
	var buf []int
	for _, v := range []int{1, 2, 3, 4, 5} {
		buf = appendCapped(buf, v, 3)
	}
	if len(buf) != 3 || buf[0] != 3 || buf[1] != 4 || buf[2] != 5 {
		t.Errorf("appendCapped kept %v, want [3 4 5]", buf)
	}
	if got := appendCapped([]int{1}, 2, 5); len(got) != 2 || got[0] != 1 || got[1] != 2 {
		t.Errorf("appendCapped below the cap = %v, want [1 2]", got)
	}
	if got := appendCapped(nil, 1, 0); len(got) != 0 {
		t.Errorf("appendCapped with max 0 = %v, want empty", got)
	}
}

func TestEngineProduct(t *testing.T) {
	cases := []struct{ engine, want string }{
		{EngineChrome, "chrome"},
		{EngineShell, "chrome-headless-shell"},
		{EngineExt, "chrome"},
		{"", "chrome"},
		{"other", "chrome"},
	}
	for _, c := range cases {
		if got := engineProduct(c.engine); got != c.want {
			t.Errorf("engineProduct(%q) = %q, want %q", c.engine, got, c.want)
		}
	}
}

func TestWithKey(t *testing.T) {
	if got := withKey(nil, "exit_type", "Normal"); got["exit_type"] != "Normal" {
		t.Errorf("withKey(nil) = %#v, want exit_type=Normal", got)
	}
	base := map[string]any{"a": 1}
	got := withKey(base, "b", 2)
	if got["a"] != 1 || got["b"] != 2 {
		t.Errorf("withKey did not preserve the existing map: %#v", got)
	}
	if replaced := withKey("not a map", "k", "v"); replaced["k"] != "v" {
		t.Errorf("withKey on a non-map = %#v, want k=v", replaced)
	}
}

func TestProfilePath(t *testing.T) {
	t.Setenv("AXSCOPE_HOME", "/tmp/axscope-home")
	want := filepath.Join("/tmp/axscope-home", "profiles", "work")
	if got := ProfilePath("work"); got != want {
		t.Errorf("ProfilePath = %q, want %q", got, want)
	}
}

// buildArgs is the launch command line; the flags that keep the profile isolated
// and the window deterministic must be present, and the headless/shell split
// must not add a headless flag to the already-headless shell.
func TestBuildArgs(t *testing.T) {
	t.Setenv("AXSCOPE_FORCE_AX", "0")

	has := func(args []string, want string) bool {
		for _, a := range args {
			if a == want {
				return true
			}
		}
		return false
	}

	base := buildArgs(LaunchOptions{Port: 9222}, "/tmp/profile")
	for _, want := range []string{
		"--remote-debugging-port=9222",
		"--remote-debugging-address=127.0.0.1",
		"--user-data-dir=/tmp/profile",
		"--window-size=1440,960",
		"--window-position=60,60",
	} {
		if !has(base, want) {
			t.Errorf("buildArgs is missing %q in %v", want, base)
		}
	}
	if has(base, "--headless=new") {
		t.Errorf("a non-headless launch must not add --headless=new: %v", base)
	}
	if has(base, "--force-renderer-accessibility") {
		t.Errorf("AXSCOPE_FORCE_AX=0 must not force accessibility: %v", base)
	}

	headless := buildArgs(LaunchOptions{Port: 1, Engine: EngineChrome, Headless: true}, "p")
	if !has(headless, "--headless=new") || !has(headless, "--disable-gpu") {
		t.Errorf("headless chrome needs --headless=new and --disable-gpu: %v", headless)
	}

	shell := buildArgs(LaunchOptions{Port: 1, Engine: EngineShell, Headless: true}, "p")
	if has(shell, "--headless=new") {
		t.Errorf("chrome-headless-shell is already headless: %v", shell)
	}
	if !has(shell, "--disable-gpu") {
		t.Errorf("the shell still disables the gpu: %v", shell)
	}

	t.Setenv("AXSCOPE_FORCE_AX", "1")
	forced := buildArgs(LaunchOptions{Port: 1}, "p")
	if !has(forced, "--force-renderer-accessibility") {
		t.Errorf("AXSCOPE_FORCE_AX=1 must force accessibility: %v", forced)
	}

	extra := buildArgs(LaunchOptions{Port: 1, WindowSize: "800,600", ExtraArgs: []string{"--foo"}}, "p")
	if !has(extra, "--window-size=800,600") || !has(extra, "--foo") {
		t.Errorf("window size and extra args were lost: %v", extra)
	}
}

func TestTargetExpressions(t *testing.T) {
	// requestedText only recognizes the text= form.
	for _, c := range []struct{ in, want string }{
		{"text=Save", "Save"},
		{"text=  Save  ", "Save"},
		{"e1", ""},
		{"css=#x", ""},
		{"", ""},
	} {
		if got := requestedText(c.in); got != c.want {
			t.Errorf("requestedText(%q) = %q, want %q", c.in, got, c.want)
		}
	}

	// The generated JS must embed the wanted text as a proper JSON string so a
	// quote in the text cannot break the expression.
	quoted := strconv.Quote(`a "quoted" text`)
	for _, expr := range []string{textExpression(`a "quoted" text`), countExpression(`a "quoted" text`)} {
		if !strings.Contains(expr, quoted) {
			t.Errorf("expression %q does not embed %s", expr, quoted)
		}
		if !strings.Contains(expr, "underShadow") {
			t.Errorf("expression %q does not cross shadow roots", expr)
		}
	}
	if !strings.Contains(cssExpression("#id"), strconv.Quote("#id")) ||
		!strings.Contains(cssExpression("#id"), "querySelector") {
		t.Errorf("cssExpression does not carry the selector: %q", cssExpression("#id"))
	}
}

// nodeByBackend is the bridge from a frame's backend node to a node id in the
// tree built so far; a miss must be "" so the caller can refuse instead of
// grafting onto the wrong node.
func TestNodeByBackend(t *testing.T) {
	nodes := []axNode{
		ax("a", "", "button", "one", 11),
		ax("b", "", "link", "two", 22),
	}
	if got := nodeByBackend(nodes, 22); got != "b" {
		t.Errorf("nodeByBackend(22) = %q, want b", got)
	}
	if got := nodeByBackend(nodes, 99); got != "" {
		t.Errorf("nodeByBackend(missing) = %q, want empty", got)
	}
	if got := nodeByBackend(nil, 1); got != "" {
		t.Errorf("nodeByBackend(nil) = %q, want empty", got)
	}
}
