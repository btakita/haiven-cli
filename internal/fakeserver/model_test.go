package fakeserver

import (
	"encoding/json"
	"reflect"
	"testing"
)

// These transcripts are the JS model's (TypeScript SDK test/auth.test.ts)
// expressed at the wire: what a client that sends these frames receives.

var (
	good    = Token{Value: "good-token", UserID: "user-1", Valid: true}
	other   = Token{Value: "other-token", UserID: "user-2", Valid: true}
	bad     = Token{Value: "bad-token", UserID: "user-1"}
	lapsed  = Token{Value: "lapsed-token", UserID: "user-1", Valid: true, Expired: true}
	allToks = []Token{good, other, bad, lapsed}
)

type peer struct {
	got         []ServerFrame
	closeCode   int
	closeReason string
	// onFrame lets a test answer from inside delivery, as a JS client does
	// from onmessage.
	onFrame func(ServerFrame)
}

func (p *peer) Deliver(f ServerFrame) {
	p.got = append(p.got, f)
	if p.onFrame != nil {
		p.onFrame(f)
	}
}

func (p *peer) Close(code int, reason string) { p.closeCode, p.closeReason = code, reason }

func (p *peer) wire(t *testing.T) []string {
	t.Helper()
	out := make([]string, len(p.got))
	for i, f := range p.got {
		b, err := json.Marshal(f)
		if err != nil {
			t.Fatal(err)
		}
		out[i] = string(b)
	}
	return out
}

func frame(typ, id string, payload any) []byte {
	m := map[string]any{"type": typ, "requestId": id}
	if payload != nil {
		m["payload"] = payload
	}
	b, _ := json.Marshal(m)
	return b
}

func open(t *testing.T, opts Options, token string) (*Socket, *peer) {
	t.Helper()
	if opts.Tokens == nil {
		opts.Tokens = allToks
	}
	p := &peer{}
	s := NewSocket(opts, p, nil)
	s.Receive(frame(TypeAuth, "a1", token))
	return s, p
}

func expectWire(t *testing.T, p *peer, want ...string) {
	t.Helper()
	got := p.wire(t)
	if len(want) == 0 {
		want = []string{}
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("wire mismatch\n got  %q\n want %q", got, want)
	}
}

func TestHandshakeRepliesWithEmptyRequestIDAndNothingElse(t *testing.T) {
	_, p := open(t, Options{}, good.Value)
	expectWire(t, p, `{"type":"auth.success","requestId":"","success":true}`)
}

func TestRejectedTokenClosesWithPolicyViolationAndNoFrame(t *testing.T) {
	for _, tok := range []string{bad.Value, "unknown"} {
		s, p := open(t, Options{}, tok)
		expectWire(t, p)
		if p.closeCode != StatusPolicyViolation || !s.Closed() {
			t.Fatalf("%s: close %d", tok, p.closeCode)
		}
	}
	// A first frame that is not auth closes too.
	p := &peer{}
	s := NewSocket(Options{Tokens: allToks}, p, nil)
	s.Receive(frame(TypePing, "p1", nil))
	if p.closeCode != StatusPolicyViolation {
		t.Fatalf("close %d", p.closeCode)
	}
}

func TestLapsedTokenAuthenticatesIntoAnExpiredSession(t *testing.T) {
	s, p := open(t, Options{}, lapsed.Value)
	expectWire(t, p, `{"type":"auth.expired","requestId":"","success":false,"error":"expired_token"}`)
	if len(s.DroppedOutbound) != 0 {
		t.Fatal("nothing is dropped on the handshake")
	}
}

func TestPingIsAnsweredWithCorrelatedPong(t *testing.T) {
	s, p := open(t, Options{}, good.Value)
	s.Receive(frame(TypePing, "p1", nil))
	s.Receive(frame("chat.send", "c1", map[string]string{"content": "hi"}))
	expectWire(t, p,
		`{"type":"auth.success","requestId":"","success":true}`,
		`{"type":"pong","requestId":"p1","success":true}`,
		`{"type":"chat.send","requestId":"c1","success":false,"error":"unknown_message_type"}`,
	)
}

func TestExpiredSessionNamesTheRefusedRequest(t *testing.T) {
	s, p := open(t, Options{Routes: []string{"chat.send"}}, good.Value)
	s.ExpireSession()
	s.Receive(frame("chat.send", "c1", nil))
	expectWire(t, p,
		`{"type":"auth.success","requestId":"","success":true}`,
		`{"type":"auth.expired","requestId":"c1","success":false,"error":"expired_token"}`,
	)
	if len(s.DroppedInbound) != 1 || len(s.DroppedOutbound) != 0 {
		t.Fatalf("dropped in %d out %d", len(s.DroppedInbound), len(s.DroppedOutbound))
	}
	// In-band re-auth recovers without a reconnect.
	s.Receive(frame(TypeAuth, "a2", good.Value))
	s.Receive(frame("chat.send", "c1", nil))
	got := p.wire(t)
	want := []string{
		`{"type":"auth.success","requestId":"a2","success":true}`,
		`{"type":"chat.send","requestId":"c1","success":true}`,
	}
	if !reflect.DeepEqual(got[2:], want) {
		t.Fatalf("got %q", got[2:])
	}
}

