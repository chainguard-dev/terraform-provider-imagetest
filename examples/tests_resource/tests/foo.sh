#!/bin/bash

echo "Hello World"

kubectl get po -A

echo "$IMAGES" | jq '.'
