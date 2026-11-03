#!/bin/sh
# Reads the request with a plain read under set -e and saves it.
set -e
read -r line
printf '%s' "$line" > "$FAKE_DIRECTORY/line"
