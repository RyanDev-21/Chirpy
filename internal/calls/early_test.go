package calls

import (
	"encoding/json"
	"strings"
	"testing"
)

const (
	hostCand  = "candidate:1 1 udp 2122260223 192.168.1.23 51000 typ host generation 0"
	srflxCand = "candidate:2 1 udp 1686052607 171.96.138.164 51000 typ srflx raddr 192.168.1.23 rport 51000 generation 0"
	relayCand = "candidate:3 1 udp 41885439 104.30.1.2 3478 typ relay raddr 171.96.138.164 rport 51000 generation 0"
)

// No address of the phone itself may pass while ringing.
var leaks = []string{"192.168.1.23", "171.96.138.164"}

func noLeak(t *testing.T, s string) {
	t.Helper()
	for _, ip := range leaks {
		if strings.Contains(s, ip) {
			t.Fatalf("leaked %s in %s", ip, s)
		}
	}
}

func cand(c string) json.RawMessage {
	b, _ := json.Marshal(map[string]any{"type": "candidate", "candidate": map[string]any{"candidate": c, "sdpMid": "0", "sdpMLineIndex": 0}})
	return b
}

func TestRelayOnlyCandidates(t *testing.T) {
	for _, c := range []string{hostCand, srflxCand} {
		if _, keep, err := relayOnly(cand(c)); keep || err != nil {
			t.Fatalf("%q: keep %v, err %v", c, keep, err)
		}
	}
	out, keep, err := relayOnly(cand(relayCand))
	if !keep || err != nil {
		t.Fatalf("relay: keep %v, err %v", keep, err)
	}
	noLeak(t, string(out))
	if !strings.Contains(string(out), "104.30.1.2 3478 typ relay raddr 0.0.0.0 rport 0") || !strings.Contains(string(out), `"sdpMid":"0"`) {
		t.Fatalf("relay candidate %s", out)
	}
	if _, keep, err := relayOnly(cand("")); !keep || err != nil { // end of candidates
		t.Fatalf("end of candidates: keep %v, err %v", keep, err)
	}
	for _, bad := range []string{`{"type":"bye"}`, `{"type":"candidate"}`, `{"type":"offer"}`, `[1]`, `"x"`} {
		if _, _, err := relayOnly(json.RawMessage(bad)); err == nil {
			t.Fatalf("%s accepted", bad)
		}
	}
	// extra fields don't ride along
	out, _, _ = relayOnly(json.RawMessage(`{"type":"candidate","candidate":{"candidate":"` + relayCand + `"},"note":"171.96.138.164"}`))
	noLeak(t, string(out))
}

func TestRelayOnlySDP(t *testing.T) {
	sdp := strings.Join([]string{
		"v=0",
		"o=- 4611731400430051336 2 IN IP4 171.96.138.164",
		"s=-",
		"m=audio 51000 UDP/TLS/RTP/SAVPF 111",
		"c=IN IP4 171.96.138.164",
		"a=rtcp:51000 IN IP4 171.96.138.164",
		"a=" + hostCand,
		"a=" + srflxCand,
		"a=" + relayCand,
		"a=end-of-candidates",
		"c=IN IP6 2001:db8::1",
		"",
	}, "\r\n")
	raw, _ := json.Marshal(map[string]string{"type": "offer", "sdp": sdp})
	out, keep, err := relayOnly(raw)
	if !keep || err != nil {
		t.Fatalf("keep %v, err %v", keep, err)
	}
	var got struct{ Type, SDP string }
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatal(err)
	}
	noLeak(t, got.SDP)
	if strings.Contains(got.SDP, "2001:db8") {
		t.Fatalf("leaked IPv6: %s", got.SDP)
	}
	for _, want := range []string{"o=- 4611731400430051336 2 IN IP4 127.0.0.1\r\n", "c=IN IP4 0.0.0.0\r\n", "a=rtcp:9 IN IP4 0.0.0.0\r\n",
		"typ relay raddr 0.0.0.0 rport 0", "a=end-of-candidates\r\n", "m=audio 51000 UDP/TLS/RTP/SAVPF 111\r\n"} {
		if !strings.Contains(got.SDP, want) {
			t.Fatalf("missing %q in\n%s", want, got.SDP)
		}
	}
	if got.Type != "offer" || strings.Count(got.SDP, "a=candidate:") != 1 {
		t.Fatalf("got %s", got.SDP)
	}
}

func FuzzRelayOnly(f *testing.F) {
	f.Add([]byte(cand(srflxCand)))
	f.Add([]byte(`{"type":"answer","sdp":"c=IN IP4 171.96.138.164\r\na=` + hostCand + `"}`))
	f.Fuzz(func(t *testing.T, data []byte) {
		out, keep, err := relayOnly(data)
		if err == nil && keep {
			var s earlySignal
			if json.Unmarshal(out, &s) != nil {
				t.Fatalf("invalid output %s", out)
			}
			if s.Candidate != nil && s.Candidate.Candidate != "" && !isRelay(s.Candidate.Candidate) {
				t.Fatalf("non-relay candidate passed: %s", out)
			}
		}
	})
}
