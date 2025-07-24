resource "imagetest_tests" "foo" {
  name   = "ec2-driver-basic"
  driver = "ec2"

  drivers = {
    ec2 = {
      region        = "us-west-2"
      ami           = "ami-08b674058d6b8d3f6"
      instance_type = "t3.large"
      commands = {
        user  = "ubuntu"
        shell = "bash"
        commands = [
          "some=1337",
          "[ some -eq 1337 ] && exit 0 || exit 1"
        ]
      }
    }
  }

  images = {
    foo = "cgr.dev/chainguard/busybox:latest@sha256:c546e746013d75c1fc9bf01b7a645ce7caa1ec46c45cb618c6e28d7b57bccc85"
  }

  # tests = [
  #   {
  #     name  = "sample"
  #     image = "cgr.dev/chainguard/busybox:latest-dev"
  #     content = [
  #       {
  #         source = "${path.module}/testdata/TestAccTestsResource"
  #       }
  #     ]
  #     cmd = "./%[1]s"
  #   }
  # ]

  // Something before GHA timeouts
  timeout = "5m"
}
