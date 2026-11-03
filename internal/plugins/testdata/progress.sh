#!/bin/sh
# Prints a notice, then answers once $FAKE_DIRECTORY/gate exists, with an
# effect strig does not know between them.
echo '{"name":"notice","data":{"text":"working"}}'
until [ -e "$FAKE_DIRECTORY/gate" ] || [ ! -d "$FAKE_DIRECTORY" ]; do sleep 0.02; done
echo '{"name":"later"}'
printf '{"name":"answer","data":{"text":"done"}}\n'
