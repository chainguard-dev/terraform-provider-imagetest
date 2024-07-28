variable "foo" { default = "bar" }

resource "terraform_data" "foo" {
  provisioner "local-exec" {
    command = "docker run --name ${self.id} -e 'FOO=${var.foo}' -l 'dev.chainguard.imagetest=true' -d cgr.dev/chainguard/wolfi-base:latest tail -f /dev/null"
    when    = create
  }

  provisioner "local-exec" {
    command = "docker rm -f ${self.id}"
    when    = destroy
  }
}

output "connection" {
  value = {
    docker = {
      cid = terraform_data.foo.id
    }
  }
}
