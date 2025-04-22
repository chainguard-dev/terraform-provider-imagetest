#!/bin/sh

kubectl get serviceaccount default -o jsonpath='{.metadata.namespace}' | grep default
