package proxy

import (
	"bufio"
	"encoding/json"
	"io"
	"strings"
)

// SSEEvent holds the text from one DeepSeek SSE line, split into the answer and the
// thinking (reasoning) text, plus the response message ID when the line carries one.
type SSEEvent struct {
	Content           string
	Reasoning         string
	ResponseMessageID interface{} // number or string
}

type fragment struct {
	ID      int    `json:"id"`
	Type    string `json:"type"`
	Content string `json:"content"`
}

// Parser follows DeepSeek's chat stream, which works like a JSON patch log:
//
//	{"v":{"response":{"fragments":[{"type":"RESPONSE","content":"The"}]}}}   snapshot
//	{"p":"response/fragments/-1/content","o":"APPEND","v":" quick"}          append to the last fragment
//	{"v":" brown"}                                                          no "p": same path as before
//	{"p":"response/fragments","v":[{"id":2,"type":"THINK",...},{"id":3,"type":"RESPONSE",...}]}
//
// "-1" means the last fragment, not the chat title (that arrives as a separate
// "event: title" line). THINK fragments are reasoning; RESPONSE fragments are the answer.
type Parser struct {
	includeThinking bool
	path            string
	lastType        string
	seen            map[int]bool
}

func NewParser(includeThinking bool) *Parser {
	return &Parser{includeThinking: includeThinking, seen: map[int]bool{}}
}

type deepSeekSSE struct {
	P             *string         `json:"p"`
	V             json.RawMessage `json:"v"`
	ResponseMsgID interface{}     `json:"response_message_id"`
}

// Line parses one stream line. It returns nil for lines that carry nothing useful.
func (p *Parser) Line(line string) *SSEEvent {
	line = strings.TrimSpace(line)
	if !strings.HasPrefix(line, "data:") {
		return nil
	}
	payload := strings.TrimSpace(line[5:])
	if payload == "" || payload == "[DONE]" {
		return nil
	}
	var d deepSeekSSE
	if err := json.Unmarshal([]byte(payload), &d); err != nil {
		return nil
	}
	evt := &SSEEvent{ResponseMessageID: d.ResponseMsgID}
	if d.P != nil {
		p.path = *d.P
	}
	if len(d.V) == 0 {
		return evt
	}

	switch {
	case d.P == nil && d.V[0] == '{':
		// Snapshot of the whole response.
		var snap struct {
			Response struct {
				MessageID interface{} `json:"message_id"`
				Fragments []fragment  `json:"fragments"`
			} `json:"response"`
		}
		if json.Unmarshal(d.V, &snap) == nil {
			if snap.Response.MessageID != nil {
				evt.ResponseMessageID = snap.Response.MessageID
			}
			p.path = ""
			p.addFragments(evt, snap.Response.Fragments)
		}
	case p.path == "response/fragments" && d.V[0] == '[':
		var frags []fragment
		if json.Unmarshal(d.V, &frags) == nil {
			p.addFragments(evt, frags)
		}
	case strings.HasPrefix(p.path, "response/fragments/") && strings.HasSuffix(p.path, "/content") && d.V[0] == '"':
		var s string
		if json.Unmarshal(d.V, &s) == nil {
			p.emit(evt, p.lastType, s)
		}
	}
	return evt
}

// addFragments emits the content of fragments not seen before. DeepSeek re-sends
// finished fragments (for example the full THINK text) when it starts a new one.
func (p *Parser) addFragments(evt *SSEEvent, frags []fragment) {
	for _, f := range frags {
		p.lastType = f.Type
		if f.ID != 0 && p.seen[f.ID] {
			continue
		}
		if f.ID != 0 {
			p.seen[f.ID] = true
		}
		p.emit(evt, f.Type, f.Content)
	}
}

func (p *Parser) emit(evt *SSEEvent, fragType, text string) {
	switch strings.ToUpper(fragType) {
	case "THINK", "THINKING":
		if p.includeThinking {
			evt.Reasoning += text
		}
	case "RESPONSE", "":
		evt.Content += text
	}
}

// CollectFull reads a whole DeepSeek stream and returns the answer, the reasoning
// text and the last response message ID.
func CollectFull(r io.Reader, includeThinking bool) (text, reasoning string, msgID interface{}) {
	p := NewParser(includeThinking)
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024) // 1 MB line buffer for long responses
	for scanner.Scan() {
		evt := p.Line(scanner.Text())
		if evt == nil {
			continue
		}
		if evt.ResponseMessageID != nil {
			msgID = evt.ResponseMessageID
		}
		text += evt.Content
		reasoning += evt.Reasoning
	}
	return
}
