# Terraform Provider Image Test

🚨 **This is a work in progress** 🚨

A terraform provider for authoring and executing tests using terraform primitives. Designed to work in conjunction with the [Chainguard Images](https://github.com/chainguard-dev/images) project. I would strongly recommend against using it for anything else.

See [examples](./examples) for usages, and [design](./design.md) for more information about the providers design.


## Testing the provider

Basic acceptance tests:

```
TF_ACC=1 go test -v ./...
``

Testing the EKS driver takes a _lot_ longer, and creates resources which might cost money. To run these tests, ensure you have AWS auth set up and `eksctl` installed, then run:

```
TF_ACC=1 go test -tags=eks -v ./... -run=EKS -timeout=30m
```

