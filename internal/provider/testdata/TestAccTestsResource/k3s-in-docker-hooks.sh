#!/bin/sh

# ensure something we created in the post hook is present
kubectl wait --for=condition=Ready pod foo -n default --timeout=2m
