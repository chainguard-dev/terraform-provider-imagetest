resource "imagetest_tests" "foo" {
  name   = "%[1]s"
  driver = "k3s_in_docker"

  drivers = {
    k3s_in_docker = {}
  }

  images = {
    foo = "cgr.dev/chainguard/busybox:latest@sha256:c546e746013d75c1fc9bf01b7a645ce7caa1ec46c45cb618c6e28d7b57bccc85"
  }

  tests = [
    {
      name    = "sample"
      image   = "cgr.dev/chainguard/kubectl:latest-dev"
      content = [{ source = "${path.module}/testdata/TestAccTestsResource" }]
      cmd     = "./%[1]s"
    }
  ]

  // Something before GHA timeouts
  timeout = "5m"
}
