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

## Teardown

Teardown does not drain nodes. It deletes the nodegroup with
`eksctl delete nodegroup --drain=false --wait`, then the cluster with
`eksctl delete cluster --force --wait`. Both are bounded by the driver's
`timeouts.teardown` (default 20m): it is the Teardown context deadline, and
each delete gets the time left until that deadline as eksctl `--timeout`.

Only the cluster delete decides the outcome. `delete cluster --wait` removes
any nodegroup stack still present, so a failed nodegroup delete (typically
because Setup failed before the nodegroup stack existed) is logged as a
warning, not a teardown failure. If the cluster delete fails the error names
the cluster and includes the manual cleanup command below.

To keep the cluster around after a test run, set `IMAGETEST_EKS_SKIP_TEARDOWN=true`.
Clean it up manually in the same order, so the nodegroup goes without a drain:

```
eksctl delete nodegroup --cluster <cluster> --region us-west-2 --name <ng-...> --drain=false --wait && \
eksctl delete cluster --name <cluster> --region us-west-2 --force --wait
```

The nodegroup name is in the driver's "Created nodegroup" log line, or
`eksctl get nodegroup --cluster <cluster> --region us-west-2`.
