package ec2

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/chainguard-dev/terraform-provider-imagetest/internal/drivers"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/client"
)

// TestCopyArtifact exercises copyArtifact against a real Docker daemon. It is
// the deterministic counterpart to the EC2 acceptance test's artifact path:
// runContainer uses the same CopyFromContainer + untar + NewRunArtifactResult
// flow, just over an SSH-tunneled client. Skips when no daemon is reachable.
func TestCopyArtifact(t *testing.T) {
	const image = "cgr.dev/chainguard/wolfi-base:latest"

	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		t.Skipf("no docker client: %v", err)
	}
	defer cli.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	if _, err := cli.Ping(ctx); err != nil {
		t.Skipf("docker daemon not reachable: %v", err)
	}

	// Mirror the entrypoint's bundle: an arbitrary blob at ArtifactsPath. The
	// content is opaque to copyArtifact — it just round-trips the bytes.
	want := []byte("imagetest-artifact-bundle\x00\x01\x02 contents")
	wantSum := sha256.Sum256(want)
	artifactPath := "/test-artifact.tar.gz"

	resp, err := cli.ContainerCreate(ctx,
		&container.Config{Image: image, Cmd: []string{"true"}},
		nil, nil, nil, "")
	if err != nil {
		t.Skipf("could not create container from %s (image present?): %v", image, err)
	}
	t.Cleanup(func() {
		_ = cli.ContainerRemove(context.Background(), resp.ID, container.RemoveOptions{Force: true})
	})

	// Inject the file via the same tar framing the Docker API uses.
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	hdr := &tar.Header{Name: "test-artifact.tar.gz", Mode: 0o644, Size: int64(len(want)), Typeflag: tar.TypeReg}
	if err := tw.WriteHeader(hdr); err != nil {
		t.Fatalf("write tar header: %v", err)
	}
	if _, err := tw.Write(want); err != nil {
		t.Fatalf("write tar body: %v", err)
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("close tar: %v", err)
	}
	if err := cli.CopyToContainer(ctx, resp.ID, "/", &buf, container.CopyToContainerOptions{}); err != nil {
		t.Fatalf("copy file into container: %v", err)
	}

	// 1) copyArtifact must unwrap Docker's tar framing and return the raw bytes.
	rc, err := copyArtifact(ctx, cli, resp.ID, artifactPath)
	if err != nil {
		t.Fatalf("copyArtifact: %v", err)
	}
	got, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("read artifact: %v", err)
	}
	if err := rc.Close(); err != nil {
		t.Fatalf("close artifact reader: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("artifact bytes mismatch:\n got=%q\nwant=%q", got, want)
	}

	// 2) The full attach path: NewRunArtifactResult must produce a file:// URI
	// and a checksum matching the content, as surfaced in Terraform state.
	rc2, err := copyArtifact(ctx, cli, resp.ID, artifactPath)
	if err != nil {
		t.Fatalf("copyArtifact (2): %v", err)
	}
	res, err := drivers.NewRunArtifactResult(ctx, rc2)
	_ = rc2.Close()
	if err != nil {
		t.Fatalf("NewRunArtifactResult: %v", err)
	}

	if res.Checksum != hex.EncodeToString(wantSum[:]) {
		t.Fatalf("checksum mismatch: got %s want %s", res.Checksum, hex.EncodeToString(wantSum[:]))
	}

	u, err := url.Parse(res.URI)
	if err != nil || u.Scheme != "file" {
		t.Fatalf("expected file:// URI, got %q (err=%v)", res.URI, err)
	}
	onDisk, err := os.ReadFile(u.Path)
	if err != nil {
		t.Fatalf("read artifact file %s: %v", u.Path, err)
	}
	if !bytes.Equal(onDisk, want) {
		t.Fatalf("on-disk artifact mismatch:\n got=%q\nwant=%q", onDisk, want)
	}

	// copyArtifact must surface a clear error for a missing path.
	if _, err := copyArtifact(ctx, cli, resp.ID, "/does-not-exist.tar.gz"); err == nil {
		t.Fatalf("expected error for missing artifact path, got nil")
	}
}
