#!/bin/sh
# Writes one line of standard error longer than the log keeps, without a
# newline, then prints one effect.
head -c 70000 /dev/zero | tr '\0' a >&2
echo '{"name":"notice","data":{"text":"ok"}}'
