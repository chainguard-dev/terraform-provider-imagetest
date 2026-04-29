# GKE Driver Quick Start Guide

## 🚀 5-Minute Setup

### 1. Install Prerequisites

```bash
# Install gke-gcloud-auth-plugin
gcloud components install gke-gcloud-auth-plugin

# Verify
gke-gcloud-auth-plugin --version
```

### 2. Authenticate

```bash
# Login to GCP
gcloud auth application-default login

# Set your project (replace with your project ID)
gcloud config set project YOUR_PROJECT_ID

# Authenticate to Chainguard registry
chainctl auth login
chainctl auth configure-docker
```

### 3. Configure Terraform Dev Override

```bash
# This tells Terraform to use your locally built provider
cat > ~/.terraformrc <<'EOF'
provider_installation {
  dev_overrides {
    "chainguard-dev/imagetest" = "<YOUR_PATH_TO>/terraform-provider-imagetest"
  }
  direct {}
}
EOF
```

### 4. Update Configuration

```bash
cd <YOUR_PATH_TO>/terraform-provider-imagetest/examples/gke-test

# Edit main.tf and replace:
# 1. YOUR_GCP_PROJECT_ID with your GCP project
# 2. YOUR_ORG with your Chainguard organization name

# You can use sed:
sed -i '' 's/YOUR_GCP_PROJECT_ID/my-gcp-project/g' main.tf
sed -i '' 's/YOUR_ORG/chainguard-private/g' main.tf
```

### 5. Run the Test

```bash
# Initialize (you'll see a warning about dev overrides - that's expected!)
terraform init

# See the plan
terraform plan

# Create cluster and run test (takes ~15 minutes)
terraform apply -auto-approve

# Clean up
terraform destroy -auto-approve
```

**Note**: The test uses cgr.dev (Chainguard registry). Make sure you're authenticated with `chainctl auth login` and have push access to the repository specified in `main.tf`.

## 💡 Expected Output

### During `terraform apply`:

```
imagetest_tests.gke_basic: Creating...
imagetest_tests.gke_basic: Still creating... [10s elapsed]
imagetest_tests.gke_basic: Still creating... [8m30s elapsed]
...
imagetest_tests.gke_basic: Still creating... [14m45s elapsed]
imagetest_tests.gke_basic: Creation complete after 15m2s [id=gke-basic-test]

Apply complete! Resources: 1 added, 0 changed, 0 destroyed.

Outputs:

test_results = {
  "driver" = "gke"
  "id" = "gke-basic-test"
  "name" = "gke-basic-test"
  ...
}
```

### In GCP Console:

You'll see a cluster named `imagetest-XXXXXXXX` being created in us-central1.

## ⚡ Faster Testing Options

### Option 1: Use a Zonal Cluster (Cheaper, Faster)

Edit `main.tf`:
```hcl
drivers = {
  gke = {
    project_id = "your-project"
    zone       = "us-central1-a"  # Instead of region
  }
}
```

### Option 2: Use an Existing Cluster

```bash
# If you already have a GKE cluster
export IMAGETEST_GKE_CLUSTER="my-existing-cluster"
terraform apply
```

### Option 3: Keep Cluster for Multiple Tests

```bash
# First run - creates cluster
export IMAGETEST_GKE_SKIP_TEARDOWN=true
terraform apply

# Make changes to tests in main.tf, then:
export IMAGETEST_GKE_CLUSTER="imagetest-XXXXX"  # Use the cluster name from first run
terraform apply

# Clean up manually when done
gcloud container clusters delete imagetest-XXXXX --region=us-central1
```

## 🔍 Verify It's Working

Check the logs during terraform apply:

```
# You should see:
INFO Creating GKE cluster name=imagetest-abc123 project=your-project
INFO Waiting for GKE cluster provisioning...
INFO Created GKE cluster: imagetest-abc123
INFO Running test with image: cgr.dev/chainguard/kubectl:latest-dev
INFO kubectl version --client
INFO kubectl get nodes
INFO ✓ GKE cluster is working!
```

## 🐛 Quick Troubleshooting

### Error: "tag references are not supported"

The imagetest provider requires digest references, not tags:

```bash
# ❌ Wrong - using tag
images = {
  nginx = "cgr.dev/chainguard/nginx:latest"
}

# ✅ Correct - using digest
images = {
  nginx = "cgr.dev/chainguard/nginx:latest@sha256:abc123..."
}
```

To get the digest for an image:
```bash
# Install crane if needed
go install github.com/google/go-containerregistry/cmd/crane@latest

# Get digest
crane digest cgr.dev/chainguard/nginx:latest
```

### Error: "gke-gcloud-auth-plugin: command not found"
```bash
gcloud components install gke-gcloud-auth-plugin
```

### Error: "Permission denied" or "IAM policy"
```bash
# Grant yourself container admin role
gcloud projects add-iam-policy-binding YOUR_PROJECT_ID \
  --member="user:$(gcloud config get-value account)" \
  --role="roles/container.admin"
```

### Error: "Quota exceeded"
Your project has hit resource limits. Either:
- Delete unused resources in the GCP console
- Request quota increase: https://console.cloud.google.com/iam-admin/quotas
- Use a different region

### Warning: "Provider development overrides are in effect"
This is **expected**! It means Terraform is using your local binary.

## 💰 Cost Estimate

- **Test duration**: ~20 minutes
- **Cluster cost**: Regional clusters have a $0.10/hour management fee
- **Node cost**: e2-standard-4 is ~$0.13/hour
- **Total**: ~$0.10 per test run

To minimize costs:
- Use zonal clusters (no management fee)
- Use smaller machines (e2-standard-2)
- Clean up immediately after testing
- Use `IMAGETEST_GKE_CLUSTER` to reuse clusters

## 🎯 What's Next?

Once this works, you can:

1. **Test with real images** from images-private:
   ```hcl
   images = {
     gcp-csi = "cgr.dev/chainguard/gcp-compute-persistent-disk-csi-driver:latest"
   }
   ```

2. **Add more complex tests**:
   ```hcl
   tests = [{
     name  = "persistent-volume-test"
     image = "cgr.dev/chainguard/kubectl:latest-dev"
     cmd   = <<-EOF
       kubectl apply -f storageclass.yaml
       kubectl apply -f pvc.yaml
       kubectl wait --for=condition=Bound pvc/test-pvc
     EOF
   }]
   ```

3. **Run acceptance tests**:
   ```bash
   export IMAGETEST_GKE_PROJECT_ID="your-project"
   export TF_ACC=1
   go test -v -tags=gke ./internal/provider/tests_resource_gke_test.go -timeout=120m
   ```

## 📚 Full Documentation

See `README.md` for complete documentation including:
- All configuration options
- Debugging tips
- GCP permissions
- Resource cleanup
