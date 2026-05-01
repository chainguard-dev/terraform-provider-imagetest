# GKE Driver Local Testing

This directory contains a test configuration for locally testing the GKE driver.

## Prerequisites

1. **GCP Project**: You need a GCP project with billing enabled
2. **GCP Credentials**: Authenticated with GCP
3. **gke-gcloud-auth-plugin**: Required for kubectl authentication

### Install gke-gcloud-auth-plugin

```bash
# macOS
gcloud components install gke-gcloud-auth-plugin

# Or via Homebrew
brew install gke-gcloud-auth-plugin

# Verify installation
gke-gcloud-auth-plugin --version
```

### Authenticate with GCP

```bash
# Option 1: User credentials (recommended for testing)
gcloud auth application-default login

# Option 2: Service account key (for CI/CD)
export GOOGLE_APPLICATION_CREDENTIALS="/path/to/service-account-key.json"

# Verify authentication
gcloud auth list
```

### Container Registry & Authentication

The imagetest provider needs a container registry to push modified test images. This example uses **cgr.dev** (Chainguard registry).

**Authenticate to cgr.dev**:

```bash
# Option 1: Using chainctl (recommended)
chainctl auth login
chainctl auth configure-docker

# Option 2: Using OIDC token
export CHAINGUARD_IDENTITY_TOKEN=$(chainctl auth token)

# Option 3: Using pull token (for automation)
# Create a pull token in the Chainguard console, then:
docker login cgr.dev -u <token-id> -p <token-secret>
```

**Configure the repository**:

In `main.tf`, update the `repo` parameter:
```hcl
provider "imagetest" {
  repo = "cgr.dev/YOUR_ORG/imagetest"
}
```

Replace `YOUR_ORG` with your Chainguard organization name (e.g., `chainguard-private`).

### Set GCP Project

```bash
# Set your default project
gcloud config set project YOUR_PROJECT_ID

# Or export for this session
export GOOGLE_CLOUD_PROJECT="YOUR_PROJECT_ID"
```

## Setup

1. **Build the provider**:
```bash
cd <YOUR_PATH_TO>/terraform-provider-imagetest
make terraform-provider-imagetest
# Or directly: go build -o terraform-provider-imagetest .
```

2. **Update main.tf**:
   - Open `main.tf`
   - Replace `YOUR_GCP_PROJECT_ID` with your actual GCP project ID

3. **Create dev override** (tells Terraform to use local binary):
```bash
cd examples/gke-test

cat > ~/.terraformrc <<EOF
provider_installation {
  dev_overrides {
    "chainguard-dev/imagetest" = "<YOUR_PATH_TO>/terraform-provider-imagetest"
  }
  direct {}
}
EOF
```

## Run the Test

```bash
cd examples/gke-test

# Initialize Terraform
terraform init

# See what will be created (doesn't create anything)
terraform plan

# Create the cluster and run tests (takes ~15 minutes)
terraform apply

# View the results
terraform output

# Clean up (deletes the cluster)
terraform destroy
```

## Test Options

### Quick Test (Use Existing Cluster)

If you already have a GKE cluster:

```bash
export IMAGETEST_GKE_CLUSTER="your-existing-cluster-name"
terraform apply
```

This will skip cluster creation and use your existing cluster.

### Debug Mode (Keep Resources)

To keep the cluster after testing (for debugging):

```bash
export IMAGETEST_GKE_SKIP_TEARDOWN=true
terraform apply
```

Then manually delete when done:
```bash
gcloud container clusters delete imagetest-XXXXX --region=us-central1
```

### Minimal Test (Fastest)

Modify `main.tf` to use a zonal cluster with 1 node:

```hcl
drivers = {
  gke = {
    project = "your-project"
    zone       = "us-central1-a"  # Zonal is faster
    node_count = 1
    machine_type = "e2-standard-2"  # Smaller machine
  }
}
```

## Cost Considerations

- **Regional cluster**: ~$0.10/hour (per cluster) + node costs
- **Zonal cluster**: $0 cluster fee + node costs
- **e2-standard-4**: ~$0.13/hour per node
- **Typical test run**: ~20 minutes = ~$0.05

**Recommendation**: Use zonal clusters with `e2-standard-2` for testing to minimize costs.

## Troubleshooting

### "Permission denied" errors

Ensure your GCP account has these IAM roles:
- `roles/container.admin` (create/delete clusters)
- `roles/compute.networkAdmin` (create VPCs/subnets)
- `roles/iam.serviceAccountUser` (use service accounts)

```bash
# Grant roles to your user
gcloud projects add-iam-policy-binding YOUR_PROJECT_ID \
  --member="user:your-email@example.com" \
  --role="roles/container.admin"
```

### "gke-gcloud-auth-plugin not found"

Install the plugin:
```bash
gcloud components install gke-gcloud-auth-plugin
```

### "Quota exceeded" errors

Check your project quotas:
```bash
gcloud compute project-info describe --project=YOUR_PROJECT_ID
```

Request quota increases if needed: https://console.cloud.google.com/iam-admin/quotas

### Cluster creation timeout

Increase the timeout in `main.tf`:
```hcl
timeout = "60m"  # Increase from 45m
```

## What Gets Created

When you run `terraform apply`, the following GCP resources are created:

1. **GKE Cluster** (`imagetest-XXXXXXXX`)
   - Location: us-central1 (regional) or us-central1-a (zonal)
   - Initial node count: 1 (default)

2. **Managed Resources** (auto-created by GKE):
   - VPC network and subnet
   - Firewall rules
   - VM instances (nodes)
   - Service accounts
   - Load balancers (if needed)

3. **Kubernetes Resources** (created by imagetest):
   - Namespace: `imagetest`
   - Service account with cluster-admin role
   - Test pods

All resources are automatically deleted when you run `terraform destroy`.

## Next Steps

Once this basic test works, try:
- Custom node configurations
- Multiple node pools
- Workload Identity (future feature)
- Integration with actual image tests from images-private repo
