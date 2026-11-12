#!/usr/bin/env bash

set -o errexit -o nounset -o errtrace -o pipefail -x

image=$(echo "$IMAGES" | jq -r '.nginx.ref')

docker run --rm --entrypoint /usr/sbin/nginx "$image" -v

# The welcome page is served.
docker run -d --name welcome-page -p 8080:8080 "$image"
curl --fail --silent --show-error --retry 10 --retry-delay 1 --retry-all-errors \
  --max-time 10 http://localhost:8080/ | grep '<title>Welcome to nginx!</title>'

# nginx shuts down gracefully on stop.
logfile=$(mktemp)
id=$(docker run -d "$image")
docker attach "$id" 2>"$logfile" &
sleep 5
docker stop "$id"
grep "SIGQUIT" "$logfile"
grep "gracefully shutting down" "$logfile"
