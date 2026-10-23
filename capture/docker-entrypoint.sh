#!/bin/sh
# Container entrypoint for the capture helper.
#
# The app creates one private directory per capture attempt inside
# ARIVU_CAPTURE_RUNTIME_DIR, so that directory must exist and be writable by
# the shared arivu group before the app sends its first capture. The socket
# volume starts empty, so create it here (mode 0770) and then start the
# helper exactly as the systemd unit does.
set -eu
umask 0007
runtime_dir="${ARIVU_CAPTURE_RUNTIME_DIR:?ARIVU_CAPTURE_RUNTIME_DIR is required}"
mkdir -p "$runtime_dir"
chmod 0770 "$runtime_dir"
exec "$@"
