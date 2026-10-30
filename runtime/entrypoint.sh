#!/bin/sh
set -eu
if [ "${PW_VISUAL_BROWSER:-0}" = 1 ]; then
  export DISPLAY=:99
  Xvfb :99 -screen 0 1280x800x24 -nolisten tcp >/tmp/xvfb.log 2>&1 &
  x11vnc -display :99 -forever -shared -nopw -localhost -rfbport 5900 -wait 20 >/tmp/x11vnc.log 2>&1 &
  websockify --web=/usr/share/novnc 0.0.0.0:6080 localhost:5900 >/tmp/websockify.log 2>&1 &
fi
exec "$@"