func TestFanOutBurstWhileExpiredIsReplacedOnePerFrame(t *testing.T) {
	s, p := open(t, Options{}, good.Value)
	s.ExpireSession()
	var pushes []ServerFrame
	for _, id := range []string{"m1", "m2", "m3"} {
		pushes = append(pushes, ServerFrame{Type: "chat.message", Payload: json.RawMessage(`{"id":"` + id + `"}`)})
	}
	s.Push(pushes...)
	if len(s.DroppedOutbound) != 3 {
		t.Fatalf("dropped %d", len(s.DroppedOutbound))
	}
	expired := `{"type":"auth.expired","requestId":"","success":false,"error":"expired_token"}`
	expectWire(t, p, `{"type":"auth.success","requestId":"","success":true}`, expired, expired, expired)
}

func TestReauthFailures(t *testing.T) {
	cases := map[string]struct {
		payload any
		want    string
	}{
		"not a string":  {42, `{"type":"auth.failure","requestId":"a2","success":false,"error":"invalid_payload"}`},
		"unknown token": {"nope", `{"type":"auth.failure","requestId":"a2","success":false,"error":"invalid_payload"}`},
		"rejected":      {bad.Value, `{"type":"auth.failure","requestId":"a2","success":false,"error":"invalid_token"}`},
		"other user":    {other.Value, `{"type":"auth.failure","requestId":"a2","success":false,"error":"unauthorized"}`},
		"still lapsed":  {lapsed.Value, `{"type":"auth.expired","requestId":"a2","success":false,"error":"expired_token"}`},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			s, p := open(t, Options{}, good.Value)
			s.Receive(frame(TypeAuth, "a2", c.payload))
			if got := p.wire(t); got[len(got)-1] != c.want {
				t.Fatalf("got %s", got[len(got)-1])
			}
		})
	}
}

func TestRateLimitRunsBeforeTheExpiryGate(t *testing.T) {
	s, p := open(t, Options{RateLimited: []string{"chat.send"}}, good.Value)
	s.Receive(frame("chat.send", "c0", nil))
	if got := p.wire(t)[1]; got != `{"type":"chat.send","requestId":"c0","success":false,"error":"rate_limited"}` {
		t.Fatalf("got %s", got)
	}
	// Expired: the gate still answers first, so the read loop does not
	// count the frame as refused for expiry; its answer is content, so the
	// pump replaces it.
	s.ExpireSession()
	s.Receive(frame("chat.send", "c1", nil))
	if len(s.DroppedInbound) != 0 || len(s.DroppedOutbound) != 1 || s.DroppedOutbound[0].Error != ErrRateLimited {
		t.Fatalf("dropped in %+v out %+v", s.DroppedInbound, s.DroppedOutbound)
	}
}

func TestDeferredReplyMeetsALapsedSession(t *testing.T) {
	s, p := open(t, Options{Routes: []string{"chat.send"}, Deferred: []string{"chat.send"}}, good.Value)
	s.Receive(frame("chat.send", "c1", nil))
	if len(p.got) != 1 {
		t.Fatal("a deferred request is not answered inline")
	}
	s.ExpireSession()
	s.AnswerDeferred()
	if len(s.DroppedOutbound) != 1 || *s.DroppedOutbound[0].RequestID != "c1" {
		t.Fatalf("dropped %+v", s.DroppedOutbound)
	}
	s.ReportDropped(DroppedFrames{Count: 1, Types: []TypeCount{{Type: "chat.send", Count: 1}}, RequestIDs: []string{"c1"}, Truncated: false})
	got := p.wire(t)
	if want := `{"type":"auth.expired.dropped","requestId":"","success":false,"payload":{"count":1,"types":[{"type":"chat.send","count":1}],"requestIds":["c1"],"truncated":false},"error":"expired_token"}`; got[len(got)-1] != want {
		t.Fatalf("got %s", got[len(got)-1])
	}
}

// A client that answers from inside delivery must not reorder the
// transcript: the frames of the current server step are written first.
func TestReentrantClientKeepsServerStepOrder(t *testing.T) {
	p := &peer{}
	s := NewSocket(Options{Tokens: allToks}, p, nil)
	sent := false
	p.onFrame = func(f ServerFrame) {
		if f.Type == TypeAuthSuccess && !sent {
			sent = true
			s.Receive(frame(TypePing, "p1", nil))
		}
	}
	s.Receive(frame(TypeAuth, "a1", good.Value))
	expectWire(t, p,
		`{"type":"auth.success","requestId":"","success":true}`,
		`{"type":"pong","requestId":"p1","success":true}`,
	)
}
