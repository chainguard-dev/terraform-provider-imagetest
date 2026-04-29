#!/bin/bash
# Cleanup orphaned imagetest clusters older than a certain time

set -e

PROJECT_ID="${GOOGLE_PROJECT_ID:?GOOGLE_PROJECT_ID must be set}"
REGION="${GKE_REGION:-us-central1}"
MAX_AGE_HOURS="${MAX_AGE_HOURS:-2}"  # Delete clusters older than 2 hours

echo "🔍 Looking for orphaned imagetest clusters in project: $PROJECT_ID"
echo "   Will delete clusters older than $MAX_AGE_HOURS hours"
echo ""

# Get current timestamp
CURRENT_TIME=$(date +%s)
CUTOFF_TIME=$((CURRENT_TIME - (MAX_AGE_HOURS * 3600)))

# List all imagetest clusters
gcloud container clusters list \
  --project="$PROJECT_ID" \
  --filter="name:imagetest" \
  --format="value(name,location,createTime)" | \
while IFS=$'\t' read -r name location createTime; do
  # Convert createTime to epoch seconds
  # Format: 2026-03-06T10:30:00+00:00
  createEpoch=$(date -j -f "%Y-%m-%dT%H:%M:%S%z" "${createTime}" "+%s" 2>/dev/null || echo "0")
  
  if [ "$createEpoch" -lt "$CUTOFF_TIME" ]; then
    age_hours=$(( (CURRENT_TIME - createEpoch) / 3600 ))
    echo "🗑️  Deleting old cluster: $name (age: ${age_hours}h)"
    echo "   Location: $location"
    echo "   Created: $createTime"
    
    gcloud container clusters delete "$name" \
      --location="$location" \
      --project="$PROJECT_ID" \
      --quiet \
      --async
    
    echo "   ✓ Deletion initiated"
    echo ""
  else
    age_hours=$(( (CURRENT_TIME - createEpoch) / 3600 ))
    echo "✓ Keeping recent cluster: $name (age: ${age_hours}h)"
  fi
done

echo ""
echo "✅ Cleanup complete!"
echo ""
echo "💡 Tip: Set MAX_AGE_HOURS=1 to delete clusters older than 1 hour"
echo "   Example: MAX_AGE_HOURS=1 ./cleanup-old-clusters.sh"
