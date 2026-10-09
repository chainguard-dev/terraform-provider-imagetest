package ec2

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
)

const ec2ErrorResponse = `<?xml version="1.0" encoding="UTF-8"?>
<Response><Errors><Error><Code>UnauthorizedOperation</Code><Message>denied by test</Message></Error></Errors><RequestID>test</RequestID></Response>`

// fakeEC2 answers the EC2 query API actions keyPair uses. Actions listed in
// fail get a non-retryable error response.
func fakeEC2(t *testing.T, fail map[string]bool) *ec2.Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Errorf("parsing request form: %v", err)
		}
		action := r.Form.Get("Action")
		if fail[action] {
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(ec2ErrorResponse))
			return
		}
		switch action {
		case "ImportKeyPair":
			_, _ = w.Write([]byte(`<ImportKeyPairResponse xmlns="http://ec2.amazonaws.com/doc/2016-11-15/"><requestId>test</requestId><keyName>test-key</keyName>` +
				`<keyFingerprint>fp</keyFingerprint><keyPairId>key-0123456789abcdef0</keyPairId></ImportKeyPairResponse>`))
		case "DeleteKeyPair":
			_, _ = w.Write([]byte(`<DeleteKeyPairResponse xmlns="http://ec2.amazonaws.com/doc/2016-11-15/"><requestId>test</requestId><return>true</return></DeleteKeyPairResponse>`))
		default:
			t.Errorf("unexpected EC2 action %q", action)
			w.WriteHeader(http.StatusBadRequest)
		}
	}))
	t.Cleanup(srv.Close)

	return ec2.NewFromConfig(aws.Config{
		Region:           "us-east-1",
		Credentials:      credentials.NewStaticCredentialsProvider("test", "test", ""),
		RetryMaxAttempts: 1,
	}, func(o *ec2.Options) {
		o.BaseEndpoint = aws.String(srv.URL)
	})
}

// leftovers lists the entries in dir, which the test points TMPDIR at.
func leftovers(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("reading %s: %v", dir, err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

// TestKeyPairPrivateKeyCleanup checks that the private key written to the
// temp dir never outlives the key pair: it is removed when create fails and
// on teardown whether or not deleting the AWS key pair succeeds.
func TestKeyPairPrivateKeyCleanup(t *testing.T) {
	tests := []struct {
		name            string
		fail            map[string]bool
		wantCreateErr   bool
		wantTeardownErr bool
	}{{
		name: "teardown removes key file after AWS delete succeeds",
	}, {
		name:            "teardown removes key file even when AWS delete fails",
		fail:            map[string]bool{"DeleteKeyPair": true},
		wantTeardownErr: true,
	}, {
		name:          "failed import leaves no key file",
		fail:          map[string]bool{"ImportKeyPair": true},
		wantCreateErr: true,
	}}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tmp := t.TempDir()
			t.Setenv("TMPDIR", tmp)
			ctx := context.Background()

			k := &keyPair{client: fakeEC2(t, tc.fail), name: "imagetest-ec2-test-key"}
			teardown, err := k.create(ctx)
			if (err != nil) != tc.wantCreateErr {
				t.Fatalf("create(fail=%v): got err=%v, want error=%t", tc.fail, err, tc.wantCreateErr)
			}

			if err == nil {
				// While the key pair exists the private key must be on disk
				// (docker's ssh connection helper reads it) and private.
				if filepath.Dir(k.path) != tmp {
					t.Errorf("key path = %q, want it in TMPDIR %q", k.path, tmp)
				}
				fi, serr := os.Stat(k.path)
				if serr != nil {
					t.Fatalf("stat key file %q after create: %v", k.path, serr)
				}
				if got := fi.Mode().Perm(); got != 0o600 {
					t.Errorf("key file mode = %#o, want %#o", got, 0o600)
				}

				err = teardown(ctx)
				if (err != nil) != tc.wantTeardownErr {
					t.Errorf("teardown(fail=%v): got err=%v, want error=%t", tc.fail, err, tc.wantTeardownErr)
				}
			}

			if left := leftovers(t, tmp); len(left) > 0 {
				t.Errorf("fail=%v: TMPDIR has leftover entries %v, want none (last err=%v)", tc.fail, left, err)
			}
		})
	}
}
