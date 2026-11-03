#!/bin/sh
# Prints an error effect, leaves a child holding standard output and dies on a
# signal that strig did not send.
sleep 300 &
echo $! > "$FAKE_DIRECTORY/child-process"
echo '{"name":"error","data":{"text":"out of memory"}}'
kill -KILL $$
