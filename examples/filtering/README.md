# Skipping Tests

This example walks through skipping one or more tests at runtime.

If you run `terraform apply` without setting any variables, all tests will be
executed. This module exposes three variables that configure `imagetest` skipping
behavior that we will use to run subsets of the tests defined in `main.tf`: 
`include_tests_by_label`, `skip_tests_by_label`, and `skip_all_tests`.

If we want to skip the flaky tests, we could run:

```sh
TF_LOG=info TF_VAR_skip_tests_by_label='{"flaky":"true"}' terraform apply -auto-approve
```

We should see that the `k3s-hello-world-eks` and `docker-aws-hello-world` features were skipped and the other tests ran successfully.

If we skip all of the tests within a single harness, the harness setup will
also be skipped, saving additional time and resources:

```sh
TF_LOG=info TF_VAR_skip_tests_by_label='{"type":"k8s"}' terraform apply -auto-approve
```

A realistic test bucket for CI execution would be all of the small tests, other
than those which are flaky:

```sh
TF_LOG=info TF_VAR_skip_tests_by_label='{"flaky":"true"}' TF_VAR_include_tests_by_label='{"size":"small"}' terraform apply -auto-approve
```

If we want to skip all of the tests:

```sh
# Or, set IMAGETEST_SKIP_ALL environment variable
TF_LOG=info TF_VAR_skip_all_tests='true' terraform apply -auto-approve
```
