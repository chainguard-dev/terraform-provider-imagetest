resource "terraform_data" "foo" {
  provisioner "local-exec" {
    command = <<EOF
# pick a random open port
port=$(shuf -i 1024-65535 -n 1)
docker run --name ${self.id} -l 'dev.chainguard.imagetest=true' -d --privileged -p $port:$port cgr.dev/chainguard/k3s:latest server --disable traefik --disable metrics-server --https-listen-port $port --write-kubeconfig-mode 0644 --tls-san ${self.id} --https-listen-port $port

# Wait for the k3s cluster to be ready
until docker exec ${self.id} sh -c "k3s kubectl get --raw='/healthz'"; do
  sleep 1
done

# docker exec ${self.id} k3s kubectl config set-cluster default --server https://localhost:$port > /dev/null

docker exec ${self.id} cat /etc/rancher/k3s/k3s.yaml > foo.yaml
EOF
    when    = create
  }

  provisioner "local-exec" {
    command = "docker rm -f ${self.id}"
    when    = destroy
  }
}

output "connection" {
  value = {
    # docker = {
    #   cid = terraform_data.foo.id
    # }
    kubernetes = {
      kubeconfig_path = "foo.yaml"
    }
  }
}
