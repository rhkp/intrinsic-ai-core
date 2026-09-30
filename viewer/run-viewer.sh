#!/usr/bin/env bash
set -euo pipefail
umask 077

export DISPLAY=:1
export XDG_RUNTIME_DIR=/run/intrinsic-viewer
export XAUTHORITY="$XDG_RUNTIME_DIR/Xauthority"
export LIBGL_ALWAYS_SOFTWARE=1

children=()
cleanup() {
  if ((${#children[@]})); then
    kill "${children[@]}" 2>/dev/null || true
    wait "${children[@]}" 2>/dev/null || true
  fi
}
trap cleanup EXIT
trap 'exit 0' TERM INT

touch "$XAUTHORITY"
chmod 600 "$XAUTHORITY"
printf 'add %s . %s\n' "$DISPLAY" "$(mcookie)" | xauth -q -f "$XAUTHORITY"

# SSH and owner-only Unix sockets provide access control and transport security.
# Neither the X server nor the VNC server accepts TCP connections.
Xtigervnc "$DISPLAY" -geometry 1600x900 -depth 24 \
  -desktop 'Intrinsic Simulation' -auth "$XAUTHORITY" -nolisten tcp \
  -rfbport -1 -rfbunixpath "$XDG_RUNTIME_DIR/vnc.sock" -rfbunixmode 0600 \
  -SecurityTypes None -AlwaysShared -FrameRate 20 -AcceptSetDesktopSize=0 \
  -AcceptCutText=0 -SendCutText=0 \
  > "$HOME/intrinsic-install-logs/viewer-xserver.log" 2>&1 &
children+=("$!")

for attempt in {1..30}; do
  if glxinfo -B >/dev/null 2>&1; then break; fi
  kill -0 "${children[0]}"
  sleep 1
done
glxinfo -B > "$HOME/intrinsic-install-logs/viewer-renderer.log"

dbus-run-session -- "$HOME/.local/lib/intrinsic-viewer/desktop-session.sh" \
  > "$HOME/intrinsic-install-logs/viewer-desktop.log" 2>&1 &
children+=("$!")

websockify --unix-listen "$XDG_RUNTIME_DIR/web.sock" --unix-listen-mode 0600 \
  --unix-target "$XDG_RUNTIME_DIR/vnc.sock" --web /usr/share/novnc --file-only \
  > "$HOME/intrinsic-install-logs/viewer-web.log" 2>&1 &
children+=("$!")

# A failed display, desktop, or proxy restarts the complete session via systemd.
wait -n "${children[@]}" || exit 1
exit 1
