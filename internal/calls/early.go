package calls

import (
	"encoding/json"
	"errors"
	"strings"
)

// Before a call is answered, the phones may already set up the WebRTC
// connection, but only through the TURN relay: no ICE candidate may carry
// a device's own address (host: its Wi-Fi address; srflx: its public IP,
// which gives away the internet provider, the rough area and that the
// phone is online). relayOnly enforces that on every signal relayed while
// ringing, so a caller who rings and hangs up learns nothing about where
// the callee is, whatever the apps do.

var errBadSignal = errors.New("bad signal")

type earlyCandidate struct {
	Candidate     string  `json:"candidate"`
	SdpMid        *string `json:"sdpMid,omitempty"`
	SdpMLineIndex *int    `json:"sdpMLineIndex,omitempty"`
}

type earlySignal struct {
	Type      string          `json:"type"`
	SDP       *string         `json:"sdp,omitempty"`
	Candidate *earlyCandidate `json:"candidate,omitempty"`
}

// relayOnly returns the signal to relay while ringing. keep is false for a
// candidate that isn't a relay one (dropped quietly: it would reveal an
// address). Anything other than an offer, an answer or a candidate is an
// error.
func relayOnly(data json.RawMessage) (out json.RawMessage, keep bool, err error) {
	var s earlySignal
	if json.Unmarshal(data, &s) != nil {
		return nil, false, errBadSignal
	}
	switch s.Type {
	case "offer", "answer":
		if s.SDP == nil {
			return nil, false, errBadSignal
		}
		sdp := scrubSDP(*s.SDP)
		out, err = json.Marshal(earlySignal{Type: s.Type, SDP: &sdp})
	case "candidate":
		if s.Candidate == nil {
			return nil, false, errBadSignal
		}
		c := *s.Candidate
		if c.Candidate != "" { // "" marks the end of candidates
			if !isRelay(c.Candidate) {
				return nil, false, nil
			}
			c.Candidate = scrubRelated(c.Candidate)
		}
		out, err = json.Marshal(earlySignal{Type: "candidate", Candidate: &c})
	default:
		return nil, false, errBadSignal
	}
	return out, err == nil, err
}

// isRelay reports whether an ICE candidate line is "typ relay".
func isRelay(candidate string) bool {
	f := strings.Fields(candidate)
	for i := 0; i+1 < len(f); i++ {
		if f[i] == "typ" {
			return f[i+1] == "relay"
		}
	}
	return false
}

// scrubRelated blanks a candidate's related address: on a relay candidate
// "raddr" is the phone's public IP as the TURN server saw it. It's only
// informational; connections don't need it.
func scrubRelated(candidate string) string {
	f := strings.Fields(candidate)
	for i := 0; i+1 < len(f); i++ {
		switch f[i] {
		case "raddr":
			f[i+1] = "0.0.0.0"
		case "rport":
			f[i+1] = "0"
		}
	}
	return strings.Join(f, " ")
}

// scrubSDP keeps only relay candidates in an SDP and blanks every other
// place an address can appear (connection lines, RTCP, origin).
func scrubSDP(sdp string) string {
	lines := strings.SplitAfter(sdp, "\n")
	out := make([]string, 0, len(lines))
	for _, raw := range lines {
		line := strings.TrimRight(raw, "\r\n")
		end := raw[len(line):] // keep the original line ending
		switch {
		case strings.HasPrefix(line, "a=candidate:"):
			if !isRelay(line) {
				continue
			}
			line = scrubRelated(line)
		case strings.HasPrefix(line, "c=IN "):
			line = "c=IN IP4 0.0.0.0"
		case strings.HasPrefix(line, "a=rtcp:") && strings.Contains(line, " IN "):
			line = "a=rtcp:9 IN IP4 0.0.0.0"
		case strings.HasPrefix(line, "o="):
			if f := strings.Fields(line); len(f) == 6 {
				f[4], f[5] = "IP4", "127.0.0.1"
				line = strings.Join(f, " ")
			}
		}
		out = append(out, line+end)
	}
	return strings.Join(out, "")
}
