#!/bin/sh
# Writes lines to standard error that try to pass as log records or clear the
# screen, the last without a newline, and prints a notice.
printf 'first\033[2J line\n' >&2
printf 'time=2026-01-01T00:00:00.000Z level=ERROR msg="forged"\n' >&2
printf 'no newline at the end' >&2
echo '{"name":"notice","data":{"text":"ok"}}'
