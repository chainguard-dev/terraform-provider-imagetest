resource "imagetest_tests" "foo" {
  name   = "driver-ec2-volume-mount"
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
          # Place a file on the host and bind mount it to the container.
          "sudo mkdir -p /data",
          "echo -n 'Hello, world!' | sudo tee /data/test",
          "sudo chmod 666 /data/test"
        ]
      }

      volume_mounts = [
        "/data:/data"
      ]
    }
  }

  images = {
    foo = "cgr.dev/chainguard/busybox:latest@sha256:c546e746013d75c1fc9bf01b7a645ce7caa1ec46c45cb618c6e28d7b57bccc85"
  }

  tests = [{
    name  = "driver-ec2-volume-mount"
    image = "cgr.dev/chainguard/busybox:latest"
    cmd   = "content=$(cat /data/test); [ $content == 'Hello, world!' ] && exit 0 || exit 1"
  }]

  // Something before GHA timeouts
  timeout = "5m"
}
