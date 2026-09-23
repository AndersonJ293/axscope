package browser

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/AndersonJ293/axscope/internal/cdp"
	"github.com/AndersonJ293/axscope/internal/dom"
)

// nopPresenter satisfies the drawing port without touching a browser.
type nopPresenter struct{}

func (nopPresenter) Install(context.Context, *cdp.Client, string) error { return nil }
func (nopPresenter) MoveCursor(context.Context, *cdp.Client, string, float64, float64) error {
	return nil
}
func (nopPresenter) PressCursor(context.Context, *cdp.Client, string, float64, float64, string) error {
	return nil
}
func (nopPresenter) Spotlight(context.Context, *cdp.Client, string, *dom.Rect) error { return nil }
func (nopPresenter) SetHUD(context.Context, *cdp.Client, string, string, string) error {
	return nil
}

// fakeCDP stands in for a browser: it answers the discovery and attach the
// session bootstrap needs. No real target or page is involved.
func fakeCDP(t *testing.T) *cdp.Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
		if err != nil {
			return
		}
		go serveFakeCDP(conn)
	}))
	t.Cleanup(srv.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(srv.URL, "http"), nil)
	if err != nil {
		t.Fatalf("dialing the fake CDP: %v", err)
	}
	client := cdp.FromConn(conn)
	t.Cleanup(client.Close)
	return client
}

func serveFakeCDP(conn *websocket.Conn) {
	defer conn.Close(websocket.StatusNormalClosure, "")
	ctx := context.Background()
	for {
		_, data, err := conn.Read(ctx)
		if err != nil {
			return
		}
		var req struct {
			ID     *int64 `json:"id"`
			Method string `json:"method"`
		}
		if json.Unmarshal(data, &req) != nil || req.ID == nil {
			continue
		}
		result := map[string]any{}
		switch req.Method {
		case "Target.getTargets":
			result = map[string]any{"targetInfos": []map[string]any{
				{"targetId": "1", "type": "page", "url": "about:blank", "title": ""},
			}}
		case "Target.attachToTarget":
			// Answer after a delay, so a canceled context is the case the send
			// waits on and the failure is deterministic (not a select race).
			time.Sleep(100 * time.Millisecond)
			result = map[string]any{"sessionId": "s1"}
		}
		out, _ := json.Marshal(map[string]any{"id": *req.ID, "result": result})
		_ = conn.Write(ctx, websocket.MessageText, out)
	}
}

// Regression: the session outlives the request that built it. The daemon
// cancels the request's context when the client leaves (an MCP cancellation, a
// Ctrl-C); a tab attached on a later command must still attach. When the
// session stored that request context, every attach after the first command
// failed with "failed to acquire lock: context canceled" — newtab, tab and open
// on a fresh target all died, while acting on the already-attached tab worked.
func TestAttachSurvivesRequestCancel(t *testing.T) {
	client := fakeCDP(t)

	reqCtx, cancel := context.WithCancel(context.Background())
	sess, err := NewSession(reqCtx, client, false, nopPresenter{})
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	defer sess.Close()

	// The command that built the session returned and the client is gone.
	cancel()

	tab, created := sess.rememberNew("2", "about:blank", "")
	if !created {
		t.Fatal("the new target was not registered")
	}
	sess.attachIfNeeded(tab)
	select {
	case <-tab.ready:
	case <-time.After(5 * time.Second):
		t.Fatal("attaching a tab after the request ended did not finish")
	}
	if tab.initErr != nil {
		t.Fatalf("attaching a tab after the request ended: %v", tab.initErr)
	}
	if tab.SessionID == "" {
		t.Fatal("the tab was not attached")
	}
}
