# Local testing

To run the acceptance test, have `eksctl` installed and AWS auth set up, then run:

```
TF_ACC=1 go test -tags=eks ./internal/provider/... -count=1 -v -run=EKS -timeout=30m
```

This will use your credentials to spin up an EKS cluster in `us-west-2`, run an image on that cluster, then delete the cluster.

To test with a custom AMI:

```
IMAGETEST_EKS_NODE_AMI=<some AMI you like> \
TF_ACC=1 go test -tags=eks ./internal/provider/... -count=1 -v -run=EKS -timeout=30m
```
