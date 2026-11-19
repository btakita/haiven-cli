package cli

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
	sjs "github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/haiven-dev/haiven-cli/internal/fakeserver"
	"github.com/haiven-dev/haiven-cli/internal/result"
)

func init() {
	goldenCases["sdk-fake-server-bad-token"] = invocation{args: []string{"sdk", "fake-server", "--token", "only-a-value", "--json"}}
	goldenCases["sdk-fake-server-bad-addr"] = invocation{args: []string{"sdk", "fake-server", "--addr", "no-port", "--json"}}
}

// startFakeServer runs `haiven sdk fake-server` in-process until stop is called,
// and returns its ready document.
func startFakeServer(t *testing.T, extra ...string) (FakeServerData, string, func() ran) {
	t.Helper()
	root := t.TempDir()
	ready := filepath.Join(root, "ready.json")
	args := append([]string{"sdk", "fake-server", "--json", "--ready-file", "ready.json", "--record", "transcript.jsonl"}, extra...)
	ctx, cancel := context.WithCancel(context.Background())
	var stdout, stderr bytes.Buffer
	done := make(chan int, 1)
	go func() {
		done <- Run(ctx, Env{
			Stdout: &stdout, Stderr: &stderr, Getenv: func(string) string { return "" },
			Cwd: root, Version: testVersion, GOOS: "linux", GOARCH: "amd64",
		}, args)
	}()
	deadline := time.Now().Add(5 * time.Second)
	var d FakeServerData
	for {
		raw, err := os.ReadFile(ready)
		if err == nil && json.Unmarshal(raw, &d) == nil {
			break
		}
		select {
		case code := <-done:
			cancel()
			t.Fatalf("fake-server exited %d before ready:\n%s", code, stdout.String())
		default:
		}
		if time.Now().After(deadline) {
			cancel()
			t.Fatal("fake-server never wrote its ready file")
		}
		time.Sleep(10 * time.Millisecond)
	}
	stop := func() ran {
		cancel()
		select {
		case code := <-done:
			return ran{exit: code, stdout: stdout.String(), stderr: stderr.String()}
		case <-time.After(10 * time.Second):
			t.Fatal("fake-server did not stop")
			return ran{}
		}
	}
	t.Cleanup(cancel)
	return d, filepath.Join(root, "transcript.jsonl"), stop
}

type client struct {
	t  *testing.T
	ws *websocket.Conn
}

func dial(t *testing.T, url string) *client {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ws, resp, err := websocket.Dial(ctx, url, &websocket.DialOptions{Subprotocols: []string{fakeserver.Subprotocol}})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Body != nil {
		_ = resp.Body.Close()
	}
	if ws.Subprotocol() != fakeserver.Subprotocol {
		t.Fatalf("subprotocol %q", ws.Subprotocol())
	}
	return &client{t: t, ws: ws}
}

func (c *client) send(frame string) {
	c.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := c.ws.Write(ctx, websocket.MessageText, []byte(frame)); err != nil {
		c.t.Fatal(err)
	}
}

func (c *client) read() string {
	c.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, data, err := c.ws.Read(ctx)
	if err != nil {
		c.t.Fatal(err)
	}
	return string(data)
}

func control(t *testing.T, base, lever, body string) {
	t.Helper()
	resp, err := http.Post(base+lever, "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("%s: HTTP %d", lever, resp.StatusCode)
	}
}

