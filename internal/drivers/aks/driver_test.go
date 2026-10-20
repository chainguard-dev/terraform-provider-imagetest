package aks

import (
	"context"
	"encoding/base64"
	"encoding/pem"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/chainguard-dev/terraform-provider-imagetest/internal/drivers"
	"github.com/chainguard-dev/terraform-provider-imagetest/internal/harness"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
)

// TestKubeconfigLifetime checks the kubeconfig Setup writes the cluster's
// admin credentials to: it is a private temp file, removed by the teardown
// stack unless the cluster itself is kept (IMAGETEST_AKS_SKIP_TEARDOWN).
func TestKubeconfigLifetime(t *testing.T) {
	tests := []struct {
		name     string
		skipEnv  string // IMAGETEST_AKS_SKIP_TEARDOWN
		wantKept bool
	}{{
		name: "teardown removes the kubeconfig",
	}, {
		name:     "kubeconfig is kept with the cluster when AKS teardown is skipped",
		skipEnv:  "true",
		wantKept: true,
	}, {
		name:    "only the exact value true skips teardown",
		skipEnv: "1",
	}}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tmp := t.TempDir()
			t.Setenv("TMPDIR", tmp)
			t.Setenv("IMAGETEST_AKS_SKIP_TEARDOWN", tc.skipEnv)

			k := &driver{clusterName: "imagetest-test", stack: harness.NewStack()}
			if err := k.createKubeconfig(); err != nil {
				t.Fatalf("createKubeconfig: %v", err)
			}
			if filepath.Dir(k.kubeconfig) != tmp {
				t.Errorf("kubeconfig = %q, want it in TMPDIR %q", k.kubeconfig, tmp)
			}
			fi, err := os.Stat(k.kubeconfig)
			if err != nil {
				t.Fatalf("stat kubeconfig %q: %v", k.kubeconfig, err)
			}
			if got := fi.Mode().Perm(); got != 0o600 {
				t.Errorf("kubeconfig mode = %#o, want %#o", got, 0o600)
			}

			if err := k.stack.Teardown(t.Context()); err != nil {
				t.Errorf("stack teardown: %v", err)
			}

			_, err = os.Stat(k.kubeconfig)
			if kept := err == nil; kept != tc.wantKept {
				t.Errorf("IMAGETEST_AKS_SKIP_TEARDOWN=%q: kubeconfig kept=%t (stat err=%v), want kept=%t", tc.skipEnv, kept, err, tc.wantKept)
			}
		})
	}
}

// TestLocalFiles checks the kubeconfig is reported as a host file teardown
// removes, so the provider can name it at the end of a run that kept it.
func TestLocalFiles(t *testing.T) {
	tests := []struct {
		name       string
		kubeconfig string
		entraAuth  bool
		want       []drivers.LocalFile
	}{{
		name: "nothing is reported before Setup creates the kubeconfig",
	}, {
		name:       "the kubeconfig is reported with its cluster",
		kubeconfig: "/tmp/imagetest-test-kubeconfig-123",
		want: []drivers.LocalFile{{
			Path:        "/tmp/imagetest-test-kubeconfig-123",
			Description: "admin kubeconfig for AKS cluster imagetest-test",
		}},
	}, {
		name:       "an Entra cluster's kubeconfig signs in with kubelogin",
		kubeconfig: "/tmp/imagetest-test-kubeconfig-123",
		entraAuth:  true,
		want: []drivers.LocalFile{{
			Path:        "/tmp/imagetest-test-kubeconfig-123",
			Description: "kubeconfig, which signs in with kubelogin, for AKS cluster imagetest-test",
		}},
	}}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			k := &driver{clusterName: "imagetest-test", kubeconfig: tc.kubeconfig, entraAuth: tc.entraAuth}
			if got := k.LocalFiles(); !slices.Equal(got, tc.want) {
				t.Errorf("LocalFiles() with kubeconfig=%q: got %+v, want %+v", tc.kubeconfig, got, tc.want)
			}
		})
	}
}

// renewingCredential hands out a new token on every call, as azidentity does
// once the previous one nears expiry.
type renewingCredential struct {
	mu     sync.Mutex
	issued int
	scopes [][]string
}

func (c *renewingCredential) GetToken(_ context.Context, opts policy.TokenRequestOptions) (azcore.AccessToken, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.issued++
	c.scopes = append(c.scopes, opts.Scopes)
	return azcore.AccessToken{Token: fmt.Sprintf("token-%d", c.issued)}, nil
}

// TestUseEntraTokens checks that a client built from an Entra cluster's
// kubeconfig authenticates every request with a token the credential hands
// out for that request, so a token renewed mid-test reaches the next request,
// and that the kubeconfig's kubelogin exec stanza is never run.
func TestUseEntraTokens(t *testing.T) {
	var (
		mu   sync.Mutex
		auth []string
	)
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		auth = append(auth, r.Header.Get("Authorization"))
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if _, err := fmt.Fprint(w, `{"major":"1","minor":"33","gitVersion":"v1.33.0"}`); err != nil {
			t.Errorf("writing version: %v", err)
		}
	}))
	defer srv.Close()

	// The shape listClusterUserCredential returns for an Entra-only cluster:
	// server and CA, and a kubelogin exec stanza instead of a credential.
	ca := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw})
	kubeconfig := filepath.Join(t.TempDir(), "kubeconfig")
	if err := os.WriteFile(kubeconfig, fmt.Appendf(nil, `apiVersion: v1
kind: Config
clusters:
- name: aks
  cluster:
    server: %s
    certificate-authority-data: %s
users:
- name: clusterUser
  user:
    exec:
      apiVersion: client.authentication.k8s.io/v1beta1
      command: kubelogin-not-installed
      args: [get-token, --server-id, 6dae42f8-4368-4678-94ff-3960e28e3630]
contexts:
- name: aks
  context:
    cluster: aks
    user: clusterUser
current-context: aks
`, srv.URL, base64.StdEncoding.EncodeToString(ca)), 0o600); err != nil {
		t.Fatal(err)
	}

	config, err := clientcmd.BuildConfigFromFlags("", kubeconfig)
	if err != nil {
		t.Fatalf("BuildConfigFromFlags: %v", err)
	}
	cred := &renewingCredential{}
	useEntraTokens(config, cred)
	kcli, err := kubernetes.NewForConfig(config)
	if err != nil {
		t.Fatalf("NewForConfig: %v", err)
	}

	for range 3 {
		if _, err := kcli.Discovery().ServerVersion(); err != nil {
			t.Fatalf("ServerVersion: %v", err)
		}
	}

	want := []string{"Bearer token-1", "Bearer token-2", "Bearer token-3"}
	if !slices.Equal(auth, want) {
		t.Errorf("Authorization headers = %q, want %q", auth, want)
	}
	for _, scopes := range cred.scopes {
		if !slices.Equal(scopes, []string{aksServerScope}) {
			t.Errorf("token requested for scopes %q, want %q", scopes, aksServerScope)
		}
	}
}
