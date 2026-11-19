package fakeserver

import (
	"bufio"
	"encoding/json"
	"io"
	"sync"

	"github.com/invopop/jsonschema"
)

// TranscriptSchemaID is the "schema" of a recorded transcript's entries.
const TranscriptSchemaID = "haiven.dev/fake-server-transcript/v1"

// Event is one transcript entry's kind.
type Event string

// The closed transcript event vocabulary.
const (
	EventOpen            Event = "open"
	EventClient          Event = "client"
	EventServer          Event = "server"
	EventDroppedInbound  Event = "dropped-inbound"
	EventDroppedOutbound Event = "dropped-outbound"
	EventClose           Event = "close"
	EventDisconnect      Event = "disconnect"
)

// Events lists every Event.
func Events() []Event {
	return []Event{EventOpen, EventClient, EventServer, EventDroppedInbound, EventDroppedOutbound, EventClose, EventDisconnect}
}

// JSONSchema publishes the closed vocabulary as a JSON-Schema enum.
func (Event) JSONSchema() *jsonschema.Schema {
	enum := make([]any, 0, len(Events()))
	for _, e := range Events() {
		enum = append(enum, string(e))
	}
	return &jsonschema.Schema{
		Type: "string",
		Enum: enum,
		Description: "open: a socket was accepted. client/server: a frame on the wire. " +
			"dropped-inbound: a client frame the read loop refused for an expired session. " +
			"dropped-outbound: a server frame the write pump discarded for an expired session (the client saw auth.expired instead). " +
			"close: the server closed the socket (code, reason). disconnect: the client went away first.",
	}
}

// Entry is one line of a --record transcript (JSON Lines). Entries carry no
// timestamps, so a transcript of a deterministic client is byte-stable.
type Entry struct {
	Schema string          `json:"schema" jsonschema_description:"Always haiven.dev/fake-server-transcript/v1."`
	Seq    int             `json:"seq" jsonschema:"minimum=1" jsonschema_description:"Position in the transcript, across every connection."`
	Conn   int             `json:"conn" jsonschema:"minimum=1" jsonschema_description:"Connection number, in accept order."`
	Event  Event           `json:"event"`
	Frame  json.RawMessage `json:"frame,omitempty" jsonschema_description:"The frame, for client, server and dropped events. A client text message that is not JSON is recorded as a string."`
	Code   int             `json:"code,omitempty" jsonschema_description:"WebSocket close code, for close."`
	Reason string          `json:"reason,omitempty"`
}

// Recorder appends entries to a JSON Lines writer. A nil Recorder records
// nothing.
type Recorder struct {
	mu     sync.Mutex
	w      *bufio.Writer
	c      io.Closer
	seq    int
	closed bool
	err    error
}

// NewRecorder records to w, closing it on Close when it is an io.Closer.
func NewRecorder(w io.Writer) *Recorder {
	r := &Recorder{w: bufio.NewWriter(w)}
	if c, ok := w.(io.Closer); ok {
		r.c = c
	}
	return r
}

// Record appends one entry, numbering it.
func (r *Recorder) Record(e Entry) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed || r.err != nil {
		return
	}
	r.seq++
	e.Seq = r.seq
	e.Schema = TranscriptSchemaID
	line, err := json.Marshal(e)
	if err != nil {
		r.err = err
		return
	}
	if _, err := r.w.Write(append(line, '\n')); err != nil {
		r.err = err
		return
	}
	// Flush per entry so a transcript survives a killed process.
	r.err = r.w.Flush()
}

// Entries is the number of entries recorded.
func (r *Recorder) Entries() int {
	if r == nil {
		return 0
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.seq
}

// Close flushes and closes; later Records are dropped. It returns the first
// write error.
func (r *Recorder) Close() error {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return r.err
	}
	r.closed = true
	if err := r.w.Flush(); err != nil && r.err == nil {
		r.err = err
	}
	if r.c != nil {
		if err := r.c.Close(); err != nil && r.err == nil {
			r.err = err
		}
	}
	return r.err
}
