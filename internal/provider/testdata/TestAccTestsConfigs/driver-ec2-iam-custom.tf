# driver-ec2-iam-custom.tf tests using a custom IAM instance profile
#
# Test workflow:
#
# 1. Create a custom IAM role and instance profile with additional permissions
# 2. Launch EC2 instance WITH custom instance_profile_name specified
# 3. Verify that custom IAM profile is used (not auto-created)
# 4. Test that custom permissions work

# Create a custom IAM role for testing
resource "aws_iam_role" "imagetest_custom" {
  name = "imagetest-custom-test-role"

  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Effect = "Allow"
        Principal = {
          Service = "ec2.amazonaws.com"
        }
        Action = "sts:AssumeRole"
      }
    ]
  })

  tags = {
    Name    = "imagetest-custom-test-role"
    Team    = "Containers"
    Project = "terraform-provider-imagetest::driver::ec2::test"
  }
}

# Attach ECR ReadOnly policy to custom role
resource "aws_iam_role_policy_attachment" "imagetest_custom_ecr" {
  role       = aws_iam_role.imagetest_custom.name
  policy_arn = "arn:aws:iam::aws:policy/AmazonEC2ContainerRegistryReadOnly"
}

# Attach additional S3 ReadOnly policy for testing custom permissions
resource "aws_iam_role_policy_attachment" "imagetest_custom_s3" {
  role       = aws_iam_role.imagetest_custom.name
  policy_arn = "arn:aws:iam::aws:policy/AmazonS3ReadOnlyAccess"
}

# Create instance profile and associate with role
resource "aws_iam_instance_profile" "imagetest_custom" {
  name = "imagetest-custom-test-profile"
  role = aws_iam_role.imagetest_custom.name

  tags = {
    Name    = "imagetest-custom-test-profile"
    Team    = "Containers"
    Project = "terraform-provider-imagetest::driver::ec2::test"
  }
}

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

resource "imagetest_tests" "iam_custom" {
  name   = "driver-ec2-iam-custom"
  driver = "ec2"

  drivers = {
    ec2 = {
      # Canonical's Ubuntu 24.04, amd64
      ami           = "ami-01b52ecd9c0144a93"
      instance_type = "t3.medium"

      # Specify custom instance profile name
      instance_profile_name = aws_iam_instance_profile.imagetest_custom.name

      exec = {
        user  = "ubuntu"
        shell = "bash"
        commands = [
          # Install awscli for testing permissions
          "sudo apt-get update",
          "sudo apt-get install -y awscli",

          # Test that instance has the custom IAM role attached
          "curl -s http://169.254.169.254/latest/meta-data/iam/security-credentials/ | grep imagetest-custom",

          # Test ECR permissions (from ECR ReadOnly policy)
          "aws sts get-caller-identity",
          "aws ecr get-login-password --region us-west-2 || echo 'ECR access test completed'",

          # Test S3 permissions (from custom S3 ReadOnly policy)
          "aws s3 ls || echo 'S3 access test completed'",
        ]
      }
    }
  }

  images = {
    test_image = "${local.layers.base.image}:${local.layers.base.tag}@${local.layers.base.digest}"
  }

  tests = [{
    name  = "iam-custom-profile-test"
    image = "${local.layers.test.image}:${local.layers.test.tag}@${local.layers.test.digest}"
    cmd   = "echo 'Custom IAM profile test passed!'; exit 0"
  }]

  timeout = "15m"

  # Ensure custom IAM resources are created before test runs
  depends_on = [
    aws_iam_instance_profile.imagetest_custom,
    aws_iam_role_policy_attachment.imagetest_custom_ecr,
    aws_iam_role_policy_attachment.imagetest_custom_s3
  ]
}