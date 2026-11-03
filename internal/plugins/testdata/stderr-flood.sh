#!/bin/sh
# Writes a megabyte to standard error, then prints one effect.
yes 'xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx' | head -c 1048576 >&2
echo '{"name":"notice","data":{"text":"ok"}}'
