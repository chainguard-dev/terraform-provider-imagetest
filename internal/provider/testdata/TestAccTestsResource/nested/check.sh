#!/bin/sh

set -eu

test -f ./sub/data.txt
test "$(cat ./sub/data.txt)" = "nested-content"

echo "nested content OK"