// waitNoOpenSockets polls the stats lever until every socket handler has
// recorded its last transcript entry and left.
func waitNoOpenSockets(t *testing.T, base string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		resp, err := http.Get(base + "stats")
		if err != nil {
			t.Fatal(err)
		}
		var st fakeserver.Stats
		err = json.NewDecoder(resp.Body).Decode(&st)
		_ = resp.Body.Close()
		if err != nil {
			t.Fatal(err)
		}
		if st.Open == 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%d socket(s) still open", st.Open)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestFakeServerServesTheModelAndRecords(t *testing.T) {
	d, transcript, stop := startFakeServer(t, "--token", "good:u1", "--token", "bad:u1:invalid", "--route", "chat.send")
	if !strings.HasSuffix(d.WSURL, fakeserver.WSPath) || !strings.HasPrefix(d.WSURL, "ws://127.0.0.1:") {
		t.Fatalf("ws url %q", d.WSURL)
	}

	a := dial(t, d.WSURL)
	a.send(`{"type":"auth","requestId":"a1","payload":"good"}`)
	if got := a.read(); got != `{"type":"auth.success","requestId":"","success":true}` {
		t.Fatalf("handshake: %s", got)
	}
	a.send(`{"type":"ping","requestId":"p1"}`)
	if got := a.read(); got != `{"type":"pong","requestId":"p1","success":true}` {
		t.Fatalf("ping: %s", got)
	}

	control(t, d.ControlURL, "push?user=u1", `{"type":"chat.message","payload":{"id":"m1"}}`)
	if got := a.read(); got != `{"type":"chat.message","payload":{"id":"m1"}}` {
		t.Fatalf("push: %s", got)
	}

	control(t, d.ControlURL, "expire", "")
	a.send(`{"type":"chat.send","requestId":"c1"}`)
	if got := a.read(); got != `{"type":"auth.expired","requestId":"c1","success":false,"error":"expired_token"}` {
		t.Fatalf("expired: %s", got)
	}
	control(t, d.ControlURL, "push", `[{"type":"chat.message"},{"type":"chat.message"}]`)
	for range 2 {
		if got := a.read(); got != `{"type":"auth.expired","requestId":"","success":false,"error":"expired_token"}` {
			t.Fatalf("pump: %s", got)
		}
	}

	// A rejected token is a 1008 close with no frame.
	b := dial(t, d.WSURL)
	b.send(`{"type":"auth","requestId":"a1","payload":"bad"}`)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	_, _, err := b.ws.Read(ctx)
	cancel()
	if status := websocket.CloseStatus(err); status != websocket.StatusPolicyViolation {
		t.Fatalf("close status %v (%v)", status, err)
	}

	// Clients leave first, so the stats and transcript are deterministic. A
	// returned Close proves only the close handshake: the server's socket
	// library echoes the close frame before its handler records disconnect, so
	// stopping now could still close a and record close instead. Wait until the
	// server itself reports no open sockets.
	_ = a.ws.Close(websocket.StatusNormalClosure, "")
	waitNoOpenSockets(t, d.ControlURL)
	r := stop()
	if r.exit != 0 || r.stderr != "" {
		t.Fatalf("exit %d stderr %q\n%s", r.exit, r.stderr, r.stdout)
	}
	inst, err := sjs.UnmarshalJSON(strings.NewReader(r.stdout))
	if err != nil {
		t.Fatal(err)
	}
	if err := compile(t, SchemaResult).Validate(inst); err != nil {
		t.Fatalf("envelope: %v", err)
	}
	if err := compile(t, "sdk-fake-server").Validate(inst.(map[string]any)["data"]); err != nil {
		t.Fatalf("data: %v", err)
	}
	var env struct {
		Code result.Outcome `json:"code"`
		Data FakeServerData `json:"data"`
	}
	if err := json.Unmarshal([]byte(r.stdout), &env); err != nil {
		t.Fatal(err)
	}
	st := env.Data.Stats
	want := fakeserver.Stats{Connections: 2, ClientFrames: 4, ServerFrames: 6, DroppedInbound: 1, DroppedOutbound: 2, PolicyCloses: 1}
	if env.Code != result.OutcomeOK || st != want {
		t.Fatalf("code %q stats %+v, want %+v", env.Code, st, want)
	}
	if env.Data.Record != "transcript.jsonl" || env.Data.RecordedEntries == 0 {
		t.Fatalf("record %q entries %d", env.Data.Record, env.Data.RecordedEntries)
	}

	// Every transcript line validates, and the events tell the story.
	f, err := os.Open(transcript)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	entrySchema := compile(t, SchemaTranscript)
	var events []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line, err := sjs.UnmarshalJSON(bytes.NewReader(sc.Bytes()))
		if err != nil {
			t.Fatal(err)
		}
		if err := entrySchema.Validate(line); err != nil {
			t.Fatalf("%s: %v", sc.Text(), err)
		}
		var e fakeserver.Entry
		_ = json.Unmarshal(sc.Bytes(), &e)
		events = append(events, string(e.Event))
	}
	if len(events) != env.Data.RecordedEntries {
		t.Fatalf("%d lines, envelope says %d", len(events), env.Data.RecordedEntries)
	}
	got := strings.Join(events, " ")
	wantEvents := "open client server client server server client dropped-inbound server dropped-outbound dropped-outbound server server open client close disconnect"
	if got != wantEvents {
		t.Fatalf("events\n got  %s\n want %s", got, wantEvents)
	}
}

func TestFakeServerControlRejectsAFramelessPush(t *testing.T) {
	d, _, stop := startFakeServer(t)
	defer stop()
	resp, err := http.Post(d.ControlURL+"push", "application/json", strings.NewReader(`{"payload":{}}`))
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("HTTP %d", resp.StatusCode)
	}
	if len(d.Tokens) != 1 || d.Tokens[0].Value != DefaultFakeToken {
		t.Fatalf("default tokens %+v", d.Tokens)
	}
}

func TestFakeServerHumanModeAnnouncesTheSocket(t *testing.T) {
	root := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	var stdout, stderr syncBuffer
	done := make(chan int, 1)
	go func() {
		done <- Run(ctx, Env{Stdout: &stdout, Stderr: &stderr, Getenv: func(string) string { return "" }, Cwd: root, Version: testVersion}, []string{"sdk", "fake-server"})
	}()
	deadline := time.Now().Add(5 * time.Second)
	for !strings.Contains(stdout.String(), "listening on ws://") {
		if time.Now().After(deadline) {
			cancel()
			t.Fatal("no announcement")
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	if code := <-done; code != 0 || !strings.Contains(stdout.String(), "stopped: 0 connection(s)") {
		t.Fatalf("exit %d\n%s", code, stdout.String())
	}
}

// syncBuffer is a bytes.Buffer safe to read while the command writes.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}
