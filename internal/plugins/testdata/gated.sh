#!/bin/sh
# One run at a time, checkable: takes a lock directory, records its input in
# $FAKE_DIRECTORY/order and its start in $FAKE_DIRECTORY/started, then waits for
# $FAKE_DIRECTORY/gate to exist and a little longer, so a run that starts alongside it
# finds the lock taken and writes $FAKE_DIRECTORY/overlap.
if ! mkdir "$FAKE_DIRECTORY/lock" 2>/dev/null; then
  echo overlap >> "$FAKE_DIRECTORY/overlap"
fi
cat >> "$FAKE_DIRECTORY/order"
echo >> "$FAKE_DIRECTORY/order"
echo started >> "$FAKE_DIRECTORY/started"  # a named pipe the test reads
until [ -e "$FAKE_DIRECTORY/gate" ] || [ ! -d "$FAKE_DIRECTORY" ]; do sleep 0.02; done
[ -e "$FAKE_DIRECTORY/gate" ] || exit 1
sleep 0.2
rmdir "$FAKE_DIRECTORY/lock"
echo '{"name":"notice","data":{"text":"ok"}}'
