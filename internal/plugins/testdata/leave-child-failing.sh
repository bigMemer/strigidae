#!/bin/sh
# Like leave-child.sh, but the plugin itself exits 1.
sleep 300 &
echo $! > "$FAKE_DIRECTORY/child-process"
echo '{"name":"notice","data":{"text":"done"}}'
exit 1
