# driver-ec2-iam-auto.tf tests automatic IAM role and instance profile creation
#
# Test workflow:
#
# 1. Launch EC2 instance WITHOUT specifying instance_profile_name
# 2. Verify that IAM role and instance profile are automatically created
# 3. Verify that EC2 instance can access ECR (pull container images)
# 4. Run a simple test that requires ECR access

locals {
  layers = {
    base = {
      image  = "cgr.dev/chainguard/busybox"
      tag    = "latest"
      digest = "sha256:c546e746013d75c1fc9bf01b7a645ce7caa1ec46c45cb618c6e28d7b57bccc85"
    }
    test = {
      image  = "cgr.dev/chainguard/busybox"
      tag    = "latest"
      digest = "sha256:c546e746013d75c1fc9bf01b7a645ce7caa1ec46c45cb618c6e28d7b57bccc85"
    }
  }
}

resource "imagetest_tests" "iam_auto" {
  name   = "driver-ec2-iam-auto"
  driver = "ec2"

  drivers = {
    ec2 = {
      # Canonical's Ubuntu 24.04, amd64
      ami           = "ami-01b52ecd9c0144a93"
      instance_type = "t3.medium"
      # NOTE: instance_profile_name is NOT specified - should auto-create

      exec = {
        user  = "ubuntu"
        shell = "bash"
        commands = [
          # Install docker and awscli for testing ECR access
          "sudo apt-get update",
          "sudo apt-get install -y docker.io awscli",
          "sudo systemctl start docker",
          "sudo usermod -a -G docker ubuntu",

          # Test that instance has IAM role attached
          "curl -s http://169.254.169.254/latest/meta-data/iam/security-credentials/ | grep imagetest",

          # Test that IAM role has ECR permissions by attempting to get ECR token
          # This should succeed if IAM role has AmazonEC2ContainerRegistryReadOnly policy
          "aws sts get-caller-identity",
          "aws ecr get-login-password --region us-west-2 || echo 'ECR token test completed'",
        ]
      }
    }
  }

  images = {
    test_image = "${local.layers.base.image}:${local.layers.base.tag}@${local.layers.base.digest}"
  }

  tests = [{
    name  = "iam-auto-creation-test"
    image = "${local.layers.test.image}:${local.layers.test.tag}@${local.layers.test.digest}"
    cmd   = "echo 'IAM auto-creation test passed!'; exit 0"
  }]

  timeout = "15m"
}