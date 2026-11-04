package media

import (
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
	u, _ := r.PresignGet("m/x", "image/jpeg", time.Minute)
	if !strings.Contains(u, "response-content-disposition=attachment") || !strings.Contains(u, "response-content-type=image%2Fjpeg") {
		t.Fatalf("get url %s", u)
	}
}
