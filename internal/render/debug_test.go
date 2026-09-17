package render

import (
	"bytes"
	"context"
	"errors"
	"log"
	"strings"
	"testing"

	"github.com/AndersonJ293/axscope/internal/cdp"
	"github.com/AndersonJ293/axscope/internal/dom"
)

// failingPresenter fails every call, like the page rejecting the overlay script.
type failingPresenter struct{}

func (failingPresenter) Install(context.Context, *cdp.Client, string) error {
	return errors.New("install boom")
}
func (failingPresenter) MoveCursor(context.Context, *cdp.Client, string, float64, float64) error {
	return errors.New("cursor boom")
}
func (failingPresenter) PressCursor(context.Context, *cdp.Client, string, float64, float64, string) error {
	return errors.New("press boom")
}
func (failingPresenter) Spotlight(context.Context, *cdp.Client, string, *dom.Rect) error {
	return errors.New("spotlight boom")
}
func (failingPresenter) SetHUD(context.Context, *cdp.Client, string, string, string) error {
	return errors.New("hud boom")
}

func captureLog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	prev, flags := log.Writer(), log.Flags()
	log.SetOutput(&buf)
	log.SetFlags(0)
	t.Cleanup(func() {
		log.SetOutput(prev)
		log.SetFlags(flags)
	})
	return &buf
}

// The wrapper must keep the error for the caller (the domain still ignores it)
// and write it to the log when asked.
func TestLoggedReportsFailuresWhenDebug(t *testing.T) {
	t.Setenv("AXSCOPE_DEBUG", "1")
	buf := captureLog(t)
	l := Logged{failingPresenter{}}
	if err := l.MoveCursor(context.Background(), nil, "s", 1, 2); err == nil {
		t.Fatal("Logged must return the presenter error")
	}
	if err := l.SetHUD(context.Background(), nil, "s", "1/1", "click"); err == nil {
		t.Fatal("Logged must return the presenter error")
	}
	got := buf.String()
	for _, want := range []string{"presenter cursor: cursor boom", "presenter hud: hud boom"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in %q", want, got)
		}
	}
}

// Without AXSCOPE_DEBUG the behavior is exactly the old one: silent.
func TestLoggedSilentByDefault(t *testing.T) {
	t.Setenv("AXSCOPE_DEBUG", "")
	buf := captureLog(t)
	l := Logged{failingPresenter{}}
	_ = l.Install(context.Background(), nil, "s")
	_ = l.PressCursor(context.Background(), nil, "s", 1, 2, "left")
	_ = l.Spotlight(context.Background(), nil, "s", nil)
	if buf.Len() != 0 {
		t.Errorf("logged without AXSCOPE_DEBUG: %q", buf.String())
	}
}
