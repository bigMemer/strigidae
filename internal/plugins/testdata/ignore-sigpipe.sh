#!/bin/sh
# Ignores SIGPIPE and prints effect lines without end: only a kill stops it.
trap '' PIPE
while :; do
  echo '{"name":"notice","data":{"text":"x"}}'
done
