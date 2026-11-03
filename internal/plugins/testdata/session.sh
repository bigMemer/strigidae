#!/bin/sh
# Prints its process number and its session number as a notice.
session=$(ps -o sid= -p $$ 2>/dev/null | tr -d ' ')
printf '{"name":"notice","data":{"text":"%s %s"}}\n' "$$" "$session"
