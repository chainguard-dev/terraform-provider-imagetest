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

To test with custom instance type:

```
IMAGETEST_EKS_NODE_TYPE=m5.2xlarge \
TF_ACC=1 go test -tags=eks ./internal/provider/... -count=1 -v -run=EKS -timeout=30m
```

To test with custom node count:

```
IMAGETEST_EKS_NODE_COUNT=3 \
TF_ACC=1 go test -tags=eks ./internal/provider/... -count=1 -v -run=EKS -timeout=30m
```

To test with custom storage configuration:

```
IMAGETEST_EKS_STORAGE_SIZE=50GB \
IMAGETEST_EKS_STORAGE_TYPE=gp3 \
TF_ACC=1 go test -tags=eks ./internal/provider/... -count=1 -v -run=EKS -timeout=30m
```

To test with multiple custom parameters:

```
IMAGETEST_EKS_NODE_AMI=<some AMI you like> \
IMAGETEST_EKS_NODE_TYPE=m5.4xlarge \
IMAGETEST_EKS_NODE_COUNT=2 \
IMAGETEST_EKS_STORAGE_SIZE=20GB \
IMAGETEST_EKS_STORAGE_TYPE=gp3 \
TF_ACC=1 go test -tags=eks ./internal/provider/... -count=1 -v -run=EKS -timeout=30m
```
