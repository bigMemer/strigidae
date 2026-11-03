#!/bin/sh
# Prints an error effect, leaves a child holding standard output, exits 1.
sleep 300 &
echo $! > "$FAKE_DIRECTORY/child-process"
echo '{"name":"error","data":{"text":"no model"}}'
exit 1
