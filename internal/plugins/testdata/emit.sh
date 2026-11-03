#!/bin/sh
# Prints $FAKE_OUTPUT as is and exits with $FAKE_EXIT (default 0).
printf '%s' "$FAKE_OUTPUT"
exit "${FAKE_EXIT:-0}"
