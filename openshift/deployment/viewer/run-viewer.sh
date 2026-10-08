#!/usr/bin/env bash
set -euo pipefail
umask 077

mkdir -p "$HOME/.config/intrinsic-viewer" "$XDG_RUNTIME_DIR"
chmod 700 "$HOME" "$XDG_RUNTIME_DIR"
cp /opt/intrinsic-viewer/workcell.rviz "$HOME/.config/intrinsic-viewer/workcell.rviz"
touch "$XAUTHORITY"
cookie="$(mcookie)"
xauth -q -f "$XAUTHORITY" add "$DISPLAY" . "$cookie"

Xtigervnc "$DISPLAY" -geometry 1600x900 -depth 24 \
  -desktop 'Intrinsic dev01 Simulation' -auth "$XAUTHORITY" -nolisten tcp \
  -rfbport 5900 -localhost yes -SecurityTypes None -AlwaysShared \
  -FrameRate 20 -AcceptSetDesktopSize=0 -AcceptCutText=0 -SendCutText=0 &
vnc_pid=$!
websockify --web /usr/share/novnc --file-only 6080 localhost:5900 &
web_pid=$!
cleanup() {
  kill "$web_pid" "$vnc_pid" 2>/dev/null || true
  wait 2>/dev/null || true
}
trap cleanup EXIT INT TERM

for attempt in {1..30}; do
  if glxinfo -B >/dev/null 2>&1; then break; fi
  kill -0 "$vnc_pid"
  sleep 1
done
glxinfo -B
dbus-run-session -- /opt/intrinsic-viewer/desktop-session.sh
