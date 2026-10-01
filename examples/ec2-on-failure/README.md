# EC2 on_failure

This example fails an EC2 test on purpose and runs the ec2 driver's
`on_failure` commands on the instance before it is torn down.

Unlike a test's `on_failure`, which runs inside the test container, the ec2
driver's `on_failure` runs on the instance itself (in the driver's `shell`,
with its `env`, like `setup_commands`), and also when setup fails. Use it to
print host logs, e.g. `sudo tail -n 1000 /var/log/libvirt/libvirtd.log`. The
output is capped at 2 MiB in total, so trim large logs.

```sh
terraform init
terraform apply -var vpc_id=vpc-... # fails in the test
terraform apply -var vpc_id=vpc-... -var fail_in=setup
```

The apply fails, and an `ec2 on_failure output` warning shows each command's
output, like a test's `on_failure` output:

```
$ cat /tmp/setup.out
setup transcript

$ cat /does/not/exist
cat: /does/not/exist: No such file or directory
[exit: ...]
```
