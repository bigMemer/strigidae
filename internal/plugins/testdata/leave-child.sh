#!/bin/sh
# Starts a child that keeps standard output open, records its process number in
# $FAKE_DIRECTORY/child-process, prints an effect and exits 0.
sleep 300 &
echo $! > "$FAKE_DIRECTORY/child-process"
echo '{"name":"notice","data":{"text":"done"}}'
exit 0
