# Terraform Provider Image Test

🚨 **This is a work in progress** 🚨

A terraform provider for authoring and executing tests using terraform primitives. Designed to work in conjunction with the [Chainguard Images](https://github.com/chainguard-dev/images) project. I would strongly recommend against using it for anything else.

See [examples](./examples) for usages, and [design](./design.md) for more information about the providers design.


## Testing the provider

Basic acceptance tests:

```
IMAGETEST_ENTRYPOINT_REF=$(KO_DOCKER_REPO=ttl.sh/imagetest ko build ./cmd/entrypoint) \
    TF_ACC=1 \
    go test ./internal/provider/... -count=1 -v
```

This will build and use the entrypoint image, and use it in the test.

Testing the EKS driver takes a _lot_ longer, and creates resources which might cost money. To run these tests, ensure you have AWS auth set up and `eksctl` installed, then run:

```
IMAGETEST_ENTRYPOINT_REF=$(KO_DOCKER_REPO=ttl.sh/imagetest ko build ./cmd/entrypoint) \
    TF_ACC=1 \
    go test -tags=eks ./internal/provider/... -count=1 -v -run=EKS -timeout=30m
```

This will run the EKS tests using your auth, and `eksctl` to manage the cluster.

The test will look like it's doing nothing for ~15-20 minutes, but you can check the progress by using `eksctl get clusters`.

When the cluster is up, you can find its kubeconfig in `$TMPDIR/imagetest-<uid>`, and `eksctl` populates the file (this takes ~10 minutes), use it to interact with the cluster:

```
KUBECONFIG=$TMPDIR/imagetest-<uid> kubectl get nodes
```

When the test completes, it should delete the cluster, but just in case it doesn't, you can delete it with:

```
eksctl delete cluster --name=imagetest-<uid>
```

You can also find the cluster in the AWS Console: https://us-west-2.console.aws.amazon.com/eks/home

To reuse the cluster instead of creating a new one each time, you can run the tests with `IMAGETEST_EKS_SKIP_TEARDOWN=true`.

Then, the next time you run the test, find the cluster that the last test created, and add `IMAGETEST_EKS_CLUSTER=imagetest-<uid>` to reuse the cluster.
