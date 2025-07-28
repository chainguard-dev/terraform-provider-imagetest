resource "imagetest_tests" "foo" {
  name   = "driver-ec2-basic"
  driver = "ec2"

  drivers = {
    ec2 = {
      # Ubuntu 24.04
      ami           = "ami-08b674058d6b8d3f6"
      instance_type = "m8g.xlarge"

      exec = {
        user  = "ubuntu"
        shell = "bash"
        commands = [
          # These two commands are just a silly little example to demonstrate
          # the persistence of state across commands since everything in
          # 'commands' is executed within the scope of a single SSH session.
          "some=1337",
          "[ $some -eq 1337 ] && exit 0 || exit 1",
        ]
      }
    }
  }

  images = {
    foo = "cgr.dev/chainguard/busybox:latest@sha256:c546e746013d75c1fc9bf01b7a645ce7caa1ec46c45cb618c6e28d7b57bccc85"
  }

  tests = [{
    name  = "driver-ec2-basic"
    image = "cgr.dev/chainguard/busybox:latest"
    cmd   = "echo 'Hello, world!'; exit 0"
  }]

  // Something before GHA timeouts
  timeout = "5m"
}
