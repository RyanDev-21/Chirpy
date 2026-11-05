package media

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// The presigned GET example from AWS's Signature Version 4 documentation
// ("Authenticating Requests: Using Query Parameters"): our signer must
// produce exactly the published signature.
func TestSigV4MatchesAWSExample(t *testing.T) {
	s := sigV4{accessKey: "AKIAIOSFODNN7EXAMPLE", secretKey: "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY", region: "us-east-1", service: "s3"}
	now := time.Date(2013, 5, 24, 0, 0, 0, 0, time.UTC)
	u := s.presign("GET", "examplebucket.s3.amazonaws.com", "/test.txt", nil, nil, 86400*time.Second, now)
	const want = "aeeed9bbccd4d02ee5c0109b86d86835f995330da4c265957d157751f604d404"
	if !strings.HasSuffix(u, "X-Amz-Signature="+want) {
		t.Fatalf("signature mismatch:\n%s", u)
	}
	if !strings.Contains(u, "X-Amz-Credential=AKIAIOSFODNN7EXAMPLE%2F20130524%2Fus-east-1%2Fs3%2Faws4_request") {
		t.Fatalf("credential scope: %s", u)
	}
}

// Upload links sign the size and type, so R2 refuses a different file.
func TestPresignPutSignsSizeAndType(t *testing.T) {
	r := NewR2("acct", "bucket", "ak", "sk")
	u, _ := r.PresignPut("m/x", "image/jpeg", 12345, 10*time.Minute)
	if !strings.Contains(u, "X-Amz-SignedHeaders=content-length%3Bcontent-type%3Bhost%3Bif-none-match") || !strings.HasPrefix(u, "https://acct.r2.cloudflarestorage.com/bucket/m/x?") {
		t.Fatalf("put url %s", u)
	}
}

// Download links force a download with the stored type.
func TestPresignGetForcesDownload(t *testing.T) {
	r := NewR2("acct", "bucket", "ak", "sk")
	u, _ := r.PresignGet("m/x", "image/jpeg", "", time.Minute)
	if !strings.Contains(u, "response-content-disposition=attachment") || !strings.Contains(u, "response-content-type=image%2Fjpeg") {
		t.Fatalf("get url %s", u)
	}
	// a file keeps its name, UTF-8 and percent-encoded so it can't break the header
	u, _ = r.PresignGet("m/y", "application/pdf", `รายงาน "Q3"; x.pdf`, time.Minute)
	if !strings.Contains(u, "filename%2A%3DUTF-8%27%27") || strings.Contains(u, `"`) || strings.Contains(u, ";%20x") {
		t.Fatalf("get url %s", u)
	}
}

func TestCleanFileName(t *testing.T) {
	ok := map[string][2]string{
		"report.pdf":             {"application/pdf", "report.pdf"},
		"C:\\Users\\me\\cv.DOCX": {"application/vnd.openxmlformats-officedocument.wordprocessingml.document", "cv.DOCX"},
		"../../etc/song.mp3":     {"audio/mpeg", "song.mp3"},
		"รายงาน ไตรมาส.xlsx":     {"application/vnd.openxmlformats-officedocument.spreadsheetml.sheet", "รายงาน ไตรมาส.xlsx"},
	}
	for in, want := range ok {
		if got, good := cleanFileName(in, want[0]); !good || got != want[1] {
			t.Errorf("%q -> %q %v, want %q", in, got, good, want[1])
		}
	}
	bad := map[string]string{
		"app.apk":              "application/vnd.android.package-archive",
		"setup.exe":            "application/x-msdownload",
		"run.sh":               "text/plain",
		"photo.pdf":            "application/x-msdownload", // type doesn't match the name
		"invoice\u202Efdp.exe": "application/pdf",          // right-to-left override hides .exe
		"noextension":          "text/plain",
		".pdf":                 "application/pdf",
		"":                     "application/pdf",
	}
	for in, ct := range bad {
		if got, good := cleanFileName(in, ct); good {
			t.Errorf("%q (%s) accepted as %q", in, ct, got)
		}
	}
	if _, good := cleanFileName(strings.Repeat("a", 201)+".pdf", "application/pdf"); good {
		t.Error("over-long name accepted")
	}
}

func TestWaveformJSON(t *testing.T) {
	var w Waveform
	if err := json.Unmarshal([]byte(`[0, 12, 255]`), &w); err != nil || string(w) != "\x00\x0c\xff" {
		t.Fatalf("decode %v %v", w, err)
	}
	if out, _ := json.Marshal(w); string(out) != "[0,12,255]" {
		t.Fatalf("encode %s (must be numbers, not base64)", out)
	}
	for _, bad := range []string{`[256]`, `[-1]`, `"AAEC"`, `[` + strings.Repeat("1,", 128) + `1]`} {
		if err := json.Unmarshal([]byte(bad), &w); err == nil {
			t.Errorf("%s accepted", bad)
		}
	}
}
