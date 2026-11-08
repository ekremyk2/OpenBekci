#!/usr/bin/env sh
# Runs the pinned go-task (see server/go.mod `tool` directives); no global install needed.
set -eu
root="$(cd "$(dirname "$0")/.." && pwd)"
cd "$root/server"
exec go tool task --taskfile "$root/Taskfile.yml" --dir "$root" "$@"
