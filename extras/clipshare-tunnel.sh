#!/bin/bash

readonly LOCKFILE="$HOME/local/share/clipshare/.clipshare-tunnel.lock"
readonly SOCKFILE="$HOME/.clipshare.sock"
readonly REMOTE_SOCKFILE="$HOME/.clipshare.sock"

readonly PROGRAM="${0##*/}"

if [[ $# -ne 1 ]]; then
  echo "Use: ${PROGRAM} serverhost"
  exit 1
fi

# Limit to one instance. This is *not* foolproof, but
# should be enough for most purposes.
if [[ -s "${LOCKFILE}" ]]; then
  read -r pid <"${LOCKFILE}"
  if [[ -d "/proc/${pid}" ]]; then
    echo "${PROGRAM}: Another instance is already running (PID ${pid})"
    exit 1
  fi
fi
mypid="$$"
mkdir -p "${LOCKFILE%/*}"
echo "${mypid}" >"${LOCKFILE}"

server="$1"
# Attempt to login to remote host and setup a tunnel forever.
while :; do
  rm -f "${SOCKFILE}"
  ssh -N -o ExitOnForwardFailure=yes -L "${SOCKFILE}:${REMOTE_SOCKFILE}" "${server}" &
  ssh_pid="$!"
  trap 'kill $ssh_pid &>/dev/null 2>&1; sleep 1; rm -f "${LOCKFILE}"; echo "Terminated."; exit' SIGINT SIGHUP SIGTERM
  wait
done
