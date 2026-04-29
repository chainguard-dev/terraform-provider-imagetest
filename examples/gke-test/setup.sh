#!/bin/bash
set -e

echo "🚀 GKE Driver Test Setup"
echo "========================"
echo ""

# Colors
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
RED='\033[0;31m'
NC='\033[0m' # No Color

# Check if running from correct directory
if [ ! -f "main.tf" ]; then
    echo -e "${RED}❌ Error: main.tf not found${NC}"
    echo "Please run this script from the examples/gke-test directory"
    exit 1
fi

# Check prerequisites
echo "📋 Checking prerequisites..."
echo ""

# Check gcloud
if ! command -v gcloud &> /dev/null; then
    echo -e "${RED}❌ gcloud CLI not found${NC}"
    echo "Install from: https://cloud.google.com/sdk/docs/install"
    exit 1
fi
echo -e "${GREEN}✓ gcloud CLI installed${NC}"

# Check gke-gcloud-auth-plugin
if ! command -v gke-gcloud-auth-plugin &> /dev/null; then
    echo -e "${YELLOW}⚠️  gke-gcloud-auth-plugin not found${NC}"
    echo ""
    echo "Installing gke-gcloud-auth-plugin..."
    gcloud components install gke-gcloud-auth-plugin --quiet
    echo -e "${GREEN}✓ gke-gcloud-auth-plugin installed${NC}"
else
    echo -e "${GREEN}✓ gke-gcloud-auth-plugin installed${NC}"
fi

# Check terraform
if ! command -v terraform &> /dev/null; then
    echo -e "${RED}❌ Terraform not found${NC}"
    echo "Install from: https://www.terraform.io/downloads"
    exit 1
fi
echo -e "${GREEN}✓ Terraform installed ($(terraform version -json | jq -r .terraform_version))${NC}"

# Check GCP authentication
echo ""
echo "🔐 Checking GCP authentication..."
if ! gcloud auth application-default print-access-token &> /dev/null; then
    echo -e "${YELLOW}⚠️  Not authenticated with GCP${NC}"
    echo ""
    echo "Authenticating..."
    gcloud auth application-default login
fi
echo -e "${GREEN}✓ Authenticated with GCP${NC}"

# Check Chainguard authentication
echo ""
echo "🔐 Checking Chainguard registry authentication..."
if ! command -v chainctl &> /dev/null; then
    echo -e "${YELLOW}⚠️  chainctl not found${NC}"
    echo "Install from: https://edu.chainguard.dev/chainguard/chainctl/"
    echo ""
    read -p "Press enter to continue without chainctl authentication..."
else
    if ! chainctl auth status &> /dev/null; then
        echo -e "${YELLOW}⚠️  Not authenticated to Chainguard${NC}"
        echo ""
        echo "Authenticating..."
        chainctl auth login
        chainctl auth configure-docker
    fi
    echo -e "${GREEN}✓ Authenticated with Chainguard${NC}"
fi

# Get current project
CURRENT_PROJECT=$(gcloud config get-value project 2>/dev/null || echo "")
if [ -z "$CURRENT_PROJECT" ]; then
    echo ""
    echo -e "${YELLOW}⚠️  No default project set${NC}"
    echo "Please enter your GCP project ID:"
    read -r GCP_PROJECT
    gcloud config set project "$GCP_PROJECT"
    CURRENT_PROJECT="$GCP_PROJECT"
fi
echo -e "${GREEN}✓ Using GCP project: ${CURRENT_PROJECT}${NC}"

# Check if provider binary exists
PROVIDER_PATH="<YOUR_PATH_TO>/terraform-provider-imagetest/terraform-provider-imagetest"
if [ ! -f "$PROVIDER_PATH" ]; then
    echo ""
    echo -e "${YELLOW}⚠️  Provider binary not found${NC}"
    echo "Building provider..."
    cd <YOUR_PATH_TO>/terraform-provider-imagetest
    CGO_ENABLED=0 go build -ldflags "-s -w -X main.version=devel" -o terraform-provider-imagetest .
    cd - > /dev/null
    echo -e "${GREEN}✓ Provider built${NC}"
else
    echo -e "${GREEN}✓ Provider binary exists${NC}"
fi

# Update main.tf with project ID
echo ""
echo "📝 Updating configuration..."
if grep -q "YOUR_GCP_PROJECT_ID" main.tf; then
    sed -i '' "s/YOUR_GCP_PROJECT_ID/${CURRENT_PROJECT}/g" main.tf
    echo -e "${GREEN}✓ Updated main.tf with GCP project ID${NC}"
else
    echo -e "${GREEN}✓ GCP project ID already configured${NC}"
fi

# Prompt for Chainguard org if needed
if grep -q "YOUR_ORG" main.tf; then
    echo ""
    echo "Please enter your Chainguard organization name (e.g., 'chainguard-private'):"
    read -r CGR_ORG
    if [ -n "$CGR_ORG" ]; then
        sed -i '' "s/YOUR_ORG/${CGR_ORG}/g" main.tf
        echo -e "${GREEN}✓ Updated main.tf with Chainguard org: ${CGR_ORG}${NC}"
    else
        echo -e "${YELLOW}⚠️  Skipped Chainguard org update - you'll need to edit main.tf manually${NC}"
    fi
else
    echo -e "${GREEN}✓ Chainguard org already configured${NC}"
fi

# Create Terraform dev override
echo ""
echo "🔧 Configuring Terraform..."
TERRAFORMRC="$HOME/.terraformrc"
if grep -q "chainguard-dev/imagetest" "$TERRAFORMRC" 2>/dev/null; then
    echo -e "${GREEN}✓ Terraform dev override already configured${NC}"
else
    cat > "$TERRAFORMRC" <<EOF
provider_installation {
  dev_overrides {
    "chainguard-dev/imagetest" = "<YOUR_PATH_TO>/terraform-provider-imagetest"
  }
  direct {}
}
EOF
    echo -e "${GREEN}✓ Created Terraform dev override${NC}"
fi

# Initialize Terraform
echo ""
echo "🎯 Initializing Terraform..."
terraform init -upgrade

echo ""
echo -e "${GREEN}✅ Setup complete!${NC}"
echo ""
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo ""
echo "📌 Next steps:"
echo ""
echo "  1. Review the configuration:"
echo "     ${YELLOW}cat main.tf${NC}"
echo ""
echo "  2. See what will be created:"
echo "     ${YELLOW}terraform plan${NC}"
echo ""
echo "  3. Create cluster and run test (~15 min):"
echo "     ${YELLOW}terraform apply${NC}"
echo ""
echo "  4. Clean up when done:"
echo "     ${YELLOW}terraform destroy${NC}"
echo ""
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo ""
echo "💡 Tips:"
echo "  • First run takes ~15 minutes (GKE cluster creation)"
echo "  • Estimated cost: ~$0.10 per test run"
echo "  • Use 'terraform destroy' to clean up and avoid charges"
echo ""
echo "📚 For more info, see README.md or QUICKSTART.md"
echo ""
