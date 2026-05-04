# tests/fixtures

Terraform fixtures used by the `images-test` CI job to validate the
`chainguard-dev/imagetest` provider against real `cgr.dev/chainguard/*`
images.

## Layout

- `main.tf` — driver. Resolves each image's current `:latest` digest via
  `data "oci_ref"` and wires it into per-image modules.
- `images/<name>/` — per-image test fixtures.
- `tflib/imagetest/` — sandbox + docker-in-docker helper modules referenced
  by several fixtures.

Treat `images/` and `tflib/` as read-only imports — they are a known-working
reference set and should not be hand-edited here.

## Running

Prerequisites (installed once, globally): `terraform`, `go`, `docker`.
Everything else (`ko`, `crane`, the provider binary, the entrypoint bundle,
a local registry, a scoped `.terraformrc`) is set up by the `Makefile` in
this directory on first run.

```sh
# From the repo root:
make images-test/jre             # single image
make images-test                 # all 10

# Or equivalently, from this directory:
make test/jre
make test
make help                        # see all targets and overrides
```

On first invocation the `test` target will:

- `go install` `ko` and `crane` into `$(go env GOPATH)/bin` if missing.
- Start a `registry:2` container on `localhost:5005` (reused if already
  present; detected-and-reused if another registry is already serving the
  port, e.g. in CI).
- `go install .` the provider from the current working tree.
- Generate `tests/fixtures/.terraformrc.local` with a `dev_overrides` block
  pointing at your Go bin path, and export `TF_CLI_CONFIG_FILE` so it's
  scoped to this run — your global `~/.terraformrc` is left untouched.
- `ko build ./cmd/entrypoint` and export `IMAGETEST_ENTRYPOINT_REF`.
- `terraform init` + `terraform apply -target=module.<IMAGE>` with
  `target_repository=localhost:5005/imagetest`.

Subsequent runs reuse the registry and cached Go/ko/Terraform artifacts, so
the iteration loop is fast.

### Cleanup

```sh
make clean        # removes .terraform/, .terraformrc.local, .entrypoint.ref
make clean-all    # also stops the registry container
```

### What CI does differently

- `chainguard-dev/actions/setup-registry` provisions the `localhost:5005`
  registry before `make` runs; `make registry` detects it and skips
  starting its own.
- `step-security/setup-ko` installs `ko` via an action; `make tools` sees
  `ko` on `PATH` and skips.
- The workflow invokes `make images-test IMAGE=<matrix-entry>` — the exact
  command you'd run locally.

## Image coverage

| Image        | tflib? | Needs `target_repository`? | Coverage                           |
|--------------|:------:|:--------------------------:|------------------------------------|
| `jre`        |   no   |             no             | JVM runtime, javac compile + exec  |
| `nginx`      |   no   |             no             | HTTP server, welcome page, signals |
| `wolfi-base` |   no   |             no             | minimal base image sanity          |
| `busybox`    |  yes   |            yes             | apko sandbox + DinD smoke          |
| `go`         |  yes   |            yes             | native compile + run in DinD       |
| `maven`      |  yes   |            yes             | JVM build tool                     |
| `python`     |  yes   |            yes             | pip + numpy + multistage builds    |
| `ruby`       |  yes   |            yes             | comprehensive runtime tests        |
| `curl`       |  yes   |            yes             | HTTP client against sibling image  |
| `redis`      |  yes   |            yes             | stateful cache, active defrag      |

